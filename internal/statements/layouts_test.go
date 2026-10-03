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

func TestSBISavings(t *testing.T) {
	text := fixture.SBIStatement
	check(t, "sbi-savings", layout(text), Statement{
		Issuer: "SBI", AccountKind: "bank", Account: "4242", Date: "2026-10-31",
		Opening: 1000000, TotalDue: 1224950,
	}, []string{
		"2026-10-02 +5000.00 UPI/CR/600000000001/EXAMPLE FRIEND/EXMP/friend@exam",
		"2026-10-05 -2000.00 ATM WDL EXAMPLE TOWN",
		"2026-10-20 -750.50 BY TRANSFER-INB EXAMPLE ELECTRICITY BOARD BILL PAYMENT",
	})
	s, _ := ParseWith("sbi-savings", layout(text))
	if s.Transactions[1].Reference != "1234" || s.Transactions[0].Reference != "" {
		t.Fatal("references", s.Transactions)
	}
	failsClosed(t, "sbi-savings", map[string]Text{
		"missing row":     layout(strings.Replace(text, "   05-10-26       ATM WDL EXAMPLE TOWN                                                                    1234                    0          2000.00        13000.00\n", "", 1)),
		"running balance": layout(strings.Replace(text, "2000.00        13000.00", "2000.00        13100.00", 1)),
		"both columns":    layout(strings.Replace(text, "1234                    0          2000.00", "1234                    5.00       2000.00", 1)),
		"closing":         layout(strings.Replace(text, "31-10-26:                 12249.50", "31-10-26:                 12200.50", 1)),
		"two accounts":    layout(text + "TRANSACTION OVERVIEW\n"),
		"no account":      layout(strings.Replace(text, " XXXXXXX4242\n       Name", "\n       Name", 1)),
		"wrong layout":    layout(strings.ReplaceAll(text, "sbi.co.in", "example.in")),
	})
}

func TestHDFCBankSavings(t *testing.T) {
	text := fixture.HDFCBankStatement
	check(t, "hdfc-savings", layout(text), Statement{
		Issuer: "HDFC", AccountKind: "bank", Account: "4242", Date: "2026-10-31",
		Opening: 2000000, TotalDue: 2749950,
	}, []string{
		"2026-10-02 -500.50 UPI-EXAMPLE GROCER-GROCER.EXAMPLE@OKEXAMPLE-EXMP0000001-600000000001-UPI",
		"2026-10-05 -2000.00 ATW-400000XXXXXX4242-EXAMPLE TOWN",
		"2026-10-25 +9000.00 NEFT CR-EXMP0000001-EXAMPLE EMPLOYER PRIVATE LIMITED-EXAMPLE PERSON-EXMPN000000000001",
		"2026-10-28 +1000.00 UPI-EXAMPLE FRIEND-FRIEND@OKEXAMPLE-EXMP0000002-600000000002-FOR DINNER SHARE",
	})
	s, _ := ParseWith("hdfc-savings", layout(text))
	if s.Transactions[1].Reference != "1001" {
		t.Fatal("reference", s.Transactions[1])
	}
	failsClosed(t, "hdfc-savings", map[string]Text{
		"missing row":     layout(strings.Replace(text, "05/10/2026       ATW-400000XXXXXX4242-EXAMPLE TOWN                      1001                                    05/10/2026                             2,000.00                 0.00               17,499.50\n", "", 1)),
		"running balance": layout(strings.Replace(text, "17,499.50", "17,500.50", 1)),
		"count":           layout(strings.Replace(text, "2                         2   ", "3                         2   ", 1)),
		"other account":   layout(strings.Replace(text, "50100000004242              OTHER", "50100000008080              OTHER", 1)),
		"no summary":      layout(strings.Replace(text, "20,000.00                                  2", "", 1)),
		"wrong layout":    layout(strings.ReplaceAll(text, "HDFC BANK LIMITED", "OTHER BANK")),
		"other layout":    layout(fixture.SBIStatement),
	})
}

