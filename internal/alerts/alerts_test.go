package alerts_test

import (
	"strings"
	"testing"

	"github.com/audemed44/ledger/internal/alerts"
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

func TestWordings(t *testing.T) {
	p := fixture.AlertParser()
	p.AccountKind = "card"
	p.Wordings = []alerts.Wording{{
		Pattern:     `Rs\.(?P<amount>[\d,.]+) debited from account (?P<account>\d{4}) to (?P<merchant>.+) on (?P<date>\d{2}-\d{2}-\d{2})\.`,
		DateLayout:  "02-01-06",
		AccountKind: "bank",
	}}
	r, err := p.Parse(p.Sender, "Alert", "Rs.250.00 debited from account 9001 to Example Cafe on 05-10-26.")
	if err != nil || r.Transaction == nil || r.Transaction.AccountKind != "bank" || r.Transaction.Account != "9001" ||
		r.Transaction.Date != "2026-10-05T00:00:00+05:30" || r.Transaction.Currency != "INR" {
		t.Fatalf("%+v %v", r.Transaction, err)
	}
	// The main wording still reads its own emails, with the parser's type.
	if r, err = p.Parse(p.Sender, "Alert", fixture.AlertBody); err != nil || r.Transaction.AccountKind != "card" {
		t.Fatalf("%+v %v", r, err)
	}
	// Two wordings matching one email is ambiguous.
	p.Wordings = append(p.Wordings, alerts.Wording{Pattern: p.Pattern, DateLayout: p.DateLayout})
	if _, err = p.Parse(p.Sender, "Alert", fixture.AlertBody); err == nil {
		t.Fatal("ambiguous wordings accepted")
	}
	p.Wordings = []alerts.Wording{{Pattern: `(?P<amount>\d+)`, DateLayout: "2006"}}
	if p.Validate() == nil {
		t.Fatal("wording without required groups accepted")
	}
}
