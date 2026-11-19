package store

import (
	"strconv"
	"strings"
	"testing"

	"github.com/audemed44/ledger/internal/alerts"
	"github.com/audemed44/ledger/internal/fixture"
)

func TestCategories(t *testing.T) {
	s := testStore(t)
	cats, _ := s.Categories()
	if len(cats) != len(DefaultCategories) {
		t.Fatal(len(cats))
	}
	// Opening again doesn't bring back a category you removed.
	id := func(name string) int64 {
		t.Helper()
		cats, _ := s.Categories()
		for _, c := range cats {
			if c.Name == name {
				return c.ID
			}
		}
		t.Fatal("no category", name)
		return 0
	}
	if err := s.DeleteCategory(id("Gifts")); err != nil {
		t.Fatal(err)
	}
	if err := s.migrateCategories(); err != nil {
		t.Fatal(err)
	}
	if cats, _ = s.Categories(); len(cats) != len(DefaultCategories)-1 {
		t.Fatal("removed category came back")
	}
	if _, err := s.SaveCategory(0, " rent "); err == nil {
		t.Fatal("duplicate name allowed")
	}

	p := fixture.AlertParser()
	p.Issuer, p.AccountKind, p.Direction = "HDFC", "bank", "debit"
	p, _ = s.SaveParser(p)
	mail := func(p alerts.Parser, key, body string) {
		raw := strings.Replace(string(fixture.Mail(key, body)), "alerts@example.invalid", p.Sender, 1)
		if _, err := s.Ingest([]byte(raw)); err != nil {
			t.Fatal(err)
		}
	}
	mail(p, "rent", "INR 38,000.00 at VPA example@okhdfc (EXAMPLE LANDLORD) card 9001 on 2026-11-01")
	mail(p, "food", "INR 300.00 at SWIGGY FOOD card 9001 on 2026-11-02")
	mail(p, "mart", "INR 200.00 at SWIGGY INSTAMART card 9001 on 2026-11-03")
	mail(p, "cash", "INR 20,000.00 at ATW-400000XXXXXX4242-S1EXAMPLE card 9001 on 2026-11-04")
	category := func(search string) string {
		t.Helper()
		rows, _ := s.Transactions(Filter{Search: search})
		if len(rows) != 1 {
			t.Fatalf("%s: %d", search, len(rows))
		}
		return rows[0].Category + "/" + rows[0].CategorySource
	}

	// Rules reach what's already there, and what comes later; the more
	// specific text wins, whichever came first.
	for _, r := range []CategoryRule{
		{Contains: "SWIGGY", CategoryID: id("Food delivery")},
		{Contains: "instamart", CategoryID: id("Groceries")},
		{Contains: "EXAMPLE LANDLORD", CategoryID: id("Rent")},
		{Contains: "ATW-", CategoryID: id("Cash"), Issuer: "SBI"},
	} {
		if _, err := s.SaveCategoryRule(r); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.SaveCategoryRule(CategoryRule{Contains: "ab", CategoryID: id("Rent")}); err == nil {
		t.Fatal("too short a rule")
	}
	if category("SWIGGY FOOD") != "Food delivery/rule" || category("INSTAMART") != "Groceries/rule" ||
		category("LANDLORD") != "Rent/rule" || category("ATW-") != "/" {
		t.Fatal(category("SWIGGY FOOD"), category("INSTAMART"), category("LANDLORD"), category("ATW-"))
	}
	mail(p, "rent2", "INR 1,390.00 at VPA example@okhdfc (EXAMPLE LANDLORD) card 9001 on 2026-11-05")
	if rows, _ := s.Transactions(Filter{Search: "LANDLORD", Category: strconv.FormatInt(id("Rent"), 10)}); len(rows) != 2 {
		t.Fatal("new rent payment not categorised", rows)
	}

	// A month's cash that paid the rent: set by hand, it stays put.
	cash, _ := s.Transactions(Filter{Search: "ATW-"})
	if err := s.SetCategory(cash[0].ID, id("Rent")); err != nil {
		t.Fatal(err)
	}
	if _, err := s.SaveCategoryRule(CategoryRule{Contains: "ATW-", CategoryID: id("Cash")}); err != nil {
		t.Fatal(err)
	}
	s.Reprocess()
	if category("ATW-") != "Rent/manual" {
		t.Fatal(category("ATW-"))
	}
	if rows, _ := s.Transactions(Filter{Category: "none"}); len(rows) != 0 {
		t.Fatal("uncategorised left", rows)
	}

	totals, _ := s.MonthCategories("2026-11")
	if len(totals) != 3 || totals[0].Category != "Rent" || totals[0].Amount != 5939000 || totals[0].Count != 3 {
		t.Fatalf("%+v", totals)
	}

	// Removing a rule, or a category, hands its lines back.
	rules, _ := s.CategoryRules()
	if rules[0].Contains != "EXAMPLE LANDLORD" {
		t.Fatal("rules not most specific first", rules)
	}
	if err := s.DeleteCategoryRule(rules[0].ID); err != nil {
		t.Fatal(err)
	}
	if category("SWIGGY FOOD") != "Food delivery/rule" || len(must(s.Transactions(Filter{Category: "none"}))) != 2 {
		t.Fatal("rule removal")
	}
	if err := s.DeleteCategory(id("Rent")); err != nil {
		t.Fatal(err)
	}
	if category("ATW-") != "Cash/rule" {
		t.Fatal("deleted category's hand-set line not handed back to the rules", category("ATW-"))
	}
	if err := s.SetCategory(cash[0].ID, 0); err != nil {
		t.Fatal(err)
	}
}

func must[T any](v T, _ error) T { return v }
