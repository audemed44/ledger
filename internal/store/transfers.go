package store

import (
	"database/sql"
	"errors"
	"regexp"
	"strings"

	"github.com/audemed44/ledger/internal/pattern"
)

// Transfers move money between your own accounts: a card bill paid from a
// bank account, UPI from one bank to another, a sweep into a deposit. They
// stay in the ledger but out of spending totals. transactions.transfer is
// how one was found:
//
//   - "paired": a debit on one account and a credit of the same amount on
//     another, within TransferWindowDays; transfer_of is the other side.
//   - "rule": a transfer rule matched its description (money moving to an
//     account Ledger doesn't see, such as SBI's auto-sweep into deposits).
//   - "manual": marked by hand.
//
// no_transfer marks a transaction you said isn't one; it's never paired or
// matched by a rule again.

// TransferWindowDays is how far apart the two sides of a transfer can be.
const TransferWindowDays = 3

// TransferRule marks transactions whose description matches Pattern (on
// accounts of Issuer, or any account when it's empty) as transfers.
type TransferRule struct {
	ID      int64  `json:"id"`
	Issuer  string `json:"issuer"`
	Pattern string `json:"pattern"`
	Example string `json:"example"`
}

// findTransfers applies the transfer rules, then pairs transfers between
// accounts. A pair needs each side to have exactly one candidate, so a
// coincidence of amounts is left alone rather than guessed. The caller
// holds s.mu, with no transaction open.
func (s *Store) findTransfers() error {
	rules, err := s.TransferRules()
	if err != nil {
		return err
	}
	if len(rules) > 0 {
		rows, err := s.DB.Query("SELECT id,issuer,merchant FROM transactions WHERE transfer='' AND no_transfer=0")
		if err != nil {
			return err
		}
		marked := []int64{}
		for rows.Next() {
			var id int64
			var issuer, merchant string
			if err = rows.Scan(&id, &issuer, &merchant); err != nil {
				rows.Close()
				return err
			}
			for _, r := range rules {
				if (r.Issuer == "" || strings.EqualFold(strings.TrimSpace(r.Issuer), strings.TrimSpace(issuer))) &&
					regexp.MustCompile(r.Pattern).MatchString(merchant) {
					marked = append(marked, id)
					break
				}
			}
		}
		rows.Close()
		if err = rows.Err(); err != nil {
			return err
		}
		for _, id := range marked {
			if _, err = s.DB.Exec("UPDATE transactions SET transfer='rule' WHERE id=?", id); err != nil {
				return err
			}
		}
	}

	// Candidates include transactions already paired, so the outcome doesn't
	// depend on the order they arrived in: a pair that a later transaction
	// makes ambiguous is undone.
	rows, err := s.DB.Query(`SELECT d.id,c.id FROM transactions d JOIN transactions c
  ON c.amount=d.amount AND c.currency=d.currency AND c.direction='credit'
  AND NOT (lower(trim(c.issuer))=lower(trim(d.issuer)) AND c.account_kind=d.account_kind AND c.account=d.account)
  AND abs(julianday(substr(c.date,1,10))-julianday(substr(d.date,1,10)))<=?
WHERE d.direction='debit' AND d.transfer IN ('','paired') AND c.transfer IN ('','paired')
  AND d.no_transfer=0 AND c.no_transfer=0 AND d.status!='dismissed' AND c.status!='dismissed'`, TransferWindowDays)
	if err != nil {
		return err
	}
	type pair struct{ debit, credit int64 }
	pairs := []pair{}
	debits, credits := map[int64]int{}, map[int64]int{}
	for rows.Next() {
		var p pair
		if err = rows.Scan(&p.debit, &p.credit); err != nil {
			rows.Close()
			return err
		}
		pairs = append(pairs, p)
		debits[p.debit]++
		credits[p.credit]++
	}
	rows.Close()
	if err = rows.Err(); err != nil {
		return err
	}
	want := map[int64]int64{} // each side of a wanted pair → the other side
	for _, p := range pairs {
		if debits[p.debit] == 1 && credits[p.credit] == 1 {
			want[p.debit], want[p.credit] = p.credit, p.debit
		}
	}
	// Undo pairs no longer wanted, then make the new ones.
	rows, err = s.DB.Query("SELECT id,coalesce(transfer_of,0) FROM transactions WHERE transfer='paired'")
	if err != nil {
		return err
	}
	have := map[int64]int64{}
	for rows.Next() {
		var id, other int64
		if err = rows.Scan(&id, &other); err != nil {
			rows.Close()
			return err
		}
		have[id] = other
	}
	rows.Close()
	if err = rows.Err(); err != nil {
		return err
	}
	for id, other := range have {
		if want[id] != other {
			if _, err = s.DB.Exec("UPDATE transactions SET transfer='',transfer_of=NULL WHERE id=?", id); err != nil {
				return err
			}
		}
	}
	for id, other := range want {
		if have[id] != other {
			if _, err = s.DB.Exec("UPDATE transactions SET transfer='paired',transfer_of=? WHERE id=?", other, id); err != nil {
				return err
			}
		}
	}
	return nil
}

