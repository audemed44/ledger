package alerts

import (
	"strings"
	"testing"
	"unicode/utf16"
)

// mark tags the first occurrence of text in body, in UTF-16 offsets.
func mark(t *testing.T, body, field, text string) Mark {
	t.Helper()
	i := strings.Index(body, text)
	if i < 0 {
		t.Fatalf("%q not in body", text)
	}
	start := len(utf16.Encode([]rune(body[:i])))
	return Mark{Field: field, Start: start, End: start + len(utf16.Encode([]rune(text)))}
}

func TestFromExampleReadsTheNextAlert(t *testing.T) {
	// Synthetic HTML-extracted alert: one long line, a ₹ before the offsets
	// and an untagged balance that changes between emails.
	example := "Dear Customer,\n₹ Thank you for using card ending 4242 for Rs.1,234.56 at EXAMPLE SHOP on 01-Oct-26 14:05:09. Avl bal Rs.10,000.00. Not you? Call us.\n"
	next := "Dear Customer,\n₹ Thank you for using card ending 1001 for Rs.12,34,567.00 at ANOTHER STORE PVT LTD on 9-Nov-26 09:41:00. Avl bal Rs.99.10. Not you? Call us.\n"
	marks := []Mark{
		mark(t, example, "merchant", "EXAMPLE SHOP"),
		mark(t, example, "amount", "1,234.56"),
		mark(t, example, "account", "4242"),
		mark(t, example, "date", "01-Oct-26 14:05:09"),
	}
	got, err := FromExample("Card alert: Rs.1,234.56 spent", example, marks)
	if err != nil {
		t.Fatal(err)
	}
	if got.DateLayout != "2-Jan-06 15:04:05" || got.Subject == "" {
		t.Fatalf("%+v", got)
	}
	p := Parser{Name: "Example", Sender: "alerts@example.invalid", Subject: got.Subject, Pattern: got.Pattern,
		DateLayout: got.DateLayout, Timezone: "Asia/Kolkata", Currency: "INR", Direction: "debit"}
	r, err := p.Parse(p.Sender, "Card alert: Rs.12,34,567.00 spent", next)
	if err != nil || r.Transaction == nil {
		t.Fatalf("%s: %+v %v", got.Pattern, r, err)
	}
	tx := r.Transaction
	if tx.Amount != 123456700 || tx.Merchant != "ANOTHER STORE PVT LTD" || tx.Account != "1001" ||
		tx.Date != "2026-11-09T09:41:00+05:30" {
		t.Fatalf("%+v", tx)
	}
}

func TestFromExampleMultilineAndLastField(t *testing.T) {
	body := "Amount: INR 1290.00\nMerchant: Example Store\nCard: 4242\nDate: 2026-10-01\nReference: EXAMPLE-001\n"
	got, err := FromExample("Purchase alert", body, []Mark{
		mark(t, body, "currency", "INR"),
		mark(t, body, "amount", "1290.00"),
		mark(t, body, "merchant", "Example Store"),
		mark(t, body, "account", "4242"),
		mark(t, body, "date", "2026-10-01"),
		mark(t, body, "reference", "EXAMPLE-001"),
	})
	if err != nil || got.DateLayout != "2006-1-2" {
		t.Fatalf("%+v %v", got, err)
	}
	p := Parser{Name: "Example", Sender: "a@example.invalid", Pattern: got.Pattern, DateLayout: got.DateLayout,
		Timezone: "UTC", Currency: "INR", Direction: "debit"}
	r, err := p.Parse(p.Sender, "", strings.NewReplacer("Example Store", "Corner Café & Co", "EXAMPLE-001", "ZX9").Replace(body))
	if err != nil || r.Transaction.Merchant != "Corner Café & Co" || r.Transaction.Reference != "ZX9" {
		t.Fatalf("%s: %+v %v", got.Pattern, r.Transaction, err)
	}
}

