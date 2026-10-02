package ledger

import (
	"fmt"
	"strings"
	"testing"
)

func TestFiveCardsAndTwoBankAccountsStaySeparate(t *testing.T) {
	s := testStore(t)
	// Deliberately share suffixes across issuer and account type.
	inputs := []struct{ issuer, kind, last string }{{"HDFC", "card", "1001"}, {"HDFC", "card", "1002"}, {"ICICI", "card", "1001"}, {"SBI", "card", "1001"}, {"AXIS", "card", "1001"}, {"HDFC", "bank", "1001"}, {"ICICI", "bank", "1001"}}
	for i, a := range inputs {
		p := testParser()
		p.Name = fmt.Sprintf("Rule %d", i)
		p.Issuer = a.issuer
		p.AccountKind = a.kind
		p.Sender = fmt.Sprintf("bank%d@example.invalid", i)
		if _, e := s.SaveParser(p); e != nil {
			t.Fatal(e)
		}
		raw := strings.ReplaceAll(string(testMail(fmt.Sprint(i), strings.Replace(testBody, "4242", a.last, 1))), "alerts@example.invalid", p.Sender)
		if _, e := s.Ingest([]byte(raw)); e != nil {
			t.Fatal(e)
		}
	}
	accounts, e := s.accounts()
	if e != nil || len(accounts) != 7 {
		t.Fatalf("%+v %v", accounts, e)
	}
	for _, a := range accounts {
		rows, e := s.Transactions(Filter{Account: a.ID})
		if e != nil || len(rows) != 1 || rows[0].AccountKind != a.Kind || rows[0].AccountID != a.ID {
			t.Fatalf("account isolation failed: %+v %v", rows, e)
		}
	}
	// Purchase and refund parser names must not create separate identities.
	p := testParser()
	p.Name = "HDFC refunds"
	p.Issuer = "hdfc"
	p.AccountKind = "card"
	p.Sender = "refunds@example.invalid"
	p.Direction = "credit"
	s.SaveParser(p)
	raw := strings.ReplaceAll(string(testMail("refund", strings.Replace(testBody, "4242", "1001", 1))), "alerts@example.invalid", p.Sender)
	if _, e = s.Ingest([]byte(raw)); e != nil {
		t.Fatal(e)
	}
	accounts, e = s.accounts()
	if e != nil || len(accounts) != 7 {
		t.Fatal("parser variants split the account")
	}
	rows, e := s.Transactions(Filter{Account: accountKey("HDFC", "card", "1001")})
	if e != nil || len(rows) != 2 {
		t.Fatalf("refund failed to join card: %+v %v", rows, e)
	}
}
func TestBankAndCardBalanceSigns(t *testing.T) {
	// Same opening/debits/credits have different meanings for cash and debt.
	if v, e := balanceDifference("card", 100000, 20000, 10000, 0, 110000); e != nil || v != 0 {
		t.Fatal(v, e)
	}
	if v, e := balanceDifference("bank", 100000, 20000, 10000, 0, 90000); e != nil || v != 0 {
		t.Fatal(v, e)
	}
	if _, e := balanceDifference("unknown", 100, 0, 0, 0, 100); e == nil {
		t.Fatal("guessed account type")
	}
}
func TestStatementRejectsAdditionalCard(t *testing.T) {
	_, headerErr := ParseHDFCStatement(syntheticStatement + "\nCredit Card No. 11111111118888\n")
	if headerErr == nil || !strings.Contains(headerErr.Error(), "additional card") {
		t.Fatal("accepted conflicting card headers")
	}
	_, e := ParseHDFCStatement(syntheticStatement + "\n[Card No : 11111111118888]\n")
	if e == nil || !strings.Contains(e.Error(), "additional card") {
		t.Fatal("assigned additional card rows to primary")
	}
	s, e := ParseHDFCStatement(syntheticStatement + "\n[Card No : 11111111114242]\n")
	if e != nil || s.AccountKind != "card" || s.AccountID != accountKey("HDFC", "card", "4242") {
		t.Fatalf("%+v %v", s, e)
	}
}
