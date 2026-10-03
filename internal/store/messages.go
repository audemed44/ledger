package store

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/audemed44/ledger/internal/mail"
)

// Message is an archived email and where it is in the pipeline: queued (in
// the inbox), parsed, ignored, statement (imported) or excluded.
type Message struct {
	ID           int64             `json:"id"`
	Key          string            `json:"-"`
	Sender       string            `json:"sender"`
	Subject      string            `json:"subject"`
	Date         string            `json:"date"`
	Body         string            `json:"body,omitempty"`
	State        string            `json:"state"`
	Reason       string            `json:"reason"`
	Archive      string            `json:"-"`
	BodyFormat   string            `json:"body_format,omitempty"`
	Attachments  []mail.Attachment `json:"attachments,omitempty"`
	HasPDF       bool              `json:"has_pdf,omitempty"`
	CanParse     bool              `json:"can_parse"`
	ContentError string            `json:"content_error,omitempty"`
}

// Archive file names come from a hash, never a sender-controlled path or
// Message-ID.
var archiveName = regexp.MustCompile(`^[a-f0-9]{64}\.eml$`)

// Ingest archives a raw email and processes it. The archive is written and
// synced before the database is touched, so an interrupted ingest is retried
// safely. Ingesting the same email again (after a crash or a UIDVALIDITY
// change) only reprocesses it.
func (s *Store) Ingest(raw []byte) (int64, error) {
	if len(raw) > mail.MaxSize {
		return 0, errors.New("message exceeds 25 MiB")
	}
	hash := sha256.Sum256(raw)
	sum := hex.EncodeToString(hash[:])
	decoded := mail.Decode(raw)
	m := Message{
		Key:     decoded.Key,
		Sender:  decoded.Sender,
		Subject: decoded.Subject,
		Date:    decoded.Date,
		Body:    decoded.Body,
		Reason:  decoded.Reason,
		Archive: sum + ".eml",
		HasPDF:  decoded.HasPDF,
	}
	if m.Key == "" {
		m.Key = "sha256:" + sum
	}
	if m.Date == "" {
		m.Date = time.Now().UTC().Format(time.RFC3339)
	}

	var id int64
	err := s.DB.QueryRow("SELECT id FROM messages WHERE message_key=?", m.Key).Scan(&id)
	if err == nil {
		return id, s.process(id)
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return 0, err
	}
	if err = s.writeArchive(m.Archive, raw); err != nil {
		return 0, err
	}
	_, err = s.DB.Exec(`INSERT OR IGNORE INTO messages(message_key,sender,subject,date,body,reason,archive,has_pdf)
VALUES(?,?,?,?,?,?,?,?)`, m.Key, m.Sender, m.Subject, m.Date, m.Body, m.Reason, m.Archive, m.HasPDF)
	if err != nil {
		return 0, err
	}
	if err = s.DB.QueryRow("SELECT id FROM messages WHERE message_key=?", m.Key).Scan(&id); err != nil {
		return 0, err
	}
	return id, s.process(id)
}

// process runs the alert parsers, then automatic statement import.
func (s *Store) process(id int64) error {
	if err := s.Process(id); err != nil {
		return err
	}
	return s.AutoImport(context.Background(), id)
}

// writeArchive writes the file atomically: a synced temporary file renamed
// into place, then the directory synced.
func (s *Store) writeArchive(name string, raw []byte) error {
	path := filepath.Join(s.Dir, "archive", name)
	temp, err := os.CreateTemp(filepath.Dir(path), ".mail-*")
	if err != nil {
		return err
	}
	defer os.Remove(temp.Name())
	if _, err = temp.Write(raw); err != nil {
		temp.Close()
		return err
	}
	if err = temp.Sync(); err != nil {
		temp.Close()
		return err
	}
	if err = temp.Close(); err != nil {
		return err
	}
	if err = os.Rename(temp.Name(), path); err != nil {
		return err
	}
	dir, err := os.Open(filepath.Dir(path))
	if err != nil {
		return err
	}
	err = dir.Sync()
	dir.Close()
	return err
}

// Messages is the inbox: up to 200 queued messages, newest email first.
func (s *Store) Messages() ([]Message, error) { return s.FilteredMessages("") }

// FilteredMessages is the inbox filtered to "pdf" (has a PDF), "text" (no
// PDF) or "all". The filter applies before the 200 limit.
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

// Message returns one message as stored, without re-reading its archive.
func (s *Store) Message(id int64) (Message, error) {
	var m Message
	err := s.DB.QueryRow("SELECT id,sender,subject,date,body,state,reason,has_pdf FROM messages WHERE id=?", id).
		Scan(&m.ID, &m.Sender, &m.Subject, &m.Date, &m.Body, &m.State, &m.Reason, &m.HasPDF)
	return m, err
}

// ReviewMessage returns a message with its text and attachments decoded
// afresh from the archive. An unreadable archive is reported on the message,
// not as an error.
func (s *Store) ReviewMessage(id int64) (Message, error) {
	m, err := s.Message(id)
	if err != nil {
		return m, err
	}
	raw, err := s.ArchivedRaw(id)
	if err != nil {
		m.ContentError = "The archived email could not be read. Check the archive volume or restore it from backup."
		return m, nil
	}
	decoded := mail.Decode(raw)
	m.Body, m.BodyFormat = decoded.Body, decoded.BodyFormat
	m.Attachments, m.HasPDF, m.CanParse = decoded.Attachments, decoded.HasPDF, decoded.CanParse
	if strings.HasPrefix(decoded.Reason, "MIME:") {
		m.ContentError = decoded.Reason
	}
	return m, nil
}

// ArchivedRaw returns the original email.
func (s *Store) ArchivedRaw(id int64) ([]byte, error) {
	var name string
	if err := s.DB.QueryRow("SELECT archive FROM messages WHERE id=?", id).Scan(&name); err != nil {
		return nil, err
	}
	if !archiveName.MatchString(name) {
		return nil, errors.New("invalid archive reference")
	}
	f, err := os.Open(filepath.Join(s.Dir, "archive", name))
	if err != nil {
		return nil, err
	}
	defer f.Close()
	raw, err := io.ReadAll(io.LimitReader(f, mail.MaxSize+1))
	if err != nil {
		return nil, err
	}
	if len(raw) > mail.MaxSize {
		return nil, errors.New("archive too large")
	}
	return raw, nil
}

// recoverQueuedText re-decodes queued messages from their archives: all of
// them, or only those with no text or a MIME error.
func (s *Store) recoverQueuedText(all bool) error {
	query := "SELECT id FROM messages WHERE state='queued' AND id>?"
	if !all {
		query += " AND (body='' OR reason LIKE 'MIME:%')"
	}
	query += " ORDER BY id LIMIT 100"
	var last int64
	for {
		ids, err := s.queuedIDs(query, last)
		if err != nil {
			return err
		}
		if len(ids) == 0 {
			return nil
		}
		for _, id := range ids {
			last = id
			raw, e := s.ArchivedRaw(id)
			if e != nil {
				_, err = s.DB.Exec("UPDATE messages SET reason=? WHERE id=? AND state='queued'",
					"MIME: archive could not be read; restore it and retry", id)
			} else {
				decoded := mail.Decode(raw)
				reason := decoded.Reason
				if reason == "" {
					reason = noParserReason
				}
				_, err = s.DB.Exec("UPDATE messages SET body=?,reason=?,has_pdf=? WHERE id=? AND state='queued'",
					decoded.Body, reason, decoded.HasPDF, id)
			}
			if err != nil {
				return fmt.Errorf("recover archived text: %w", err)
			}
		}
	}
}
