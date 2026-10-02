package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"

	"github.com/audemed44/ledger/internal/mail"
	"github.com/audemed44/ledger/internal/statements"
)

// PDFPreview is a PDF attachment's extracted text and, with a parser, the
// statement it parses to. Fingerprint is set only when the statement
// validated, and must be sent back to import it.
type PDFPreview struct {
	Fingerprint  string                `json:"fingerprint,omitempty"`
	Imported     *StatementImport      `json:"imported,omitempty"`
	Text         string                `json:"text"`
	PasswordSlot int                   `json:"password_slot"`
	ParserName   string                `json:"parser_name,omitempty"`
	Statement    *statements.Statement `json:"statement,omitempty"`
	ParseError   string                `json:"parse_error,omitempty"`
}

// PreviewPDF extracts the PDF in MIME part `part` of a message and, when p
// is set, parses it. Nothing is imported.
func (s *Store) PreviewPDF(ctx context.Context, id int64, part int, p *statements.Parser, passwords []string) (PDFPreview, error) {
	var out PDFPreview
	if part < 0 {
		return out, errors.New("Invalid attachment")
	}
	raw, err := s.ArchivedRaw(id)
	if err != nil {
		return out, errors.New("Could not read archived email")
	}
	f, err := os.CreateTemp("", "ledger-attachment-*.pdf")
	if err != nil {
		return out, errors.New("Could not prepare PDF for extraction")
	}
	defer os.Remove(f.Name())
	err = mail.WritePDF(raw, part, f)
	if closeErr := f.Close(); err == nil && closeErr != nil {
		err = errors.New("PDF exceeds size limit or could not be read")
	}
	if err != nil {
		return out, err
	}
	slot := 0
	if p != nil {
		slot = p.PasswordSlot
	}
	out.Text, out.PasswordSlot, err = statements.ExtractWithPasswords(ctx, f.Name(), passwords, slot)
	if err != nil {
		return out, err
	}
	if p == nil {
		return out, nil
	}
	out.ParserName = p.Name
	statement, err := p.Parse(out.Text)
	out.Statement = &statement
	if err != nil {
		out.ParseError = err.Error()
	} else if statement.Balanced {
		out.Fingerprint = statements.Fingerprint(statement)
	}
	return out, nil
}

// StatementImport is the result of importing a statement.
type StatementImport struct {
	StatementID     int64 `json:"statement_id"`
	Count           int   `json:"count"`
	AlreadyImported bool  `json:"already_imported"`
}

// ImportStatement saves a validated statement and all its rows atomically.
// preview must be freshly parsed on the server (never rows from a client),
// and expected the fingerprint the user reviewed.
//
// Importing the same statement again, from any email, changes nothing. A
// different statement for the same account and date, or a row that might
// duplicate an existing transaction, blocks the whole import.
func (s *Store) ImportStatement(messageID int64, part int, preview PDFPreview, expected string) (StatementImport, error) {
	out := StatementImport{}
	if preview.ParseError != "" || preview.Statement == nil || !preview.Statement.Balanced ||
		len(preview.Statement.Transactions) == 0 {
		return out, errors.New("Statement validation must pass before importing; review the extracted rows and error")
	}
	st := *preview.Statement
	fingerprint := statements.Fingerprint(st)
	if expected == "" || expected != fingerprint {
		return out, errors.New("Statement preview changed; extract and review it again before importing")
	}
	// Decode the archive before taking the database (it has one connection).
	message, err := s.ReviewMessage(messageID)
	if err != nil {
		return out, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	tx, err := s.DB.Begin()
	if err != nil {
		return out, err
	}
	defer tx.Rollback()
	var state string
	if err = tx.QueryRow("SELECT state FROM messages WHERE id=?", messageID).Scan(&state); err != nil {
		return out, err
	}
	if state != "queued" && state != "statement" {
		return out, errors.New("This email is not available for statement import")
	}

	var existingFingerprint string
	err = tx.QueryRow("SELECT id,fingerprint FROM statements WHERE account_key=? AND date=?", st.AccountID, st.Date).
		Scan(&out.StatementID, &existingFingerprint)
	switch {
	case err == nil:
		if existingFingerprint != fingerprint {
			return out, errors.New("A different statement for this account and date is already imported; review the revision before importing")
		}
		out.AlreadyImported = true
	case !errors.Is(err, sql.ErrNoRows):
		return out, err
	default:
		// Conservative overlap check: don't guess whether similar alert and
		// statement rows are the same purchase.
		for _, row := range st.Transactions {
			var count int
			err = tx.QueryRow(`SELECT count(*) FROM transactions
WHERE account=? AND (lower(trim(issuer))=lower(trim(?)) OR account_kind='unknown')
  AND (account_kind=? OR account_kind='unknown')
  AND amount=? AND currency=? AND direction=? AND substr(date,1,10)=?`,
				row.Account, row.Issuer, row.AccountKind, row.Amount, row.Currency, row.Direction, row.Date[:10]).Scan(&count)
			if err != nil {
				return out, err
			}
			if count > 0 {
				return out, errors.New("Possible overlap with existing transactions for this account suffix, date and amount; nothing imported. Reconciliation is required")
			}
		}
		raw, err := json.Marshal(st)
		if err != nil {
			return out, err
		}
		result, err := tx.Exec("INSERT INTO statements(account_key,date,fingerprint,snapshot) VALUES(?,?,?,?)",
			st.AccountID, st.Date, fingerprint, string(raw))
		if err != nil {
			return out, err
		}
		if out.StatementID, err = result.LastInsertId(); err != nil {
			return out, err
		}
		for index, row := range st.Transactions {
			_, err = tx.Exec(`INSERT INTO transactions(message_id,source_part,row_index,statement_id,merchant,account,amount,currency,direction,date,reference,status,issuer,account_kind)
VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
				messageID, part, index+1, out.StatementID, row.Merchant, row.Account, row.Amount, row.Currency,
				row.Direction, row.Date, row.Reference, "confirmed", row.Issuer, row.AccountKind)
			if err != nil {
				return out, err
			}
		}
	}
	out.Count = len(st.Transactions)

	// A MIME part can only refer to one statement; never silently replace provenance.
	var mapped int64
	err = tx.QueryRow("SELECT statement_id FROM statement_sources WHERE message_id=? AND part=?", messageID, part).Scan(&mapped)
	if err == nil && mapped != out.StatementID {
		return out, errors.New("This attachment is already linked to a different statement")
	}
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return out, err
	}
	_, err = tx.Exec("INSERT OR IGNORE INTO statement_sources(message_id,part,statement_id) VALUES(?,?,?)",
		messageID, part, out.StatementID)
	if err != nil {
		return out, err
	}

	// The email leaves the inbox once every PDF in it is imported.
	remaining := 0
	for _, attachment := range message.Attachments {
		if !mail.IsPDF(attachment.ContentType, attachment.Name) {
			continue
		}
		var count int
		err = tx.QueryRow("SELECT count(*) FROM statement_sources WHERE message_id=? AND part=?",
			messageID, attachment.Part).Scan(&count)
		if err != nil {
			return out, err
		}
		if count == 0 {
			remaining++
		}
	}
	state, reason := "statement", ""
	if remaining > 0 {
		state, reason = "queued", fmt.Sprintf("%d PDF attachment(s) still need review", remaining)
	}
	if _, err = tx.Exec("UPDATE messages SET state=?,reason=? WHERE id=?", state, reason, messageID); err != nil {
		return out, err
	}
	return out, tx.Commit()
}
