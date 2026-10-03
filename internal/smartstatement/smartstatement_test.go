package smartstatement

import (
	"errors"
	"net/url"
	"strings"
	"testing"

	"github.com/audemed44/ledger/internal/fixture"
)

func TestFetchStatement(t *testing.T) {
	bank := fixture.NewSmartStatementBank(t, "Secret12", fixture.PDF(fixture.HDFCBankStatement))
	f := &Fetcher{Host: strings.TrimPrefix(bank.URL, "https://"), Client: bank.Client()}
	pdf, name, err := f.Fetch(t.Context(), bank.SmartStatementLink("job1"), "Secret12")
	if err != nil || !strings.HasPrefix(string(pdf), "%PDF-") || name != "XXXXXXXX4242_16Sep2026_TO_15Oct2026.pdf" {
		t.Fatal(name, err)
	}
	if _, _, err = f.Fetch(t.Context(), bank.SmartStatementLink("job1"), "wrong"); !errors.Is(err, ErrPassword) {
		t.Fatal("wrong password:", err)
	}
	if _, _, err = f.Fetch(t.Context(), bank.SmartStatementLink("expired"), "Secret12"); !errors.Is(err, ErrExpired) {
		t.Fatal("expired link:", err)
	}
	// Only the bank's host, over https, is ever sent a password.
	hits := bank.Hits.Load()
	for _, link := range []string{
		strings.Replace(bank.SmartStatementLink("job1"), "https://", "http://", 1),
		"https://attacker.invalid/HDFCRestFulService/GetStatement.jsp?jobkey=1",
		strings.Replace(bank.SmartStatementLink("job1"), "GetStatement.jsp", "Other.jsp", 1),
	} {
		if _, _, err = f.Fetch(t.Context(), link, "Secret12"); err == nil {
			t.Error("fetched", link)
		}
	}
	if bank.Hits.Load() != hits {
		t.Fatal("requests sent for links that aren't the bank's")
	}
}

// Reference values from HDFC's own encrypt.js, run in Node with
// Math.random fixed so the seed is 42.
func TestEncryptMatchesTheBanksScript(t *testing.T) {
	for text, want := range map[string]string{
		"ab":                      "2AFF0D",
		"TOKEN0123456789Secret12": "2A0A36D46AC89DBCAD81C69EA0B8A5AE72B264959B63F150",
		"x~ Q!":                   "2AD63A0F1041",
	} {
		if got := Encrypt(text, 42); got != want {
			t.Errorf("%q: %s, want %s", text, got, want)
		}
	}
}

func TestIsLink(t *testing.T) {
	u, _ := url.Parse("https://smartstatements.hdfc.bank.in/HDFCRestFulService/GetStatement.jsp?jobkey=abc&utm_source=x")
	if !IsLink(u, Host) {
		t.Fatal("real link refused")
	}
	u, _ = url.Parse("https://smartstatements.hdfc.bank.in.attacker.invalid/HDFCRestFulService/GetStatement.jsp?jobkey=abc")
	if IsLink(u, Host) {
		t.Fatal("look-alike host accepted")
	}
}
