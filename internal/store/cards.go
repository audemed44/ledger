package store

import (
	"database/sql"
	"errors"
	"strings"

	"github.com/audemed44/ledger/internal/ledger"
)

// CardLink ties a debit card to the bank account it draws on, so the
// card's alerts land on that account and reconcile with its statements.
type CardLink struct {
	Issuer  string `json:"issuer"`
	Card    string `json:"card"`    // the card's last four digits
	Account string `json:"account"` // the account's last four digits
}

// CardLinks lists the debit card links.
func (s *Store) CardLinks() ([]CardLink, error) {
	rows, err := s.DB.Query("SELECT issuer,card,account FROM card_links ORDER BY issuer,card")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []CardLink{}
	for rows.Next() {
		var l CardLink
		if err = rows.Scan(&l.Issuer, &l.Card, &l.Account); err != nil {
			return nil, err
		}
		out = append(out, l)
	}
	return out, rows.Err()
}

// SaveCardLink links a card, replacing its earlier link. Transactions
// already recorded keep their account.
func (s *Store) SaveCardLink(l CardLink) error {
	l.Issuer = strings.TrimSpace(l.Issuer)
	if l.Issuer == "" || len(l.Issuer) > 100 || !ledger.LastFour.MatchString(l.Card) || !ledger.LastFour.MatchString(l.Account) {
		return errors.New("Enter the issuer and the last four digits of the card and the account")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	_, err := s.DB.Exec(`INSERT INTO card_links(issuer,card,account) VALUES(?,?,?)
ON CONFLICT(issuer,card) DO UPDATE SET account=excluded.account`, strings.ToLower(l.Issuer), l.Card, l.Account)
	return err
}

// DeleteCardLink removes a link; sql.ErrNoRows when there's none.
func (s *Store) DeleteCardLink(issuer, card string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	r, err := s.DB.Exec("DELETE FROM card_links WHERE issuer=? AND card=?", strings.ToLower(strings.TrimSpace(issuer)), card)
	if err != nil {
		return err
	}
	if n, _ := r.RowsAffected(); n == 0 {
		return sql.ErrNoRows
	}
	return nil
}

// linkedAccount is the bank account a debit card is linked to.
func (s *Store) linkedAccount(issuer, card string) (string, error) {
	var account string
	err := s.DB.QueryRow("SELECT account FROM card_links WHERE issuer=? AND card=?",
		strings.ToLower(strings.TrimSpace(issuer)), card).Scan(&account)
	return account, err
}
