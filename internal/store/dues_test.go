package store

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/audemed44/ledger/internal/fixture"
	"github.com/audemed44/ledger/internal/ledger"
	"github.com/audemed44/ledger/internal/statements"
)

// addStatement records a card statement's summary as an import would.
func addStatement(t *testing.T, s *Store, issuer, kind, card, date, due string, total int64) {
	t.Helper()
	st := statements.Statement{AccountID: ledger.AccountKey(issuer, kind, card), AccountKind: kind, Issuer: issuer,
		Account: card, Date: date, DueDate: due, TotalDue: total, MinimumDue: total / 20}
	raw, _ := json.Marshal(st)
	if _, err := s.DB.Exec("INSERT INTO statements(account_key,date,fingerprint,snapshot) VALUES(?,?,?,?)",
		st.AccountID, date, statements.Fingerprint(st), string(raw)); err != nil {
		t.Fatal(err)
	}
}

func TestDuesFollowTheLatestStatementAndPayments(t *testing.T) {
	s := testStore(t)
	today := time.Date(2026, 10, 3, 10, 0, 0, 0, time.Local)
	addStatement(t, s, "Example Bank", "card", "4242", "2026-08-20", "2026-09-08", 500000)
	addStatement(t, s, "Example Bank", "card", "4242", "2026-09-20", "2026-10-08", 1000000)
	addStatement(t, s, "Other Bank", "card", "1111", "2026-09-10", "2026-10-01", 200000)
	addStatement(t, s, "Old Bank", "card", "2222", "2026-06-01", "2026-06-20", 300000)
	addStatement(t, s, "Example Bank", "bank", "9999", "2026-09-30", "", 800000)
	// A payment after the statement counts; one on or before it doesn't.
	p := fixture.AlertParser()
	p.Issuer, p.AccountKind, p.Direction = "Example Bank", "card", "credit"
	if _, err := s.SaveParser(p); err != nil {
		t.Fatal(err)
	}
	for id, date := range map[string]string{"before": "2026-09-20", "after": "2026-09-25"} {
		s.Ingest(fixture.Mail(id, strings.Replace(fixture.AlertBody, "2026-10-01", date, 1)))
	}

	dues, err := s.Dues(today)
	if err != nil || len(dues) != 2 {
		t.Fatalf("%+v %v", dues, err)
	}
	other, example := dues[0], dues[1]
	if other.Issuer != "Other Bank" || other.Status != "overdue" || other.Days != -2 || other.Remaining != 200000 {
		t.Fatalf("%+v", other)
	}
	if example.DueDate != "2026-10-08" || example.Days != 5 || example.Paid != 123456 || example.Remaining != 1000000-123456 || example.Status != "due" {
		t.Fatalf("%+v", example)
	}

	if err = s.SettleDue(other.AccountID, other.DueDate, true); err != nil {
		t.Fatal(err)
	}
	dues, _ = s.Dues(today)
	if dues[0].Issuer != "Example Bank" || dues[1].Status != "paid" || !dues[1].Settled || dues[1].Remaining != 0 {
		t.Fatalf("settled dues sort last: %+v", dues)
	}
	if s.SettleDue("not-a-key", "2026-10-01", true) == nil || s.SettleDue(other.AccountID, "soon", true) == nil {
		t.Fatal("invalid settle accepted")
	}
}
