package store

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/audemed44/ledger/internal/alerts"
	"github.com/audemed44/ledger/internal/pattern"
	"github.com/audemed44/ledger/internal/statements"
)

// ErrParserNameTaken is returned when a different PDF parser has the name.
var ErrParserNameTaken = errors.New("A PDF parser with this name already exists; select or edit it, or choose a different name")

// Parsers returns the alert parsers, oldest first.
func (s *Store) Parsers() ([]alerts.Parser, error) {
	rows, err := s.DB.Query("SELECT id,definition FROM parsers ORDER BY id")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []alerts.Parser{}
	for rows.Next() {
		var id int64
		var raw string
		if err = rows.Scan(&id, &raw); err != nil {
			return nil, err
		}
		var p alerts.Parser
		if err = json.Unmarshal([]byte(raw), &p); err != nil {
			return nil, err
		}
		p.ID = id
		out = append(out, p)
	}
	return out, rows.Err()
}

// SaveParser creates (ID 0) or replaces an alert parser. Transactions it
// already imported don't change.
func (s *Store) SaveParser(p alerts.Parser) (alerts.Parser, error) {
	if err := p.Validate(); err != nil {
		return p, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	raw, err := json.Marshal(p)
	if err != nil {
		return p, err
	}
	p.ID, err = s.saveDefinition("parsers", p.ID, raw)
	return p, err
}

// DeleteParser deletes an alert parser, keeping its transactions and mail.
func (s *Store) DeleteParser(id int64) error { return s.deleteDefinition("parsers", id) }

// StatementParsers returns the PDF statement presets, oldest first.
func (s *Store) StatementParsers() ([]statements.Parser, error) {
	rows, err := s.DB.Query("SELECT id,definition FROM statement_parsers ORDER BY id")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []statements.Parser{}
	for rows.Next() {
		var id int64
		var raw string
		if err = rows.Scan(&id, &raw); err != nil {
			return nil, err
		}
		var p statements.Parser
		if err = json.Unmarshal([]byte(raw), &p); err != nil {
			return nil, err
		}
		p.ID = id
		if p.Triggers == nil {
			p.Triggers = []statements.Trigger{}
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// SaveStatementParser creates or replaces a PDF preset. Creating one that's
// identical to a saved preset returns that preset; a name used by a
// different configuration is ErrParserNameTaken.
func (s *Store) SaveStatementParser(p statements.Parser) (statements.Parser, error) {
	if err := p.Validate(); err != nil {
		return p, err
	}
	p.Name = strings.TrimSpace(p.Name)
	s.mu.Lock()
	defer s.mu.Unlock()
	existing, err := s.StatementParsers()
	if err != nil {
		return p, err
	}
	for _, saved := range existing {
		if saved.ID == p.ID || !strings.EqualFold(saved.Name, p.Name) {
			continue
		}
		same := saved.Adapter == p.Adapter && saved.PasswordSlot == p.PasswordSlot &&
			saved.BalanceTolerancePaise == p.BalanceTolerancePaise
		if p.ID == 0 && same {
			return saved, nil
		}
		return p, ErrParserNameTaken
	}
	raw, err := json.Marshal(p)
	if err != nil {
		return p, err
	}
	p.ID, err = s.saveDefinition("statement_parsers", p.ID, raw)
	return p, err
}

// DeleteStatementParser deletes a PDF preset, keeping statements,
// transactions and mail.
func (s *Store) DeleteStatementParser(id int64) error {
	return s.deleteDefinition("statement_parsers", id)
}

// saveDefinition inserts (id 0) or updates a JSON definition row. table is
// always a constant.
func (s *Store) saveDefinition(table string, id int64, raw []byte) (int64, error) {
	if id == 0 {
		r, err := s.DB.Exec("INSERT INTO "+table+"(definition) VALUES(?)", string(raw))
		if err != nil {
			return 0, err
		}
		return r.LastInsertId()
	}
	r, err := s.DB.Exec("UPDATE "+table+" SET definition=? WHERE id=?", string(raw), id)
	if err != nil {
		return id, err
	}
	n, err := r.RowsAffected()
	if err != nil {
		return id, err
	}
	if n == 0 {
		return id, sql.ErrNoRows
	}
	return id, nil
}

// deleteDefinition returns sql.ErrNoRows when there's no such row.
func (s *Store) deleteDefinition(table string, id int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	r, err := s.DB.Exec("DELETE FROM "+table+" WHERE id=?", id)
	if err != nil {
		return err
	}
	n, err := r.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return sql.ErrNoRows
	}
	return nil
}

// AddStatementTrigger makes a preset import statements like the PDF in MIME
// part `part` of a message by itself. It's refused when another preset
// would import that PDF too. Adding a trigger it already has changes nothing.
func (s *Store) AddStatementTrigger(parserID, messageID int64, part int) (statements.Trigger, error) {
	m, err := s.ReviewMessage(messageID)
	if err != nil {
		return statements.Trigger{}, err
	}
	name := ""
	for _, a := range m.Attachments {
		if a.Part == part {
			name = a.Name
		}
	}
	t := statements.TriggerFor(m.Sender, m.Subject, name)
	if err = t.Validate(); err != nil {
		return t, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	parsers, err := s.StatementParsers()
	if err != nil {
		return t, err
	}
	var target *statements.Parser
	for i, p := range parsers {
		if p.ID == parserID {
			target = &parsers[i]
		} else if p.Matches(m.Sender, m.Subject, name) {
			return t, fmt.Errorf("%s already imports statements like this one automatically", p.Name)
		}
	}
	if target == nil {
		return t, sql.ErrNoRows
	}
	if target.Matches(m.Sender, m.Subject, name) {
		return t, nil
	}
	target.Triggers = append(target.Triggers, t)
	if err = target.Validate(); err != nil {
		return t, err
	}
	raw, err := json.Marshal(target)
	if err != nil {
		return t, err
	}
	_, err = s.saveDefinition("statement_parsers", target.ID, raw)
	return t, err
}

// IgnoreLike saves a rule that ignores emails like this one: the same
// sender, and a subject like its subject (numbers and month names may
// change), whatever the body says. Ignored mail stays archived.
func (s *Store) IgnoreLike(messageID int64) (alerts.Parser, error) {
	m, err := s.Message(messageID)
	if err != nil {
		return alerts.Parser{}, err
	}
	if m.Sender == "" {
		return alerts.Parser{}, errors.New("This email has no readable sender to match")
	}
	subject := pattern.Whole(m.Subject)
	if subject == "" {
		subject = "^$"
	}
	name := strings.TrimSpace(m.Subject)
	if r := []rune(name); len(r) > 60 {
		name = string(r[:60]) + "…"
	}
	return s.SaveParser(alerts.Parser{
		Name:        "Ignore: " + name,
		AccountKind: "unknown",
		Sender:      strings.ToLower(m.Sender),
		Subject:     subject,
		Timezone:    "Asia/Kolkata",
		Currency:    "INR",
		Direction:   "ignore",
		Enabled:     true,
	})
}
