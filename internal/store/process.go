package store

import (
	"strings"
	"time"

	"github.com/audemed44/ledger/internal/alerts"
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

	tx, err := s.DB.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	state := "ignored"
	if t := chosen.Transaction; t != nil {
		// An alert arriving after its statement was imported may duplicate a
		// statement line; it waits for reconciliation instead.
		var overlaps int
		err = tx.QueryRow(`SELECT count(*) FROM transactions
WHERE statement_id IS NOT NULL AND account=?
  AND (lower(trim(issuer))=lower(trim(?)) OR ?='unknown') AND (account_kind=? OR ?='unknown')
  AND amount=? AND currency=? AND direction=? AND substr(date,1,10)=?`,
			t.Account, t.Issuer, t.AccountKind, t.AccountKind, t.AccountKind,
			t.Amount, t.Currency, t.Direction, t.Date[:10]).Scan(&overlaps)
		if err != nil {
			return err
		}
		if overlaps > 0 {
			_, err = tx.Exec("UPDATE messages SET reason=? WHERE id=?",
				"Possible match to an imported statement transaction; reconciliation required", id)
			if err != nil {
				return err
			}
			return tx.Commit()
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
			if err = s.Process(id); err != nil {
				return count, err
			}
			last = id
			count++
		}
	}
}