// FindTransfers runs findTransfers on its own.
func (s *Store) FindTransfers() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.findTransfers()
}

// SetTransfer marks a transaction as a transfer by hand, or says it isn't
// one: then it and its pair (if any) are unmarked, and never paired or
// matched by a rule again.
func (s *Store) SetTransfer(id int64, transfer bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	var partner sql.NullInt64
	if err := s.DB.QueryRow("SELECT transfer_of FROM transactions WHERE id=?", id).Scan(&partner); err != nil {
		return err
	}
	if transfer {
		_, err := s.DB.Exec("UPDATE transactions SET transfer='manual',no_transfer=0 WHERE id=? AND transfer=''", id)
		return err
	}
	_, err := s.DB.Exec(`UPDATE transactions SET transfer='',transfer_of=NULL,no_transfer=1
WHERE id=? OR (id=? AND transfer_of=?)`, id, partner.Int64, id)
	return err
}

// TransferRules lists the rules, oldest first.
func (s *Store) TransferRules() ([]TransferRule, error) {
	rows, err := s.DB.Query("SELECT id,issuer,pattern,example FROM transfer_rules ORDER BY id")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []TransferRule{}
	for rows.Next() {
		var r TransferRule
		if err = rows.Scan(&r.ID, &r.Issuer, &r.Pattern, &r.Example); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// TransferRuleLike saves a rule treating every transaction described like
// transaction id (numbers and month names may differ), on the same
// issuer's accounts, as a transfer, and applies it.
func (s *Store) TransferRuleLike(id int64) (TransferRule, error) {
	var r TransferRule
	if err := s.DB.QueryRow("SELECT issuer,merchant FROM transactions WHERE id=?", id).Scan(&r.Issuer, &r.Example); err != nil {
		return r, err
	}
	r.Pattern = pattern.Whole(r.Example)
	if r.Pattern == "" {
		return r, errors.New("This transaction has no description to match")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	res, err := s.DB.Exec("INSERT INTO transfer_rules(issuer,pattern,example) VALUES(?,?,?)", r.Issuer, r.Pattern, r.Example)
	if err != nil {
		return r, err
	}
	if r.ID, err = res.LastInsertId(); err != nil {
		return r, err
	}
	return r, s.findTransfers()
}

// DeleteTransferRule removes a rule and unmarks what rules marked; the
// remaining rules then apply again.
func (s *Store) DeleteTransferRule(id int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	res, err := s.DB.Exec("DELETE FROM transfer_rules WHERE id=?", id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return sql.ErrNoRows
	}
	if _, err = s.DB.Exec("UPDATE transactions SET transfer='' WHERE transfer='rule'"); err != nil {
		return err
	}
	return s.findTransfers()
}
