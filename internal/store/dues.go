package store

import (
	"encoding/json"
	"errors"
	"sort"
	"time"

	"github.com/audemed44/ledger/internal/ledger"
	"github.com/audemed44/ledger/internal/statements"
)

// Due is what a credit card's latest statement asks you to pay, and how
// much has come in since.
type Due struct {
	AccountID     string `json:"account_id"`
	Issuer        string `json:"issuer"`
	LastFour      string `json:"last_four"`
	StatementDate string `json:"statement_date"`
	DueDate       string `json:"due_date"`
	// Currency is the statement's; every supported layout is in rupees.
	Currency   string `json:"currency"`
	TotalDue   int64  `json:"total_due"`
	MinimumDue int64  `json:"minimum_due"`
	// Paid is the card's credits after the statement date: payments, and
	// refunds, which lower what's owed just the same.
	Paid      int64 `json:"paid"`
	Remaining int64 `json:"remaining"`
	// Days is how many days are left until the due date; negative once it
	// has passed.
	Days int `json:"days"`
	// Settled is set when you marked it paid: for payments Ledger saw no
	// alert for.
	Settled bool `json:"settled"`
	// Status is "paid", "due" or "overdue".
	Status string `json:"status"`
}

// dueHorizon is how long after its due date a statement stays listed.
const dueHorizon = 45

// Dues lists each credit card's latest statement as of today's date,
// soonest due date first. Statements whose due date passed more than
// dueHorizon days ago are left out, as are cards whose statements have no
// due date.
func (s *Store) Dues(today time.Time) ([]Due, error) {
	rows, err := s.DB.Query(`SELECT snapshot FROM statements s
WHERE date=(SELECT max(date) FROM statements WHERE account_key=s.account_key) ORDER BY account_key`)
	if err != nil {
		return nil, err
	}
	var snapshots []statements.Statement
	for rows.Next() {
		var raw string
		var st statements.Statement
		if err = rows.Scan(&raw); err != nil {
			rows.Close()
			return nil, err
		}
		if json.Unmarshal([]byte(raw), &st) == nil && st.AccountKind == "card" && st.DueDate != "" {
			snapshots = append(snapshots, st)
		}
	}
	rows.Close()
	if err = rows.Err(); err != nil {
		return nil, err
	}
	day := time.Date(today.Year(), today.Month(), today.Day(), 0, 0, 0, 0, time.UTC)
	out := []Due{}
	for _, st := range snapshots {
		due, err := time.Parse("2006-01-02", st.DueDate)
		if err != nil {
			continue
		}
		d := Due{
			AccountID: ledger.AccountKey(st.Issuer, "card", st.Account), Issuer: st.Issuer, LastFour: st.Account,
			StatementDate: st.Date, DueDate: st.DueDate, Currency: "INR",
			TotalDue: max(st.TotalDue, 0), MinimumDue: max(st.MinimumDue, 0),
			Days: int(due.Sub(day).Hours() / 24),
		}
		if d.Days < -dueHorizon {
			continue
		}
		err = s.DB.QueryRow(`SELECT coalesce(sum(amount),0) FROM transactions
WHERE lower(trim(issuer))=lower(trim(?)) AND account_kind='card' AND account=? AND direction='credit'
  AND status!='dismissed' AND substr(date,1,10)>?`, st.Issuer, st.Account, st.Date).Scan(&d.Paid)
		if err != nil {
			return nil, err
		}
		err = s.DB.QueryRow("SELECT count(*)>0 FROM dues_settled WHERE account_key=? AND due_date=?",
			d.AccountID, d.DueDate).Scan(&d.Settled)
		if err != nil {
			return nil, err
		}
		d.Remaining = max(d.TotalDue-d.Paid, 0)
		if d.Settled {
			d.Remaining = 0
		}
		switch {
		case d.Remaining == 0:
			d.Status = "paid"
		case d.Days < 0:
			d.Status = "overdue"
		default:
			d.Status = "due"
		}
		out = append(out, d)
	}
	// Unpaid first, then by due date.
	sort.SliceStable(out, func(i, j int) bool {
		if (out[i].Status == "paid") != (out[j].Status == "paid") {
			return out[j].Status == "paid"
		}
		return out[i].DueDate < out[j].DueDate
	})
	return out, nil
}

// ReminderSent reports whether the reminder for a statement's due date at
// `days` before it went out already.
func (s *Store) ReminderSent(accountID, dueDate string, days int) (bool, error) {
	var n int
	err := s.DB.QueryRow("SELECT count(*) FROM reminders WHERE account_key=? AND due_date=? AND days=?",
		accountID, dueDate, days).Scan(&n)
	return n > 0, err
}

// MarkReminderSent records a reminder that went out.
func (s *Store) MarkReminderSent(accountID, dueDate string, days int, at time.Time) error {
	_, err := s.DB.Exec("INSERT OR IGNORE INTO reminders(account_key,due_date,days,sent) VALUES(?,?,?,?)",
		accountID, dueDate, days, at.UTC().Format(time.RFC3339))
	return err
}

// SettleDue marks a statement's due as paid, or unmarks it.
func (s *Store) SettleDue(accountID, dueDate string, settled bool) error {
	if _, err := ledger.AccountParts(accountID); err != nil {
		return err
	}
	if _, err := time.Parse("2006-01-02", dueDate); err != nil {
		return errors.New("invalid due date")
	}
	var err error
	if settled {
		_, err = s.DB.Exec("INSERT OR IGNORE INTO dues_settled(account_key,due_date) VALUES(?,?)", accountID, dueDate)
	} else {
		_, err = s.DB.Exec("DELETE FROM dues_settled WHERE account_key=? AND due_date=?", accountID, dueDate)
	}
	return err
}
