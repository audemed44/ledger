package store

import (
	"strings"
	"testing"
	"time"

	"github.com/audemed44/ledger/internal/alerts"
	"github.com/audemed44/ledger/internal/fixture"
	"github.com/audemed44/ledger/internal/ledger"
)

func TestCardPaymentDescriptions(t *testing.T) {
	cards := yourCards{
		issuers: map[string]bool{"hdfc": true, "hsbc": true, "icici": true, "axis": true},
		numbers: map[string]bool{"4242": true, "8080": true},
	}
	for merchant, want := range map[string]bool{
		"IB BILLPAY DR-HDFCWI-400000XXXXXX4242":                                    true,
		"IB BILLPAY DR-HDFC1S-400000XXXXXX8080 1":                                  true,
		"EXAMPLE21PBCLJ/BILLDKHSBCCREDITCARD":                                      true,
		"INB ICICI Bank Credit card ICICI_CC Payments":                             true,
		"VPA gpay-creditcard@okpayaxis Google India Digital Services Pvt Ltd":      true,
		"VPA gpay-creditcard@okpayaxis (Google India Digital Services Pvt Ltd)":    true,
		"VPA ***********card@okpayaxis (Google India Digital Services Pvt Ltd)":    true,
		"UPI-GOOGLE INDIA DIGITAL-GPAY-CREDITCARD@OKPAYAXIS-UTIB0000001-100000001": true,
		"UPI/DR/600000000001/Google I/UTIB/gpay-credi/UPI":                         true,

		// Someone else's card, or an issuer you have no card with.
		"IB BILLPAY DR-HDFCWI-400000XXXXXX9999":    false,
		"EXAMPLE21PBCLJ/BILLDKSBICREDITCARD":       false,
		"INB SBI Bank Credit card SBI_CC Payments": false,
		"IB BILLPAY DR-ELECTRICITY-100000000001":   false,
		// Debit card withdrawals and purchases carry a masked number too.
		"ATW-400000XXXXXX4242-S1EXAMPLE-GURGAON":                          false,
		"NWD-400000XXXXXX4242-B1000001-GURUGRAM":                          false,
		"POS 400000XXXXXX4242 600000000001 08AUG26 16:59:18 SWIGGY E COM": false,
		// Google Pay to a merchant, and other Google payments.
		"VPA swiggy1online.gpay@okpayaxis (Swiggy Limited)":               false,
		"VPA ***********card@okpayaxis (Example Gifts)":                   false,
		"VPA googleplay@axisbank (Google India Digital Services Pvt Ltd)": false,
		"VPA example@okaxis (EXAMPLE PERSON)":                             false,
	} {
		if got := cards.cardPayment(merchant); got != want {
			t.Errorf("%q: got %v", merchant, got)
		}
	}
	if (yourCards{issuers: map[string]bool{}, numbers: map[string]bool{}}).cardPayment("VPA gpay-creditcard@okpayaxis (Google India Digital Services Pvt Ltd)") {
		t.Error("card payment with no cards")
	}
}

