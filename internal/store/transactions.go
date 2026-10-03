package store

import (
	"database/sql"

	"github.com/audemed44/ledger/internal/ledger"
)

// Filter narrows the transaction list. Account is an ledger.AccountKey; From
// and To are inclusive YYYY-MM-DD dates.
type Filter struct {
	Search, Account, Status, From, To string
	Offset                            int
}

// PageSize is how many transactions one page holds.
const PageSize = 100

// Transactions returns one page of matching transactions, newest first.
func (s *Store) Transactions(f Filter) ([]ledger.Transaction, error) {
	query := `SELECT id,message_id,merchant,account,amount,currency,direction,date,reference,status,issuer,account_kind,
  (statement_id IS NOT NULL AND source_part<0) OR alert_message_id IS NOT NULL, placeholder
FROM transactions WHERE 1=1`
	args := []any{}
	if f.Search != "" {
		query += " AND (merchant LIKE ? OR reference LIKE ? OR issuer LIKE ?)"
		v := "%" + f.Search + "%"
		args = append(args, v, v, v)
	}
	if f.Account != "" {
		if parts, err := ledger.AccountParts(f.Account); err == nil {
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
	query += " ORDER BY date DESC,id DESC LIMIT ? OFFSET ?"
	args = append(args, PageSize, max(f.Offset, 0))
	rows, err := s.DB.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []ledger.Transaction{}
	for rows.Next() {
		var t ledger.Transaction
		err = rows.Scan(&t.ID, &t.MessageID, &t.Merchant, &t.Account, &t.Amount, &t.Currency,
			&t.Direction, &t.Date, &t.Reference, &t.Status, &t.Issuer, &t.AccountKind, &t.Matched, &t.Placeholder)
		if err != nil {
			return nil, err
		}
		t.AccountID = ledger.AccountKey(t.Issuer, t.AccountKind, t.Account)
		out = append(out, t)
	}
	return out, rows.Err()
}

// Accounts lists every account that has a transaction.
func (s *Store) Accounts() ([]ledger.Account, error) {
	rows, err := s.DB.Query(`SELECT min(issuer),account_kind,account FROM transactions
GROUP BY lower(trim(issuer)),account_kind,account ORDER BY 1,2,3`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []ledger.Account{}
	for rows.Next() {
		var a ledger.Account
		if err = rows.Scan(&a.Issuer, &a.Kind, &a.LastFour); err != nil {
			return nil, err
		}
		a.ID = ledger.AccountKey(a.Issuer, a.Kind, a.LastFour)
		out = append(out, a)
	}
	return out, rows.Err()
}

// Total is one currency's debits and credits.
type Total struct {
	Currency string `json:"currency"`
	Debit    int64  `json:"debit"`
	Credit   int64  `json:"credit"`
}

// MonthTotals sums transactions dated in month (YYYY-MM), per currency.
// Dismissed ones don't count.
func (s *Store) MonthTotals(month string) ([]Total, error) {
	rows, err := s.DB.Query(`SELECT currency,
  SUM(CASE WHEN direction='debit' THEN amount ELSE 0 END),
  SUM(CASE WHEN direction='credit' THEN amount ELSE 0 END)
FROM transactions WHERE substr(date,1,7)=? AND status!='dismissed' GROUP BY currency ORDER BY currency`, month)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Total{}
	for rows.Next() {
		var t Total
		if err = rows.Scan(&t.Currency, &t.Debit, &t.Credit); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// Counts is how many transactions, queued messages and alert parsers there are.
type Counts struct {
	Transactions int `json:"transactions"`
	Queued       int `json:"queued"`
	Parsers      int `json:"parsers"`
}

// Counts counts them.
func (s *Store) Counts() (Counts, error) {
	var c Counts
	err := s.DB.QueryRow(`SELECT
  (SELECT count(*) FROM transactions),
  (SELECT count(*) FROM messages WHERE state='queued'),
  (SELECT count(*) FROM parsers)`).Scan(&c.Transactions, &c.Queued, &c.Parsers)
	return c, err
}

// Dismiss sets a flagged transaction aside, as not a real charge (a
// pre-authorisation that settled differently, say), or restores it. Totals
// leave dismissed transactions out; a later statement line can still
// confirm one.
func (s *Store) Dismiss(id int64, dismiss bool) error {
	from, to := "flagged", "dismissed"
	if !dismiss {
		from, to = to, from
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	r, err := s.DB.Exec("UPDATE transactions SET status=? WHERE id=? AND status=?", to, id, from)
	if err != nil {
		return err
	}
	n, err := r.RowsAffected()
	if err == nil && n == 0 {
		return sql.ErrNoRows
	}
	return err
}
