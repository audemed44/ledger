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
	// Automatic is set once an import also saved an automatic import, and
	// AutomaticError says why one couldn't be saved.
	Automatic      bool   `json:"automatic,omitempty"`
	AutomaticError string `json:"automatic_error,omitempty"`
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
	s.pdf.Lock()
	defer s.pdf.Unlock()
	text, slot, err := statements.ExtractWithPasswords(ctx, f.Name(), passwords, slot)
	if err != nil {
		return out, err
	}
	out.Text, out.PasswordSlot = text.Layout, slot
	if p == nil {
		return out, nil
	}
	out.ParserName = p.Name
	statement, err := p.Parse(text)
	out.Statement = &statement
	if err != nil {
		out.ParseError = err.Error()
	} else if statement.Balanced {
		out.Fingerprint = statements.Fingerprint(statement)
	}
	return out, nil
}

// StatementImport is the result of importing a statement: how many lines it
// had, how many confirmed an alert transaction, and how many alerts in its
// period weren't on it and are now flagged.
type StatementImport struct {
	StatementID     int64 `json:"statement_id"`
	Count           int   `json:"count"`
	Matched         int   `json:"matched"`
	Flagged         int   `json:"flagged"`
	AlreadyImported bool  `json:"already_imported"`
}

// MatchWindowDays is how far apart an alert's date and its statement line's
// date can be.
const MatchWindowDays = 3

// postingGraceDays is how long before the statement date an alert may still
// be waiting to post; such alerts aren't flagged yet.
const postingGraceDays = 3

// firstStatementDays is how far back the first statement imported for an
// account is taken to reach, when flagging alerts it doesn't contain.
const firstStatementDays = 28

// ImportStatement saves a validated statement and all its rows atomically.
// preview must be freshly parsed on the server (never rows from a client),
// and expected the fingerprint the user reviewed.
//
// Each line is reconciled: a provisional alert transaction on the same
// account with the same amount, currency and direction, dated within
// MatchWindowDays, becomes that line, confirmed. Lines without an alert are
// added. Alerts in the statement's period that it doesn't contain are
// flagged.
//
// Importing the same statement again, from any email, changes nothing. A
// different statement for the same account and date blocks the import.
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
		out.StatementID, out.Matched, out.Flagged, err = reconcile(tx, messageID, part, st, fingerprint)
		if err != nil {
			return out, err
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

// reconcile saves a new statement and its lines in tx, matching each line to
// an alert transaction where one fits, then flags the period's alerts that
// weren't on it.
func reconcile(tx *sql.Tx, messageID int64, part int, st statements.Statement, fingerprint string) (id int64, matched, flagged int, err error) {
	// The period starts where the account's previous statement's grace
	// window did, or firstStatementDays back.
	var previous sql.NullString
	err = tx.QueryRow("SELECT max(date) FROM statements WHERE account_key=? AND date<?", st.AccountID, st.Date).Scan(&previous)
	if err != nil {
		return 0, 0, 0, err
	}
	from, back := st.Date, firstStatementDays
	if previous.Valid {
		from, back = previous.String, postingGraceDays
	}

	raw, err := json.Marshal(st)
	if err != nil {
		return 0, 0, 0, err
	}
	result, err := tx.Exec("INSERT INTO statements(account_key,date,fingerprint,snapshot) VALUES(?,?,?,?)",
		st.AccountID, st.Date, fingerprint, string(raw))
	if err != nil {
		return 0, 0, 0, err
	}
	if id, err = result.LastInsertId(); err != nil {
		return 0, 0, 0, err
	}
	for index, row := range st.Transactions {
		day := row.Date[:10]
		var alert int64
		err = tx.QueryRow(`SELECT id FROM transactions
WHERE statement_id IS NULL AND status IN ('provisional','flagged','dismissed')
  AND account=? AND (lower(trim(issuer))=lower(trim(?)) OR account_kind='unknown')
  AND (account_kind=? OR account_kind='unknown')
  AND amount=? AND currency=? AND direction=?
  AND abs(julianday(substr(date,1,10))-julianday(?))<=?
ORDER BY (reference<>'' AND reference=?) DESC, abs(julianday(substr(date,1,10))-julianday(?)), id LIMIT 1`,
			row.Account, row.Issuer, row.AccountKind, row.Amount, row.Currency, row.Direction,
			day, MatchWindowDays, row.Reference, day).Scan(&alert)
		switch {
		case err == nil:
			// The alert's own merchant name and date are kept, unless the
			// name was only its parser's description.
			_, err = tx.Exec(`UPDATE transactions SET status='confirmed',statement_id=?,row_index=?,
  merchant=CASE WHEN placeholder=1 THEN ? ELSE merchant END, placeholder=0 WHERE id=?`,
				id, index+1, row.Merchant, alert)
			matched++
		case errors.Is(err, sql.ErrNoRows):
			_, err = tx.Exec(`INSERT INTO transactions(message_id,source_part,row_index,statement_id,merchant,account,amount,currency,direction,date,reference,status,issuer,account_kind)
VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
				messageID, part, index+1, id, row.Merchant, row.Account, row.Amount, row.Currency,
				row.Direction, row.Date, row.Reference, "confirmed", row.Issuer, row.AccountKind)
		}
		if err != nil {
			return 0, 0, 0, err
		}
	}

	r, err := tx.Exec(`UPDATE transactions SET status='flagged'
WHERE statement_id IS NULL AND status='provisional'
  AND account=? AND (lower(trim(issuer))=lower(trim(?)) OR account_kind='unknown')
  AND (account_kind=? OR account_kind='unknown')
  AND substr(date,1,10)>date(?,?) AND substr(date,1,10)<=date(?,?)`,
		st.Account, st.Issuer, st.AccountKind, from, fmt.Sprintf("-%d days", back),
		st.Date, fmt.Sprintf("-%d days", postingGraceDays))
	if err != nil {
		return 0, 0, 0, err
	}
	n, err := r.RowsAffected()
	return id, matched, int(n), err
}
