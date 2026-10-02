package ledger

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
)

// Preserve IDs and alert provenance while allowing several rows per statement.
// All DDL runs in one transaction; a failed upgrade leaves the old schema intact.
func (s *Store) migrateStatementImports() error {
	version, err := s.Setting("statement-import-schema")
	if err != nil || version == "1" {
		return err
	}
	tx, err := s.DB.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	_, err = tx.Exec(`
 ALTER TABLE messages ADD COLUMN has_pdf INTEGER NOT NULL DEFAULT 0;
 CREATE TABLE statements(id INTEGER PRIMARY KEY, account_key TEXT NOT NULL, date TEXT NOT NULL, fingerprint TEXT NOT NULL, snapshot TEXT NOT NULL, UNIQUE(account_key,date));
 CREATE TABLE statement_sources(message_id INTEGER NOT NULL REFERENCES messages(id), part INTEGER NOT NULL, statement_id INTEGER NOT NULL REFERENCES statements(id), PRIMARY KEY(message_id,part));
 CREATE TABLE transactions_new(id INTEGER PRIMARY KEY, message_id INTEGER NOT NULL REFERENCES messages(id), merchant TEXT NOT NULL, account TEXT NOT NULL, amount INTEGER NOT NULL CHECK(amount>0), currency TEXT NOT NULL, direction TEXT NOT NULL, date TEXT NOT NULL, reference TEXT NOT NULL, status TEXT NOT NULL, issuer TEXT NOT NULL, account_kind TEXT NOT NULL DEFAULT 'unknown', source_part INTEGER NOT NULL DEFAULT -1, row_index INTEGER NOT NULL DEFAULT 0, statement_id INTEGER REFERENCES statements(id), UNIQUE(message_id,source_part,row_index), UNIQUE(statement_id,row_index));
 INSERT INTO transactions_new(id,message_id,merchant,account,amount,currency,direction,date,reference,status,issuer,account_kind) SELECT id,message_id,merchant,account,amount,currency,direction,date,reference,status,issuer,account_kind FROM transactions;
 DROP TABLE transactions;
 ALTER TABLE transactions_new RENAME TO transactions;
 CREATE INDEX transactions_date ON transactions(date);
 INSERT INTO settings(key,value) VALUES('statement-import-schema','1');`)
	if err != nil {
		return err
	}
	return tx.Commit()
}

func statementFingerprint(s Statement) string {
	// Validation preferences must not change the identity of the financial rows.
	s.BalanceTolerancePaise = 0
	s.RoundingAccepted = false
	s.Warnings = nil
	raw, _ := json.Marshal(s)
	hash := sha256.Sum256(raw)
	return hex.EncodeToString(hash[:])
}

type StatementImport struct {
	StatementID     int64 `json:"statement_id"`
	Count           int   `json:"count"`
	AlreadyImported bool  `json:"already_imported"`
}

// Imports always use freshly parsed server-side data, never client-supplied rows.
func (s *Store) importStatement(messageID int64, part int, preview PDFPreview, expected string) (StatementImport, error) {
	out := StatementImport{}
	if preview.ParseError != "" || preview.Statement == nil || !preview.Statement.Balanced || len(preview.Statement.Transactions) == 0 {
		return out, errors.New("Statement validation must pass before importing; review the extracted rows and error")
	}
	st := *preview.Statement
	fingerprint := statementFingerprint(st)
	if expected == "" || expected != fingerprint {
		return out, errors.New("Statement preview changed; extract and review it again before importing")
	}
	// Decode archive before acquiring the DB transaction (single SQLite connection).
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
	err = tx.QueryRow("SELECT id,fingerprint FROM statements WHERE account_key=? AND date=?", st.AccountID, st.Date).Scan(&out.StatementID, &existingFingerprint)
	switch {
	case err == nil:
		if existingFingerprint != fingerprint {
			return out, errors.New("A different statement for this account and date is already imported; review the revision before importing")
		}
		out.AlreadyImported = true
	case !errors.Is(err, sql.ErrNoRows):
		return out, err
	default:
		// Conservative overlap check: don't guess whether similar alert/statement rows
		// are the same purchase. A conflict blocks the entire import, atomically.
		for _, row := range st.Transactions {
			var count int
			err = tx.QueryRow(`SELECT count(*) FROM transactions WHERE account=? AND (lower(trim(issuer))=lower(trim(?)) OR account_kind='unknown') AND (account_kind=? OR account_kind='unknown') AND amount=? AND currency=? AND direction=? AND substr(date,1,10)=?`, row.Account, row.Issuer, row.AccountKind, row.Amount, row.Currency, row.Direction, row.Date[:10]).Scan(&count)
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
		result, err := tx.Exec("INSERT INTO statements(account_key,date,fingerprint,snapshot) VALUES(?,?,?,?)", st.AccountID, st.Date, fingerprint, string(raw))
		if err != nil {
			return out, err
		}
		out.StatementID, err = result.LastInsertId()
		if err != nil {
			return out, err
		}
		for index, row := range st.Transactions {
			_, err = tx.Exec(`INSERT INTO transactions(message_id,source_part,row_index,statement_id,merchant,account,amount,currency,direction,date,reference,status,issuer,account_kind) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?)`, messageID, part, index+1, out.StatementID, row.Merchant, row.Account, row.Amount, row.Currency, row.Direction, row.Date, row.Reference, "confirmed", row.Issuer, row.AccountKind)
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
	if _, err = tx.Exec("INSERT OR IGNORE INTO statement_sources(message_id,part,statement_id) VALUES(?,?,?)", messageID, part, out.StatementID); err != nil {
		return out, err
	}
	remaining := 0
	for _, attachment := range message.Attachments {
		if !isPDF(attachment.ContentType, attachment.Name) {
			continue
		}
		var count int
		if err = tx.QueryRow("SELECT count(*) FROM statement_sources WHERE message_id=? AND part=?", messageID, attachment.Part).Scan(&count); err != nil {
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
