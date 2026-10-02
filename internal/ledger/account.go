package ledger

import (
	"encoding/base64"
	"errors"
	"strings"
)

// Identity is independent of parser names/variants. Account types and issuers
// are part of the key, so a bank account and card sharing a suffix stay separate.
func accountKey(issuer, kind, lastFour string) string {
	if kind == "" {
		kind = "unknown"
	}
	return base64.RawURLEncoding.EncodeToString([]byte(strings.ToLower(strings.TrimSpace(issuer)) + "\x00" + kind + "\x00" + lastFour))
}
func accountParts(key string) ([]string, error) {
	raw, err := base64.RawURLEncoding.DecodeString(key)
	if err != nil {
		return nil, errors.New("invalid account filter")
	}
	parts := strings.Split(string(raw), "\x00")
	if len(parts) != 3 {
		return nil, errors.New("invalid account filter")
	}
	return parts, nil
}

type Account struct {
	ID       string `json:"id"`
	Issuer   string `json:"issuer"`
	Kind     string `json:"kind"`
	LastFour string `json:"last_four"`
}

func (s *Store) accounts() ([]Account, error) {
	rows, err := s.DB.Query("SELECT min(issuer),account_kind,account FROM transactions GROUP BY lower(trim(issuer)),account_kind,account ORDER BY 1,2,3")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Account{}
	for rows.Next() {
		var a Account
		if err = rows.Scan(&a.Issuer, &a.Kind, &a.LastFour); err != nil {
			return nil, err
		}
		a.ID = accountKey(a.Issuer, a.Kind, a.LastFour)
		out = append(out, a)
	}
	return out, rows.Err()
}
func (s *Store) migrateAccountKinds() error {
	rows, err := s.DB.Query("PRAGMA table_info(transactions)")
	if err != nil {
		return err
	}
	exists := false
	for rows.Next() {
		var cid, notnull, pk int
		var name, kind string
		var value any
		if err = rows.Scan(&cid, &name, &kind, &notnull, &value, &pk); err != nil {
			rows.Close()
			return err
		}
		if name == "account_kind" {
			exists = true
		}
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	if exists {
		return nil
	}
	// Existing records are explicitly unknown; a migration must not guess card/bank.
	_, err = s.DB.Exec("ALTER TABLE transactions ADD COLUMN account_kind TEXT NOT NULL DEFAULT 'unknown'")
	return err
}

// Cards represent debt; bank accounts represent funds. Do not combine their
// opposite debit/credit signs when checking a statement's opening and closing.
func balanceDifference(kind string, opening, debits, credits, charges, closing int64) (int64, error) {
	switch kind {
	case "card":
		return opening + debits + charges - credits - closing, nil
	case "bank":
		return opening + credits - debits - charges - closing, nil
	default:
		return 0, errors.New("account type is missing or unknown")
	}
}
