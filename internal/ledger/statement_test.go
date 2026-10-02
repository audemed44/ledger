package ledger

import (
	"strings"
	"testing"
)

// Handwritten synthetic fixture mirrors structural quirks, not real financial data.
const syntheticStatement = `HDFC Tata Neu Credit Card Statement
Credit Card No. 111111xxxxxx4242
Statement Date 02 Oct, 2026
PAYMENTS/CREDITS PURCHASES/DEBIT
PREVIOUS STATEMENT DUES FINANCE CHARGES TOTAL AMOUNT DUE
C1,000.00
- C600.00 + C550.00 + C0.00 = C950.00
TOTAL CREDIT LIMIT
AVAILABLE CREDIT LIMIT MINIMUM DUE DUE DATE
C50.00 22 Oct, 2026
Domestic Transactions
DATE & TIME TRANSACTION DESCRIPTION Base NeuCoins* AMOUNT PI
01/10/2026| 12:10   EXAMPLE SHOP                  C 500.00 l
01/10/2026| 14:10   EXAMPLE GROCER                C 50.00 l
HDFC Tata Neu Credit Card Statement
DATE & TIME TRANSACTION DESCRIPTION Base NeuCoins* AMOUNT PI
01/10/2026| 16:10   CREDIT CARD PAYMENT (Ref# 123456)       + C 600.00 l
`

func TestStatementRowsBalanceAndCredits(t *testing.T) {
	s, e := ParseHDFCStatement(syntheticStatement)
	if e != nil {
		t.Fatal(e)
	}
	if !s.Balanced || len(s.Transactions) != 3 || s.Transactions[2].Direction != "credit" || s.TotalDue != 95000 || s.DueDate != "2026-10-22" || s.Account != "4242" {
		t.Fatalf("%+v", s)
	}
}
func TestStatementRoundingIsFlagged(t *testing.T) {
	text := strings.Replace(syntheticStatement, "C550.00", "C550.15", 1)
	text = strings.Replace(text, "C 50.00", "C 50.15", 1)
	s, e := ParseHDFCStatementWithTolerance(text, 0)
	if e != nil {
		t.Fatal(e)
	}
	if s.Balanced || s.Discrepancy != 15 || len(s.Warnings) == 0 {
		t.Fatalf("%+v", s)
	}
}
func TestStatementFailsClosed(t *testing.T) {
	for name, text := range map[string]string{"missing row": strings.Replace(syntheticStatement, "01/10/2026| 14:10   EXAMPLE GROCER                C 50.00 l\n", "", 1), "broken row": strings.Replace(syntheticStatement, "C 500.00", "? 500.00", 1), "wrong layout": strings.ReplaceAll(syntheticStatement, "HDFC", "Other"), "missing due": strings.ReplaceAll(syntheticStatement, "MINIMUM DUE DUE DATE", "unknown"), "wrong date": strings.Replace(syntheticStatement, "01/10/2026|", "99/99/2026|", 1)} {
		t.Run(name, func(t *testing.T) {
			if _, e := ParseHDFCStatement(text); e == nil {
				t.Fatal("accepted invalid statement")
			}
		})
	}
}

func TestRoundingToleranceBoundary(t *testing.T) {
	for _, c := range []struct {
		closing    string
		accepted   bool
		difference int64
	}{{"950.00", true, 0}, {"949.85", true, 15}, {"949.01", true, 99}, {"950.99", true, -99}, {"949.00", false, 100}, {"951.00", false, -100}} {
		text := strings.Replace(syntheticStatement, "= C950.00", "= C"+c.closing, 1)
		s, e := ParseHDFCStatement(text)
		if e != nil || s.Balanced != c.accepted || s.Discrepancy != c.difference || s.RoundingAccepted != (c.accepted && c.difference != 0) {
			t.Fatalf("%s: %+v %v", c.closing, s, e)
		}
	}
	// Tolerance never hides an omitted transaction or summary mismatch.
	text := strings.Replace(syntheticStatement, "C 50.00", "C 49.85", 1)
	if _, e := ParseHDFCStatement(text); e == nil {
		t.Fatal("accepted a row-total discrepancy as rounding")
	}
}
