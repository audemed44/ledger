package statements

import (
	"strings"
	"testing"

	"github.com/audemed44/ledger/internal/fixture"
	"github.com/audemed44/ledger/internal/ledger"
)

// row is a compact view of a parsed line, for comparing.
func row(t ledger.Transaction) string {
	sign := "-"
	if t.Direction == "credit" {
		sign = "+"
	}
	return t.Date[:10] + " " + sign + ledger.Decimal(t.Amount) + " " + t.Merchant
}

// check parses text with layout id and compares the statement's facts and lines.
func check(t *testing.T, id string, text Text, want Statement, rows []string) {
	t.Helper()
	s, err := ParseWith(id, text)
	if err != nil {
		t.Fatal(err)
	}
	if !s.Balanced || s.Discrepancy != 0 || s.Account != want.Account || s.AccountKind != want.AccountKind ||
		s.Issuer != want.Issuer || s.Date != want.Date || s.DueDate != want.DueDate ||
		s.Opening != want.Opening || s.TotalDue != want.TotalDue || s.MinimumDue != want.MinimumDue {
		t.Fatalf("%+v", s)
	}
	if s.AccountID != ledger.AccountKey(want.Issuer, want.AccountKind, want.Account) {
		t.Fatal("account key")
	}
	got := []string{}
	for _, r := range s.Transactions {
		got = append(got, row(r))
	}
	if strings.Join(got, "\n") != strings.Join(rows, "\n") {
		t.Fatalf("rows:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(rows, "\n"))
	}
}

// failsClosed checks that each broken copy of a statement is refused:
// it fails to parse, or doesn't balance, so it can't be imported.
func failsClosed(t *testing.T, id string, broken map[string]Text) {
	t.Helper()
	for name, text := range broken {
		if s, err := ParseWith(id, text); err == nil && s.Balanced {
			t.Errorf("%s accepted", name)
		}
	}
}

func layout(s string) Text { return Text{Layout: s} }

func TestAxisCreditCard(t *testing.T) {
	text := fixture.AxisStatement
	check(t, "axis-credit-card", layout(text), Statement{
		Issuer: "Axis", AccountKind: "card", Account: "4242", Date: "2026-10-18", DueDate: "2026-11-07",
		Opening: 50000, TotalDue: 118000, MinimumDue: 20000,
	}, []string{
		"2026-10-01 +500.00 BBPS PAYMENT RECEIVED",
		"2026-10-03 -1000.00 EXAMPLE OUTDOOR SUPPLIES PRIVATE LIMI TED, PUNE",
		"2026-10-06 +100.00 REFUND EXAMPLE CAFE",
		"2026-10-15 -237.29 LATE PAYMENT FEE",
		"2026-10-15 -42.71 GST",
	})
	// A credit balance carried in and out.
	credit := strings.NewReplacer("          500.00                      500.00", "          500.00 Cr                   500.00",
		"1,180.00 Dr                   over", "180.00 Dr                     over").Replace(text)
	if s, err := ParseWith("axis-credit-card", layout(credit)); err != nil || !s.Balanced || s.Opening != -50000 {
		t.Fatalf("%+v %v", s, err)
	}
	failsClosed(t, "axis-credit-card", map[string]Text{
		"missing row":   layout(strings.Replace(text, " 15/10/2026            GST                                                                                                                                                                                                   42.71 Dr\n", "", 1)),
		"broken row":    layout(strings.Replace(text, "237.29 Dr", "237.29 ??", 1)),
		"second card":   layout(strings.Replace(text, "Card No:         400000******4242", "Card No:         400000******8080", 1)),
		"wrong layout":  layout(strings.ReplaceAll(text, "Axis Bank", "Other Bank")),
		"no summary":    layout(strings.Replace(text, "1,180.00 Dr                   over", "over", 1)),
		"wrong balance": layout(strings.Replace(text, "1,180.00 Dr                   over", "1,181.00 Dr                   over", 1)),
		"other layout":  layout(fixture.Statement),
	})
}

func TestICICICreditCard(t *testing.T) {
	text := Text{Layout: fixture.ICICIStatement.Layout, Raw: fixture.ICICIStatement.Raw}
	check(t, "icici-credit-card", text, Statement{
		Issuer: "ICICI", AccountKind: "card", Account: "4242", Date: "2026-10-12", DueDate: "2026-10-30",
		Opening: 200000, TotalDue: 125050, MinimumDue: 10000,
	}, []string{
		"2026-09-14 -500.25 EXAMPLE TELECOM MUMBAI IN",
		"2026-09-20 +2000.00 BBPS PAYMENT RECEIVED",
		"2026-10-02 -750.25 EXAMPLE BOOKS 24X7 BENGALURU IN",
	})
	with := func(raw string) Text { return Text{Layout: text.Layout, Raw: raw} }
	failsClosed(t, "icici-credit-card", map[string]Text{
		"no raw text":   {Layout: text.Layout},
		"missing row":   with(strings.Replace(text.Raw, "02/10/2026 1000000003 EXAMPLE BOOKS 24X7 BENGALURU IN 15 750.25\n", "", 1)),
		"foreign row":   with(strings.Replace(text.Raw, "IN 15 750.25", "US 15 USD 9.00 750.25 X", 1)),
		"second card":   with(strings.Replace(text.Raw, "Statement period", "4000XXXXXXXX8080\nStatement period", 1)),
		"credit missed": with(strings.Replace(text.Raw, "2,000.00 CR", "2,000.00", 1)),
		"wrong layout":  {Layout: strings.ReplaceAll(text.Layout, "icicibank", "example"), Raw: text.Raw},
		"no summary":    {Layout: strings.Replace(text.Layout, "Previous Balance", "Earlier", 1), Raw: text.Raw},
	})
}

func TestIDFCCreditCard(t *testing.T) {
	text := fixture.IDFCStatement
	check(t, "idfc-credit-card", layout(text), Statement{
		Issuer: "IDFC", AccountKind: "card", Account: "4242", Date: "2026-10-24", DueDate: "2026-11-08",
		Opening: -5000, TotalDue: -2000,
	}, []string{
		"2026-10-02 -1500.00 EXAMPLE KITCHEN, PUNE",
		"2026-10-10 -30.00 LATE FEE REVERSAL ADJ GST",
		"2026-10-20 +1500.00 BBPS CC Payment/EXAMPLE0000001",
	})
	failsClosed(t, "idfc-credit-card", map[string]Text{
		"missing row":  layout(strings.Replace(text, "10 Oct 26                          LATE FEE REVERSAL ADJ GST                                                                         30.00 DR\n", "", 1)),
		"broken row":   layout(strings.Replace(text, "1,500.00 DR", "1,500.00", 1)),
		"second card":  layout(strings.Replace(text, "Payments & Other Credits", "Card Number: XXXX 8080\nPayments & Other Credits", 1)),
		"in debit":     layout(strings.Replace(text, "r20.00 CR\n      Pay", "r20.00\n      Pay", 1)),
		"no summary":   layout(strings.Replace(text, "EMI & Other Debits", "Other", 1)),
		"wrong layout": layout(strings.ReplaceAll(text, "IDFC FIRST", "OTHER")),
		"bad date":     layout(strings.Replace(text, "02 Oct 26", "32 Oct 26", 1)),
	})
}
