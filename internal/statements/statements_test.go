package statements

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/audemed44/ledger/internal/fixture"
	"github.com/audemed44/ledger/internal/ledger"
)

const statement = fixture.Statement

func TestStatementRowsBalanceAndCredits(t *testing.T) {
	s, e := ParseHDFC(statement)
	if e != nil {
		t.Fatal(e)
	}
	if !s.Balanced || len(s.Transactions) != 3 || s.Transactions[2].Direction != "credit" ||
		s.TotalDue != 95000 || s.DueDate != "2026-10-22" || s.Account != "4242" {
		t.Fatalf("%+v", s)
	}
}

func TestStatementRoundingIsFlagged(t *testing.T) {
	text := strings.Replace(statement, "C550.00", "C550.15", 1)
	text = strings.Replace(text, "C 50.00", "C 50.15", 1)
	s, e := ParseHDFCWithTolerance(text, 0)
	if e != nil {
		t.Fatal(e)
	}
	if s.Balanced || s.Discrepancy != 15 || len(s.Warnings) == 0 {
		t.Fatalf("%+v", s)
	}
}

func TestStatementFailsClosed(t *testing.T) {
	broken := map[string]string{
		"missing row":  strings.Replace(statement, "01/10/2026| 14:10   EXAMPLE GROCER                C 50.00 l\n", "", 1),
		"broken row":   strings.Replace(statement, "C 500.00", "? 500.00", 1),
		"wrong layout": strings.ReplaceAll(statement, "HDFC", "Other"),
		"missing due":  strings.ReplaceAll(statement, "MINIMUM DUE DUE DATE", "unknown"),
		"wrong date":   strings.Replace(statement, "01/10/2026|", "99/99/2026|", 1),
	}
	for name, text := range broken {
		t.Run(name, func(t *testing.T) {
			if _, e := ParseHDFC(text); e == nil {
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
	}{
		{"950.00", true, 0}, {"949.85", true, 15}, {"949.01", true, 99},
		{"950.99", true, -99}, {"949.00", false, 100}, {"951.00", false, -100},
	} {
		text := strings.Replace(statement, "= C950.00", "= C"+c.closing, 1)
		s, e := ParseHDFC(text)
		if e != nil || s.Balanced != c.accepted || s.Discrepancy != c.difference ||
			s.RoundingAccepted != (c.accepted && c.difference != 0) {
			t.Fatalf("%s: %+v %v", c.closing, s, e)
		}
	}
	// Tolerance never hides an omitted transaction or summary mismatch.
	text := strings.Replace(statement, "C 50.00", "C 49.85", 1)
	if _, e := ParseHDFC(text); e == nil {
		t.Fatal("accepted a row-total discrepancy as rounding")
	}
}

func TestStatementRejectsAdditionalCard(t *testing.T) {
	_, headerErr := ParseHDFC(statement + "\nCredit Card No. 11111111118888\n")
	if headerErr == nil || !strings.Contains(headerErr.Error(), "additional card") {
		t.Fatal("accepted conflicting card headers")
	}
	_, e := ParseHDFC(statement + "\n[Card No : 11111111118888]\n")
	if e == nil || !strings.Contains(e.Error(), "additional card") {
		t.Fatal("assigned additional card rows to primary")
	}
	s, e := ParseHDFC(statement + "\n[Card No : 11111111114242]\n")
	if e != nil || s.AccountKind != "card" || s.AccountID != ledger.AccountKey("HDFC", "card", "4242") {
		t.Fatalf("%+v %v", s, e)
	}
}

func TestPartialRowsAndCardBranding(t *testing.T) {
	text := strings.ReplaceAll(strings.ReplaceAll(statement, "Tata Neu", "Regalia"), "Base NeuCoins*", "Reward Points")
	st, err := ParseHDFC(text)
	if err != nil || !st.Balanced {
		t.Fatal("branding blocks shared structure", err)
	}
	text = strings.Replace(text, "C 500.00", "? 500.00", 1)
	st, err = ParseHDFC(text)
	if err == nil || st.Balanced || len(st.Transactions) != 2 {
		t.Fatal("partial rows discarded or treated as validated", len(st.Transactions), err)
	}
}

func TestPDFPasswordTrials(t *testing.T) {
	fixture.RequirePDFTools(t)
	dir := t.TempDir()
	plain := filepath.Join(dir, "plain.pdf")
	encrypted := filepath.Join(dir, "locked.pdf")
	os.WriteFile(plain, fixture.PDF(statement), 0600)
	out, e := exec.Command("qpdf", "--encrypt", "fixture-two", "fixture-owner", "256", "--", plain, encrypted).CombinedOutput()
	if e != nil {
		t.Fatalf("encrypt fixture: %v %s", e, out)
	}
	text, slot, e := ExtractWithPasswords(t.Context(), encrypted, []string{"fixture-one", "fixture-two", "fixture-three"}, 0)
	if e != nil || slot != 2 || !strings.Contains(text.Layout, "EXAMPLE SHOP") || !strings.Contains(text.Raw, "EXAMPLE SHOP") {
		t.Fatalf("slot=%d err=%v", slot, e)
	}
	if _, _, e = ExtractWithPasswords(t.Context(), encrypted, []string{"fixture-one", "fixture-two"}, 1); e == nil {
		t.Fatal("specific slot fell back to other passwords")
	}
	if _, slot, e = ExtractWithPasswords(t.Context(), plain, nil, 0); e != nil || slot != 0 {
		t.Fatalf("unencrypted: %d %v", slot, e)
	}
	cancelled, cancel := context.WithCancel(t.Context())
	cancel()
	if _, _, e = ExtractWithPasswords(cancelled, encrypted, []string{"fixture-two"}, 0); e == nil {
		t.Fatal("ignored cancellation")
	}
}
