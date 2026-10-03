package store

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/audemed44/ledger/internal/alerts"
	"github.com/audemed44/ledger/internal/ledger"
	"github.com/audemed44/ledger/internal/statements"
)

// SeedDemo fills the store with synthetic alerts, a parser for them, card
// statements with payments due and one unmatched email, for `ledger --demo`.
func (s *Store) SeedDemo() error {
	parsers, err := s.Parsers()
	if err != nil {
		return err
	}
	if len(parsers) == 0 {
		_, err = s.SaveParser(alerts.Parser{
			Name:       "Example Bank",
			Sender:     "alerts@example.invalid",
			Subject:    "^Purchase alert$",
			Pattern:    `(?s)Amount: (?P<currency>[A-Z]{3}) (?P<amount>[\d,.]+)\nMerchant: (?P<merchant>[^\n]+)\nCard: (?P<account>\d{4})\nDate: (?P<date>[^\n]+)\nReference: (?P<reference>[^\n]+)`,
			DateLayout: "2006-01-02",
			Timezone:   "Asia/Kolkata",
			Currency:   "INR",
			Direction:  "debit",
			Enabled:    true,
		})
		if err != nil {
			return err
		}
	}
	demo := []struct{ merchant, amount string }{
		{"Paper & Press", "1290.00"},
		{"Neighbourhood Coffee", "280.00"},
		{"Metro Transit", "120.00"},
		{"Sunday Groceries", "2840.50"},
		{"Studio Music", "149.00"},
		{"The Reading Room", "760.00"},
	}
	for i, d := range demo {
		date := time.Now().AddDate(0, 0, -i).Format("2006-01-02")
		raw := fmt.Sprintf("From: alerts@example.invalid\r\nSubject: Purchase alert\r\n"+
			"Message-ID: <demo-%d@ledger.invalid>\r\nContent-Type: text/plain; charset=utf-8\r\n\r\n"+
			"Amount: INR %s\nMerchant: %s\nCard: 4242\nDate: %s\nReference: DEMO-%d\n",
			i, d.amount, d.merchant, date, i)
		if _, err = s.Ingest([]byte(raw)); err != nil {
			return err
		}
	}
	if err = s.seedDemoStatements(); err != nil {
		return err
	}
	_, err = s.Ingest([]byte("From: notices@example.invalid\r\nSubject: A new alert format\r\n" +
		"Message-ID: <demo-unmatched@ledger.invalid>\r\nContent-Type: text/plain\r\n\r\n" +
		"Your card 8080 was debited INR 450.00 at EXAMPLE SHOP on 02-Oct-2026.\n"))
	return err
}

// seedDemoStatements records two card statements' summaries, so the dues
// have something to show.
func (s *Store) seedDemoStatements() error {
	for _, d := range []struct {
		issuer, card   string
		total, minimum int64
		dueIn          int
	}{{"Example Bank", "4242", 1849250, 92500, 3}, {"Sample Card Co", "9090", 412000, 20600, 12}} {
		now := time.Now()
		st := statements.Statement{
			AccountID: ledger.AccountKey(d.issuer, "card", d.card), AccountKind: "card",
			Issuer: d.issuer, Account: d.card, Balanced: true,
			Date:     now.AddDate(0, 0, d.dueIn-20).Format("2006-01-02"),
			DueDate:  now.AddDate(0, 0, d.dueIn).Format("2006-01-02"),
			TotalDue: d.total, MinimumDue: d.minimum,
		}
		raw, _ := json.Marshal(st)
		_, err := s.DB.Exec("INSERT OR IGNORE INTO statements(account_key,date,fingerprint,snapshot) VALUES(?,?,?,?)",
			st.AccountID, st.Date, statements.Fingerprint(st), string(raw))
		if err != nil {
			return err
		}
	}
	return nil
}
