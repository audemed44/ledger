package alerts_test

import (
	"strings"
	"testing"

	"github.com/audemed44/ledger/internal/fixture"
)

func TestParserValidationAndMatching(t *testing.T) {
	body := fixture.AlertBody
	p := fixture.AlertParser()
	r, e := p.Parse(p.Sender, "Alert", body)
	if e != nil || r.Transaction == nil || r.Transaction.Amount != 123456 ||
		r.Transaction.Date != "2026-10-01T00:00:00+05:30" {
		t.Fatalf("%+v %v", r, e)
	}
	r, e = p.Parse("impostor@example.invalid", "Alert", body)
	if e != nil || r.Matched {
		t.Fatal("sender not scoped")
	}
	if _, e = p.Parse(p.Sender, "Alert", body+"\n"+body); e == nil {
		t.Fatal("ambiguous body accepted")
	}
	if _, e = p.Parse(p.Sender, "Alert", strings.Replace(body, "2026-10-01", "2026-99-01", 1)); e == nil {
		t.Fatal("bad date accepted")
	}
	p.Direction = "ignore"
	p.Pattern = "declined"
	r, e = p.Parse(p.Sender, "Alert", "declined")
	if e != nil || !r.Ignored || r.Transaction != nil {
		t.Fatal("declined handling failed")
	}
}