// Each layout reads only its own bank's statements.
func TestLayoutsRefuseOtherBanks(t *testing.T) {
	fixtures := map[string]Text{
		"hdfc-credit-card":  layout(fixture.Statement),
		"axis-credit-card":  layout(fixture.AxisStatement),
		"icici-credit-card": {Layout: fixture.ICICIStatement.Layout, Raw: fixture.ICICIStatement.Raw},
		"idfc-credit-card":  layout(fixture.IDFCStatement),
		"sbi-savings":       layout(fixture.SBIStatement),
		"hdfc-savings":      layout(fixture.HDFCBankStatement),
	}
	if len(fixtures) != len(Adapters) {
		t.Fatal("a layout has no fixture")
	}
	for _, a := range Adapters {
		for id, text := range fixtures {
			s, err := ParseWith(a.ID, text)
			if accepted := err == nil && s.Balanced; accepted != (id == a.ID) {
				t.Errorf("%s reading %s: %v %v", a.ID, id, err, s.Balanced)
			}
		}
	}
}

// Older SBI statements mark an empty column "-"; SBI has also printed a
// running balance a paisa off the line before, which the rounding
// tolerance accepts with a warning.
func TestSBIDashesAndRoundingSlips(t *testing.T) {
	dashes := strings.NewReplacer(
		"-           5000.00                    0       15000.00", "-           5000.00                    -       15000.00",
		"1234                    0          2000.00", "1234                    -          2000.00",
	).Replace(fixture.SBIStatement)
	if s, err := ParseWith("sbi-savings", layout(dashes)); err != nil || !s.Balanced || len(s.Transactions) != 3 {
		t.Fatalf("%+v %v", s, err)
	}
	// Opening 10,000.00 but the first running balance one paisa high, and
	// every later balance following on from it.
	slip := strings.NewReplacer("15000.00", "15000.01", "13000.00", "13000.01", "12249.50", "12249.51").Replace(fixture.SBIStatement)
	s, err := ParseWith("sbi-savings", layout(slip))
	if err != nil || !s.Balanced || len(s.Warnings) < 2 {
		t.Fatalf("%+v %v", s, err)
	}
	if _, err = ParseWithTolerance("sbi-savings", layout(slip), 0); err == nil {
		t.Fatal("slip accepted with no tolerance")
	}
}

// A long ICICI merchant wraps its raw row onto the next lines.
func TestICICIWrappedRawRow(t *testing.T) {
	raw := strings.Replace(fixture.ICICIStatement.Raw, "EXAMPLE BOOKS 24X7 BENGALURU IN 15 750.25",
		"EXAMPLE BOOKS 24X7 BENGALURU\nIN\n15 750.25", 1)
	s, err := ParseWith("icici-credit-card", Text{Layout: fixture.ICICIStatement.Layout, Raw: raw})
	if err != nil || !s.Balanced || s.Transactions[2].Merchant != "EXAMPLE BOOKS 24X7 BENGALURU IN" {
		t.Fatalf("%+v %v", s.Transactions, err)
	}
}

// A month with nothing on the card has no transaction section, and only
// the "(FIRST Select XX4242)" card line.
func TestIDFCQuietMonth(t *testing.T) {
	text := fixture.IDFCStatement
	text = text[:strings.Index(text, "YOUR TRANSACTIONS")]
	text = strings.NewReplacer(
		"r1,500.00\n", "r0.00\n", "r30.00\n", "r0.00\n",
		"r20.00 CR\n      Pay", "r50.00 CR\n      Pay", "r20.00 CR    ", "r50.00 CR    ",
	).Replace(text)
	s, err := ParseWith("idfc-credit-card", layout(text))
	if err != nil || !s.Balanced || len(s.Transactions) != 0 || s.Account != "4242" || s.TotalDue != -5000 {
		t.Fatalf("%+v %v", s, err)
	}
	// Without lines, the summary still has to add up.
	moved := strings.Replace(text, "r0.00\n          Payments", "r30.00\n          Payments", 1)
	if s, err = ParseWith("idfc-credit-card", layout(moved)); err == nil && s.Balanced {
		t.Fatal("summary with movement but no lines accepted")
	}
}
