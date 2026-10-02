package ledger

import (
	"database/sql"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	_ "modernc.org/sqlite"
)

type Store struct {
	DB  *sql.DB
	Dir string
	mu  sync.Mutex
}

type Message struct {
	ID           int64        `json:"id"`
	Key          string       `json:"-"`
	Sender       string       `json:"sender"`
	Subject      string       `json:"subject"`
	Date         string       `json:"date"`
	Body         string       `json:"body,omitempty"`
	State        string       `json:"state"`
	Reason       string       `json:"reason"`
	Archive      string       `json:"-"`
	BodyFormat   string       `json:"body_format,omitempty"`
	Attachments  []Attachment `json:"attachments,omitempty"`
	HasPDF       bool         `json:"has_pdf,omitempty"`
	CanParse     bool         `json:"can_parse"`
	ContentError string       `json:"content_error,omitempty"`
}

func Open(dir string) (*Store, error) {
	if err := os.MkdirAll(filepath.Join(dir, "archive"), 0700); err != nil {
		return nil, err
	}
	// Protect SQLite WAL/SHM files as well as the primary database.
	if err := os.Chmod(dir, 0700); err != nil {
		return nil, err
	}
	db, err := sql.Open("sqlite", filepath.Join(dir, "ledger.db"))
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	_, err = db.Exec(`PRAGMA journal_mode=WAL; PRAGMA busy_timeout=5000; PRAGMA foreign_keys=ON;
 CREATE TABLE IF NOT EXISTS messages(id INTEGER PRIMARY KEY, message_key TEXT UNIQUE NOT NULL, sender TEXT NOT NULL, subject TEXT NOT NULL, date TEXT NOT NULL, body TEXT NOT NULL, state TEXT NOT NULL DEFAULT 'queued', reason TEXT NOT NULL DEFAULT '', archive TEXT NOT NULL);
 CREATE TABLE IF NOT EXISTS parsers(id INTEGER PRIMARY KEY, definition TEXT NOT NULL);
 CREATE TABLE IF NOT EXISTS statement_parsers(id INTEGER PRIMARY KEY, definition TEXT NOT NULL);
 CREATE TABLE IF NOT EXISTS transactions(id INTEGER PRIMARY KEY, message_id INTEGER UNIQUE NOT NULL REFERENCES messages(id), merchant TEXT NOT NULL, account TEXT NOT NULL, amount INTEGER NOT NULL CHECK(amount>0), currency TEXT NOT NULL, direction TEXT NOT NULL, date TEXT NOT NULL, reference TEXT NOT NULL, status TEXT NOT NULL, issuer TEXT NOT NULL);
 CREATE INDEX IF NOT EXISTS transactions_date ON transactions(date);
 CREATE TABLE IF NOT EXISTS settings(key TEXT PRIMARY KEY, value TEXT NOT NULL);`)
	if err != nil {
		db.Close()
		return nil, err
	}
	if err = os.Chmod(filepath.Join(dir, "ledger.db"), 0600); err != nil {
		db.Close()
		return nil, err
	}
	store := &Store{DB: db, Dir: dir}
	if err := store.migrateAccountKinds(); err != nil {
		db.Close()
		return nil, err
	}
	if err := store.migrateStatementImports(); err != nil {
		db.Close()
		return nil, err
	}
	if err := store.mergeDuplicateStatementParsers(); err != nil {
		db.Close()
		return nil, err
	}
	// Keep archives and imported transactions; retire only older pending mail.
	if _, err := db.Exec("UPDATE messages SET state='excluded',reason=? WHERE state='queued' AND substr(date,1,10) < ? AND date GLOB '????-??-??T*' AND date NOT LIKE '0001-%'", beforeBackfillReason, backfillStart); err != nil {
		db.Close()
		return nil, err
	}
	if err := store.recoverArchivedText(); err != nil {
		db.Close()
		return nil, err
	}
	return store, nil
}

