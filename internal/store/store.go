// Package store keeps Ledger's state: SQLite at <dir>/ledger.db and every
// email, attachments included, archived intact under <dir>/archive. Keep the
// two together in backups.
package store

import (
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"sync"

	_ "modernc.org/sqlite" // pure Go, so the build stays cgo-free
)

// BackfillStart is the earliest email date Ledger imports.
const BackfillStart = "2026-01-01"

// BeforeBackfillReason is why older mail is excluded from the inbox.
const BeforeBackfillReason = "Before 1 January 2026 backfill start"

// Store is the database plus the archive directory.
type Store struct {
	DB  *sql.DB
	Dir string
	// PDFPasswords are the LEDGER_PDF_PASSWORDS slots, kept in memory only.
	PDFPasswords []string
	// mu serialises writes that read then update, such as processing a
	// message or importing a statement.
	mu sync.Mutex
	// pdf runs one PDF extraction at a time, to bound memory and CPU.
	pdf sync.Mutex
}

// Open opens (creating and upgrading as needed) the store in dir.
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
	s := &Store{DB: db, Dir: dir}
	if err = s.migrate(); err != nil {
		db.Close()
		return nil, err
	}
	return s, nil
}

// Close closes the database.
func (s *Store) Close() error { return s.DB.Close() }

// Setting returns a stored value, or "" when it isn't set.
func (s *Store) Setting(key string) (string, error) {
	var v string
	err := s.DB.QueryRow("SELECT value FROM settings WHERE key=?", key).Scan(&v)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	return v, err
}

// SetSetting stores a value.
func (s *Store) SetSetting(key, value string) error {
	_, err := s.DB.Exec(
		"INSERT INTO settings(key,value) VALUES(?,?) ON CONFLICT(key) DO UPDATE SET value=excluded.value",
		key, value)
	return err
}

// queuedIDs pages through queued message IDs above last, 100 at a time, so a
// backlog never loads every body into memory.
func (s *Store) queuedIDs(query string, last int64) ([]int64, error) {
	rows, err := s.DB.Query(query, last)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	ids := []int64{}
	for rows.Next() {
		var id int64
		if err = rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}
