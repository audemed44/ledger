package pattern

import (
	"regexp"
	"testing"
)

func TestWholeMatchesTheNextEmail(t *testing.T) {
	for _, tt := range []struct{ example, next, other string }{
		{"Your Example Bank statement for October-2026", "Your Example Bank statement for November-2026", "Your Other Bank statement for November-2026"},
		{"Rs.1,234.00 spent on card 4242", "Rs.12,34,567.89 spent on card 1001", "Rs.12 refunded on card 1001"},
		{"statement_4242_Oct2026.pdf", "statement_4242_Sept2026.pdf", "statement_4242_Oct2026.pdf.exe"},
	} {
		re := regexp.MustCompile(Whole(tt.example))
		if !re.MatchString(tt.example) || !re.MatchString(tt.next) || re.MatchString(tt.other) {
			t.Errorf("%q → %s", tt.example, re)
		}
	}
	if Whole("  ") != "" {
		t.Fatal("blank example should give no pattern")
	}
}

func TestLiteralKeepsTrailingFullStop(t *testing.T) {
	re := regexp.MustCompile("^" + Literal("balance 1,234.56.") + "$")
	if !re.MatchString("balance 99.") || re.MatchString("balance 99") {
		t.Fatal(re)
	}
}