func TestCardPaymentsWaitForTheCardsCredit(t *testing.T) {
	s := testStore(t)
	today := time.Date(2026, 11, 20, 12, 0, 0, 0, time.UTC)
	s.Now = func() time.Time { return today }
	parser := func(name, issuer, kind, direction string) alerts.Parser {
		p := fixture.AlertParser()
		p.Name, p.Issuer, p.AccountKind, p.Direction = name, issuer, kind, direction
		p.Sender = strings.ToLower(strings.ReplaceAll(name, " ", "")) + "@example.invalid"
		p, err := s.SaveParser(p)
		if err != nil {
			t.Fatal(err)
		}
		return p
	}
	bankOut := parser("HDFC out", "HDFC", "bank", "debit")
	cardOut := parser("HDFC card spend", "HDFC", "card", "debit")
	cardIn := parser("HDFC card in", "HDFC", "card", "credit")
	mail := func(p alerts.Parser, key, body string) {
		raw := strings.Replace(string(fixture.Mail(key, body)), "alerts@example.invalid", p.Sender, 1)
		if _, err := s.Ingest([]byte(raw)); err != nil {
			t.Fatal(err)
		}
	}
	find := func(search string) ledger.Transaction {
		t.Helper()
		rows, _ := s.Transactions(Filter{Search: search})
		if len(rows) != 1 {
			t.Fatalf("%s: %d rows", search, len(rows))
		}
		return rows[0]
	}
	debits := func() int64 {
		totals, _ := s.MonthTotals("2026-11")
		if len(totals) == 0 {
			return 0
		}
		return totals[0].Debit
	}

	// October's spending on card 4242, paid from the bank in November.
	mail(cardOut, "spend", "INR 1,000.00 at Example Shop card 4242 on 2026-10-12")
	mail(bankOut, "bill", "INR 1,000.00 at IB BILLPAY DR-HDFCWI-400000XXXXXX4242 card 9001 on 2026-11-14")
	mail(bankOut, "coffee", "INR 250.00 at Example Cafe card 9001 on 2026-11-15")
	if bill := find("BILLPAY"); bill.Transfer != "pending" || bill.CardPayment {
		t.Fatalf("%+v", bill)
	}
	if debits() != 25000 {
		t.Fatal("card payment counted in spending", debits())
	}
	if rows, _ := s.Transactions(Filter{Kind: "transfers"}); len(rows) != 1 {
		t.Fatal(len(rows))
	}
	// A payment to someone else's card, and a withdrawal, count.
	mail(bankOut, "other", "INR 500.00 at IB BILLPAY DR-HDFCWI-400000XXXXXX9999 card 9001 on 2026-11-16")
	mail(bankOut, "atm", "INR 2,000.00 at ATW-400000XXXXXX4242-S1EXAMPLE card 9001 on 2026-11-16")
	if debits() != 275000 {
		t.Fatal(debits())
	}

	// The card's credit arrives with its statement: the two pair.
	mail(cardIn, "paid", "INR 1,000.00 at PAYMENT RECEIVED card 4242 on 2026-11-15")
	bill := find("BILLPAY DR-HDFCWI-400000XXXXXX4242")
	if bill.Transfer != "paired" || find("PAYMENT RECEIVED").Transfer != "paired" {
		t.Fatalf("%+v", bill)
	}

	// "Not a transfer" keeps a pending payment in spending.
	mail(bankOut, "bill2", "INR 3,000.00 at IB BILLPAY DR-HDFCWI-400000XXXXXX4242 card 9001 on 2026-11-18")
	byAmount := func(amount int64) ledger.Transaction {
		t.Helper()
		rows, _ := s.Transactions(Filter{Search: "BILLPAY"})
		for _, r := range rows {
			if r.Amount == amount {
				return r
			}
		}
		t.Fatal("no row of", amount)
		return ledger.Transaction{}
	}
	if byAmount(300000).Transfer != "pending" {
		t.Fatal("second payment not pending")
	}
	if err := s.SetTransfer(byAmount(300000).ID, false); err != nil {
		t.Fatal(err)
	}
	s.FindTransfers()
	if r := byAmount(300000); r.Transfer != "" || r.CardPayment {
		t.Fatalf("%+v", r)
	}

	// With no credit after 45 days, a payment counts again, shown as a card
	// payment with no card credit found.
	mail(bankOut, "bill3", "INR 4,000.00 at IB BILLPAY DR-HDFCWI-400000XXXXXX4242 card 9001 on 2026-11-19")
	if byAmount(400000).Transfer != "pending" {
		t.Fatal("third payment not pending")
	}
	today = today.AddDate(0, 0, 45)
	s.FindTransfers()
	if r := byAmount(400000); r.Transfer != "" || !r.CardPayment {
		t.Fatalf("%+v", r)
	}
	if byAmount(100000).Transfer != "paired" {
		t.Fatal("old pair undone")
	}
	// Ingesting and processing again changes nothing.
	s.Reprocess()
	if r := byAmount(400000); r.Transfer != "" || !r.CardPayment {
		t.Fatalf("%+v", r)
	}
}
