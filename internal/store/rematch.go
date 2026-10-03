package store

import (
	"github.com/audemed44/ledger/internal/alerts"
)

// Handled is what a parser has done so far: the emails it recorded or
// ignored, and how many of those a statement has confirmed.
type Handled struct {
	IDs       []int64 `json:"-"`
	Emails    int     `json:"emails"`
	Confirmed int     `json:"confirmed"`
}

// HandledBy finds the emails parser p recorded or ignored: those in the
// ledger (parsed or ignored) that it matches. An email matches exactly one
// parser, so they're its own.
func (s *Store) HandledBy(p alerts.Parser) (Handled, error) {
	out := Handled{IDs: []int64{}}
	rows, err := s.DB.Query(`SELECT id,sender,subject,body FROM messages
WHERE state IN ('parsed','ignored') AND lower(trim(sender))=lower(trim(?)) ORDER BY id`, p.Sender)
	if err != nil {
		return out, err
	}
	type email struct {
		id                    int64
		sender, subject, body string
	}
	emails := []email{}
	for rows.Next() {
		var e email
		if err = rows.Scan(&e.id, &e.sender, &e.subject, &e.body); err != nil {
			rows.Close()
			return out, err
		}
		emails = append(emails, e)
	}
	rows.Close()
	if err = rows.Err(); err != nil {
		return out, err
	}
	for _, e := range emails {
		// A matched email that fails to parse (now) still counts.
		if r, _ := p.Parse(e.sender, e.subject, e.body); !r.Matched {
			continue
		}
		out.IDs = append(out.IDs, e.id)
		var confirmed int
		err = s.DB.QueryRow(`SELECT count(*) FROM transactions
WHERE message_id=? AND source_part<0 AND statement_id IS NOT NULL`, e.id).Scan(&confirmed)
		if err != nil {
			return out, err
		}
		out.Confirmed += confirmed
	}
	out.Emails = len(out.IDs)
	return out, nil
}

// Reread undoes what emails recorded and processes them again with the
// parsers as they are now: to fix a parser's mistake (a credit read as a
// debit, say) in what it already imported. Alert transactions a statement
// has confirmed are kept, since the statement is the record. An email that
// no parser matches any more goes back to the inbox. It returns how many
// emails it re-read and how many it kept.
func (s *Store) Reread(ids []int64) (reread, kept int, err error) {
	for _, id := range ids {
		undone, err := s.undo(id)
		if err != nil {
			return reread, kept, err
		}
		if !undone {
			kept++
			continue
		}
		if err = s.process(id); err != nil {
			return reread, kept, err
		}
		reread++
	}
	return reread, kept, nil
}

// undo removes what one parsed or ignored email recorded and queues it
// again, unless a statement confirmed its transaction.
func (s *Store) undo(id int64) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	tx, err := s.DB.Begin()
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	var state string
	if err = tx.QueryRow("SELECT state FROM messages WHERE id=?", id).Scan(&state); err != nil {
		return false, err
	}
	if state != "parsed" && state != "ignored" {
		return false, nil
	}
	var confirmed int
	err = tx.QueryRow("SELECT count(*) FROM transactions WHERE message_id=? AND source_part<0 AND statement_id IS NOT NULL", id).Scan(&confirmed)
	if err != nil || confirmed > 0 {
		return false, err
	}
	for _, q := range []string{
		"DELETE FROM transactions WHERE message_id=? AND source_part<0",
		// An alert that arrived after its statement only marked the line.
		"UPDATE transactions SET alert_message_id=NULL WHERE alert_message_id=?",
		"UPDATE messages SET state='queued',reason='' WHERE id=?",
	} {
		if _, err = tx.Exec(q, id); err != nil {
			return false, err
		}
	}
	return true, tx.Commit()
}

// parserByID finds a saved alert parser.
func (s *Store) parserByID(id int64) (alerts.Parser, bool, error) {
	parsers, err := s.Parsers()
	if err != nil {
		return alerts.Parser{}, false, err
	}
	for _, p := range parsers {
		if p.ID == id {
			return p, true, nil
		}
	}
	return alerts.Parser{}, false, nil
}

// HandledByID is HandledBy for a saved parser.
func (s *Store) HandledByID(id int64) (Handled, bool, error) {
	p, ok, err := s.parserByID(id)
	if err != nil || !ok {
		return Handled{}, ok, err
	}
	h, err := s.HandledBy(p)
	return h, true, err
}