func TestDateLayouts(t *testing.T) {
	for value, want := range map[string]string{
		"01/10/2026":           "2/1/2006",
		"10/31/2026":           "1/2/2006",
		"2026-10-01 14:05":     "2006-1-2 15:04",
		"Oct 1, 2026":          "Jan 2, 2006",
		"01 October 2026":      "2 January 2006",
		"Thu, 01 Oct 2026":     "Mon, 2 Jan 2006",
		"01-10-26":             "2-1-06",
		"01-Oct-2026 02:05 PM": "2-Jan-2006 3:04 PM",
	} {
		_, layout, err := dateLayout(value)
		if err != nil || layout != want {
			t.Errorf("%q: %q %v, want %q", value, layout, err, want)
		}
	}
	for _, bad := range []string{"01 Oct", "yesterday", "123456"} {
		if _, _, err := dateLayout(bad); err == nil {
			t.Errorf("%q accepted", bad)
		}
	}
}

func TestFromExampleRefusesBadSelections(t *testing.T) {
	body := "Spent Rs.500.00 at SHOP on card XX4242 on 01/10/2026"
	for name, marks := range map[string][]Mark{
		"none":            nil,
		"masked card":     {mark(t, body, "account", "XX4242")},
		"symbol amount":   {mark(t, body, "amount", "Rs.500.00")},
		"symbol currency": {mark(t, body, "currency", "Rs")},
		"out of range":    {{Field: "amount", Start: 5, End: 500}},
		"overlap":         {mark(t, body, "amount", "500.00"), mark(t, body, "merchant", "00 at SHOP")},
		"twice":           {mark(t, body, "amount", "500.00"), mark(t, body, "amount", "500.00")},
		"unknown":         {{Field: "balance", Start: 0, End: 5}},
		"padded":          {mark(t, body, "merchant", " SHOP ")},
	} {
		if _, err := FromExample("", body, marks); err == nil {
			t.Errorf("%s accepted", name)
		}
	}
}

func TestByteOffsetCountsUTF16(t *testing.T) {
	s := "₹𝟙a" // 3 bytes/1 unit, 4 bytes/2 units, 1 byte/1 unit
	for units, want := range map[int]int{0: 0, 1: 3, 2: -1, 3: 7, 4: 8, 5: -1} {
		if got := byteOffset(s, units); got != want {
			t.Errorf("%d: %d want %d", units, got, want)
		}
	}
}

// Axis-style HTML alerts put each value on its own line with a label, and
// lots of whitespace between them. A long gap with few words stays whole,
// so its words aren't required twice.
func TestFromExampleWideGapsWithFewWords(t *testing.T) {
	pad := "\n" + strings.Repeat("   \n", 30)
	body := "Transaction Amount:" + pad + "INR 537" + pad + "Merchant Name:" + pad + "Example Store" + pad +
		"Credit Card No." + pad + "XX4242" + pad + "Date & Time:" + pad + "01-10-2026, 20:01:18 IST\n"
	got, err := FromExample("INR 537 spent on credit card no. XX4242", body, []Mark{
		mark(t, body, "amount", "537"),
		mark(t, body, "merchant", "Example Store"),
		mark(t, body, "account", "4242"),
		mark(t, body, "date", "01-10-2026"),
	})
	if err != nil {
		t.Fatal(err)
	}
	next := strings.NewReplacer("537", "1,581.20", "Example Store", "Other Shop", "01-10-2026", "02-11-2026").Replace(body)
	p := Parser{Name: "Example", Sender: "a@example.invalid", Pattern: got.Pattern, DateLayout: got.DateLayout,
		Timezone: "Asia/Kolkata", Currency: "INR", Direction: "debit"}
	r, err := p.Parse(p.Sender, "", next)
	if err != nil || r.Transaction == nil || r.Transaction.Amount != 158120 || r.Transaction.Merchant != "Other Shop" {
		t.Fatalf("%s: %+v %v", got.Pattern, r.Transaction, err)
	}
}
