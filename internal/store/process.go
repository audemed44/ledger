package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/audemed44/ledger/internal/alerts"
	"github.com/audemed44/ledger/internal/ledger"
	"github.com/audemed44/ledger/internal/mail"
	"github.com/audemed44/ledger/internal/statements"
)

const noParserReason = "No matching alert parser"

// legacyStatementReason is what older versions queued PDFs with.
const legacyStatementReason = "Statement attachment — PDF parsing arrives in phase 2"

// Process runs the alert parsers over one queued message. Exactly one
// enabled parser must match; otherwise the message stays queued with the
// reason. PDFs and undecodable mail never become alert transactions.
func (s *Store) Process(id int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	m, err := s.Message(id)
	if err != nil {
		return err
	}
	if m.State != "queued" {
		return nil
	}
	if date, e := time.Parse(time.RFC3339, m.Date); e == nil && !date.IsZero() && date.Format("2006-01-02") < BackfillStart {
		_, err = s.DB.Exec("UPDATE messages SET state='excluded',reason=? WHERE id=?", BeforeBackfillReason, id)
		return err
	}
	if m.HasPDF || strings.HasPrefix(m.Reason, "MIME:") || m.Reason == legacyStatementReason {
		return nil
	}
	parsers, err := s.Parsers()
	if err != nil {
		return err
	}
	var chosen *alerts.Preview
	reason := noParserReason
	for _, p := range parsers {
		if !p.Enabled {
			continue
		}
		result, e := p.Parse(m.Sender, m.Subject, m.Body)
		if e != nil {
			reason = "Parser " + p.Name + ": " + e.Error()
			chosen = nil
			break
		}
		if !result.Matched {
			continue
		}
		if chosen != nil {
			chosen = nil
			reason = "Multiple parsers match; narrow their sender or subject patterns"
			break
		}
		chosen = &result
	}
	if chosen == nil {
		_, err = s.DB.Exec("UPDATE messages SET reason=? WHERE id=?", reason, id)
		return err
	}

	if t := chosen.Transaction; t != nil && t.AccountKind == "debit" {
		// A debit card's transactions belong to its bank account.
		account, err := s.linkedAccount(t.Issuer, t.Account)
		if errors.Is(err, sql.ErrNoRows) {
			_, err = s.DB.Exec("UPDATE messages SET reason=? WHERE id=?", fmt.Sprintf(
				"%s debit card ••%s isn't linked to a bank account; link it under Parsers → Debit cards, then retry", t.Issuer, t.Account), id)
			return err
		}
		if err != nil {
			return err
		}
		if t.Reference == "" {
			t.Reference = "Debit card ••" + t.Account
		}
		t.Account, t.AccountKind = account, "bank"
		t.AccountID = ledger.AccountKey(t.Issuer, "bank", account)
	}
	tx, err := s.DB.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	state := "ignored"
	if t := chosen.Transaction; t != nil {
		// An alert arriving after its statement was imported confirms the
		// statement line instead of adding a second transaction.
		var line int64
		err = tx.QueryRow(`SELECT id FROM transactions
WHERE statement_id IS NOT NULL AND source_part>=0 AND alert_message_id IS NULL AND account=?
  AND (lower(trim(issuer))=lower(trim(?)) OR ?='unknown') AND (account_kind=? OR ?='unknown')
  AND amount=? AND currency=? AND direction=?
  AND abs(julianday(substr(date,1,10))-julianday(?))<=?
ORDER BY (reference<>'' AND reference=?) DESC, abs(julianday(substr(date,1,10))-julianday(?)), id LIMIT 1`,
			t.Account, t.Issuer, t.AccountKind, t.AccountKind, t.AccountKind,
			t.Amount, t.Currency, t.Direction, t.Date[:10], MatchWindowDays, t.Reference, t.Date[:10]).Scan(&line)
		if err == nil {
			if _, err = tx.Exec("UPDATE transactions SET alert_message_id=? WHERE id=?", id, line); err != nil {
				return err
			}
			if _, err = tx.Exec("UPDATE messages SET state='parsed',reason='' WHERE id=?", id); err != nil {
				return err
			}
			return tx.Commit()
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		state = "parsed"
		_, err = tx.Exec(`INSERT INTO transactions(message_id,merchant,account,amount,currency,direction,date,reference,status,issuer,account_kind)
VALUES(?,?,?,?,?,?,?,?,?,?,?)`,
			id, t.Merchant, t.Account, t.Amount, t.Currency, t.Direction, t.Date, t.Reference, t.Status, t.Issuer, t.AccountKind)
		if err != nil {
			return err
		}
	}
	if _, err = tx.Exec("UPDATE messages SET state=?,reason='' WHERE id=?", state, id); err != nil {
		return err
	}
	return tx.Commit()
}

// Reprocess retries the whole queued backlog, after recovering any message
// whose text couldn't be read before. It returns how many it tried.
func (s *Store) Reprocess() (int, error) {
	s.mu.Lock()
	err := s.recoverQueuedText(false)
	s.mu.Unlock()
	if err != nil {
		return 0, err
	}
	var last int64
	count := 0
	for {
		ids, err := s.queuedIDs("SELECT id FROM messages WHERE state='queued' AND id>? ORDER BY id LIMIT 100", last)
		if err != nil {
			return count, err
		}
		if len(ids) == 0 {
			return count, nil
		}
		for _, id := range ids {
			if err = s.process(id); err != nil {
				return count, err
			}
			last = id
			count++
		}
	}
}

// pdfTimeout bounds one automatic PDF extraction.
const pdfTimeout = 2 * time.Minute

// AutoImport imports a queued email's statement PDFs with the statement
// parser whose trigger fits each one. A PDF that no trigger fits, that more
// than one fits, or that doesn't validate stays in the inbox with the
// reason, for review by hand. Other mail is left alone.
func (s *Store) AutoImport(ctx context.Context, id int64) error {
	m, err := s.Message(id)
	if err != nil || m.State != "queued" || !m.HasPDF {
		return err
	}
	parsers, err := s.StatementParsers()
	if err != nil {
		return err
	}
	automatic := false
	for _, p := range parsers {
		automatic = automatic || len(p.Triggers) > 0
	}
	if !automatic {
		return nil
	}
	if m, err = s.ReviewMessage(id); err != nil || m.ContentError != "" {
		return err
	}
	reasons := []string{}
	for _, a := range m.Attachments {
		if !mail.IsPDF(a.ContentType, a.Name) {
			continue
		}
		var done int
		err = s.DB.QueryRow("SELECT count(*) FROM statement_sources WHERE message_id=? AND part=?", id, a.Part).Scan(&done)
		if err != nil {
			return err
		}
		if done > 0 {
			continue
		}
		var chosen []statements.Parser
		for _, p := range parsers {
			if p.Matches(m.Sender, m.Subject, a.Name) {
				chosen = append(chosen, p)
			}
		}
		if len(chosen) != 1 {
			if len(chosen) == 0 {
				reasons = append(reasons, a.Name+": no statement parser imports this automatically; review it to import by hand")
			} else {
				reasons = append(reasons, a.Name+": several statement parsers would import this; remove the extra automatic import")
			}
			continue
		}
		p := chosen[0]
		reason := func(why string) {
			reasons = append(reasons, fmt.Sprintf("%s: automatic import with %s failed: %s", a.Name, p.Name, why))
		}
		extract, cancel := context.WithTimeout(ctx, pdfTimeout)
		preview, err := s.PreviewPDF(extract, id, a.Part, &p, s.PDFPasswords)
		cancel()
		switch {
		case err != nil:
			reason(err.Error())
		case preview.ParseError != "":
			reason(preview.ParseError)
		case preview.Fingerprint == "":
			reason("the statement didn't balance")
		default:
			if _, err = s.ImportStatement(id, a.Part, preview, preview.Fingerprint); err != nil {
				reason(err.Error())
			}
		}
	}
	if len(reasons) == 0 {
		return nil
	}
	_, err = s.DB.Exec("UPDATE messages SET reason=? WHERE id=? AND state='queued'", strings.Join(reasons, "; "), id)
	return err
}