func (s *Store) Parsers() ([]Parser, error) {
	rows, err := s.DB.Query("SELECT id,definition FROM parsers ORDER BY id")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Parser{}
	for rows.Next() {
		var id int64
		var raw string
		if err = rows.Scan(&id, &raw); err != nil {
			return nil, err
		}
		var p Parser
		if err = json.Unmarshal([]byte(raw), &p); err != nil {
			return nil, err
		}
		p.ID = id
		out = append(out, p)
	}
	return out, rows.Err()
}
func (s *Store) SaveParser(p Parser) (Parser, error) {
	if err := p.Validate(); err != nil {
		return p, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	raw, err := json.Marshal(p)
	if err != nil {
		return p, err
	}
	if p.ID == 0 {
		r, e := s.DB.Exec("INSERT INTO parsers(definition) VALUES(?)", string(raw))
		if e != nil {
			return p, e
		}
		p.ID, err = r.LastInsertId()
	} else {
		r, e := s.DB.Exec("UPDATE parsers SET definition=? WHERE id=?", string(raw), p.ID)
		if e != nil {
			return p, e
		}
		n, e := r.RowsAffected()
		if e != nil {
			return p, e
		}
		if n == 0 {
			return p, sql.ErrNoRows
		}
	}
	return p, err
}
func (s *Store) Messages() ([]Message, error) { return s.FilteredMessages("") }
func (s *Store) FilteredMessages(kind string) ([]Message, error) {
	query := "SELECT id,sender,subject,date,state,reason,has_pdf FROM messages WHERE state='queued'"
	switch kind {
	case "pdf":
		query += " AND has_pdf=1"
	case "text":
		query += " AND has_pdf=0"
	case "", "all":
	default:
		return nil, errors.New("Invalid inbox filter")
	}
	query += " ORDER BY julianday(date) DESC,id DESC LIMIT 200"
	rows, err := s.DB.Query(query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Message{}
	for rows.Next() {
		var m Message
		if err = rows.Scan(&m.ID, &m.Sender, &m.Subject, &m.Date, &m.State, &m.Reason, &m.HasPDF); err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}
func (s *Store) Message(id int64) (Message, error) {
	var m Message
	err := s.DB.QueryRow("SELECT id,sender,subject,date,body,state,reason,has_pdf FROM messages WHERE id=?", id).Scan(&m.ID, &m.Sender, &m.Subject, &m.Date, &m.Body, &m.State, &m.Reason, &m.HasPDF)
	return m, err
}
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
	if date, e := time.Parse(time.RFC3339, m.Date); e == nil && !date.IsZero() && date.Format("2006-01-02") < backfillStart {
		_, err = s.DB.Exec("UPDATE messages SET state='excluded',reason=? WHERE id=?", beforeBackfillReason, id)
		return err
	}
	// MIME failures and statements cannot accidentally become alert transactions.
	if m.HasPDF || strings.HasPrefix(m.Reason, "MIME:") || m.Reason == "Statement attachment — PDF parsing arrives in phase 2" {
		return nil
	}
	parsers, err := s.Parsers()
	if err != nil {
		return err
	}
	var chosen *Preview
	reason := "No matching alert parser"
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
		if result.Matched {
			if chosen != nil {
				chosen = nil
				reason = "Multiple parsers match; narrow their sender or subject patterns"
				break
			}
			chosen = &result
		}
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
		var overlaps int
		if err = tx.QueryRow("SELECT count(*) FROM transactions WHERE statement_id IS NOT NULL AND account=? AND (lower(trim(issuer))=lower(trim(?)) OR ?='unknown') AND (account_kind=? OR ?='unknown') AND amount=? AND currency=? AND direction=? AND substr(date,1,10)=?", t.Account, t.Issuer, t.AccountKind, t.AccountKind, t.AccountKind, t.Amount, t.Currency, t.Direction, t.Date[:10]).Scan(&overlaps); err != nil {
			return err
		}
		if overlaps > 0 {
			if _, err = tx.Exec("UPDATE messages SET reason=? WHERE id=?", "Possible match to an imported statement transaction; reconciliation required", id); err != nil {
				return err
			}
			return tx.Commit()
		}
		state = "parsed"
		_, err = tx.Exec("INSERT INTO transactions(message_id,merchant,account,amount,currency,direction,date,reference,status,issuer,account_kind) VALUES(?,?,?,?,?,?,?,?,?,?,?)", id, t.Merchant, t.Account, t.Amount, t.Currency, t.Direction, t.Date, t.Reference, t.Status, t.Issuer, t.AccountKind)
		if err != nil {
			return err
		}
	}
	if _, err = tx.Exec("UPDATE messages SET state=?,reason='' WHERE id=?", state, id); err != nil {
		return err
	}
	return tx.Commit()
}
func (s *Store) Reprocess() (int, error) {
	s.mu.Lock()
	err := s.recoverQueuedText(false)
	s.mu.Unlock()
	if err != nil {
		return 0, err
	}

	// Keyset batches cover the whole backlog without loading mail bodies into memory.
	var last int64
	count := 0
	for {
		rows, err := s.DB.Query("SELECT id FROM messages WHERE state='queued' AND id>? ORDER BY id LIMIT 100", last)
		if err != nil {
			return count, err
		}
		ids := []int64{}
		for rows.Next() {
			var id int64
			if err = rows.Scan(&id); err != nil {
				rows.Close()
				return count, err
			}
			ids = append(ids, id)
		}
		err = rows.Err()
		rows.Close()
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

type Filter struct {
	Search, Account, Status, From, To string
	Offset                            int
}

func (s *Store) Transactions(f Filter) ([]Transaction, error) {
	query := `SELECT id,message_id,merchant,account,amount,currency,direction,date,reference,status,issuer,account_kind FROM transactions WHERE 1=1`
	args := []any{}
	if f.Search != "" {
		query += " AND (merchant LIKE ? OR reference LIKE ? OR issuer LIKE ?)"
		v := "%" + f.Search + "%"
		args = append(args, v, v, v)
	}
	if f.Account != "" {
		parts, err := accountParts(f.Account)
		if err == nil {
			query += " AND lower(trim(issuer))=? AND account_kind=? AND account=?"
			args = append(args, parts[0], parts[1], parts[2])
		} else {
			query += " AND issuer || ' · ' || account=?"
			args = append(args, f.Account)
		}
	}
	if f.Status != "" {
		query += " AND status=?"
		args = append(args, f.Status)
	}
	if f.From != "" {
		query += " AND substr(date,1,10)>=?"
		args = append(args, f.From)
	}
	if f.To != "" {
		query += " AND substr(date,1,10)<=?"
		args = append(args, f.To)
	}
	query += " ORDER BY date DESC,id DESC LIMIT 100 OFFSET ?"
	args = append(args, max(f.Offset, 0))
	rows, err := s.DB.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Transaction{}
	for rows.Next() {
		var t Transaction
		if err = rows.Scan(&t.ID, &t.MessageID, &t.Merchant, &t.Account, &t.Amount, &t.Currency, &t.Direction, &t.Date, &t.Reference, &t.Status, &t.Issuer, &t.AccountKind); err != nil {
			return nil, err
		}
		t.AccountID = accountKey(t.Issuer, t.AccountKind, t.Account)
		out = append(out, t)
	}
	return out, rows.Err()
}
func (s *Store) Setting(key string) (string, error) {
	var v string
	err := s.DB.QueryRow("SELECT value FROM settings WHERE key=?", key).Scan(&v)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	return v, err
}
func (s *Store) SetSetting(key, value string) error {
	_, err := s.DB.Exec("INSERT INTO settings(key,value) VALUES(?,?) ON CONFLICT(key) DO UPDATE SET value=excluded.value", key, value)
	return err
}
