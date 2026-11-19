package store

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/audemed44/ledger/internal/alerts"
	"github.com/audemed44/ledger/internal/fixture"
)

func TestMonthInsights(t *testing.T) {
	s := testStore(t)
	p := fixture.AlertParser()
	p.Issuer, p.AccountKind, p.Direction = "HDFC", "bank", "debit"
	p, _ = s.SaveParser(p)
	n := 0
	mail := func(amount, merchant, date string) {
		n++
		raw := strings.Replace(string(fixture.Mail(fmt.Sprintf("m%d", n),
			"INR "+amount+" at "+merchant+" card 9001 on "+date)), "alerts@example.invalid", p.Sender, 1)
		if _, err := s.Ingest([]byte(raw)); err != nil {
			t.Fatal(err)
		}
	}
	// Six months of ₹30,000 rent and ₹4,000 of food, then a November with
	// a big game spend, less food, and something uncategorised.
	for m := 5; m <= 10; m++ {
		mail("30,000.00", "EXAMPLE LANDLORD", fmt.Sprintf("2026-%02d-01", m))
		mail("4,000.00", "EXAMPLE FOOD", fmt.Sprintf("2026-%02d-10", m))
	}
	mail("30,000.00", "EXAMPLE LANDLORD", "2026-11-01")
	if Payee("VPA example@okhdfc (EXAMPLE PAYEE)") != "EXAMPLE PAYEE" || Payee("SWIGGY") != "SWIGGY" {
		t.Fatal("payee")
	}
	mail("1,000.00", "EXAMPLE FOOD", "2026-11-05")
	mail("12,000.00", "EXAMPLE GAME STORE", "2026-11-08")
	mail("3,000.00", "SOMEONE", "2026-11-09")
	cats, _ := s.Categories()
	id := map[string]int64{}
	for _, c := range cats {
		id[c.Name] = c.ID
	}
	for text, cat := range map[string]string{"LANDLORD": "Rent", "EXAMPLE FOOD": "Food delivery", "GAME STORE": "Games"} {
		if _, err := s.SaveCategoryRule(CategoryRule{Contains: text, CategoryID: id[cat]}); err != nil {
			t.Fatal(err)
		}
	}

	now := time.Date(2026, 11, 10, 12, 0, 0, 0, time.Local)
	in, err := s.MonthInsights("2026-11", now)
	if err != nil {
		t.Fatal(err)
	}
	if in.Total != 4600000 || in.Count != 4 || in.Previous != 3400000 || in.Average != 3400000 ||
		in.BaselineCount != 6 || !in.Current || in.Days != 10 || in.DaysInMonth != 30 || in.Projected != 5400000 {
		t.Fatalf("%+v", in)
	}
	if len(in.Months) != TrendMonths || in.Months[11] != (MonthTotal{"2026-11", 4600000}) ||
		in.Months[5] != (MonthTotal{"2026-05", 3400000}) || in.Months[0].Amount != 0 {
		t.Fatal(in.Months)
	}
	if in.Categories[0].Category != "Rent" || in.Categories[0].Average != 3000000 ||
		len(in.Categories[0].Months) != TrendMonths || in.Categories[0].Months[10] != 3000000 {
		t.Fatalf("%+v", in.Categories[0])
	}
	if len(in.Daily) != 30 || in.Daily[7] != 1200000 || len(in.PreviousDaily) != 31 || in.PreviousDaily[9] != 400000 {
		t.Fatal(in.Daily, in.PreviousDaily)
	}
	if len(in.Largest) != 4 || in.Largest[0].Merchant != "EXAMPLE LANDLORD" || in.Largest[1].Category != "Games" {
		t.Fatalf("%+v", in.Largest)
	}
	got := []string{}
	for _, i := range in.Insights {
		got = append(got, i.Tone+": "+i.Text)
	}
	want := []string{
		"up: On pace for ₹54,000 this month, 59% above your 6-month average (₹34,000).",
		"up: Games is new: ₹12,000 this month, nothing in the 6 months before.",
		"info: Largest single spend: ₹30,000 on 1 Nov, EXAMPLE LANDLORD (Rent, 65% of the month).",
		"info: ₹3,000 (7%) is uncategorised, so these numbers are rougher than they could be.",
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("insights:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}

	// A finished month: no projection, and categories down on their usual.
	december := time.Date(2026, 12, 15, 12, 0, 0, 0, time.Local)
	in, _ = s.MonthInsights("2026-11", december)
	if in.Current || in.Projected != 0 || in.Days != 30 {
		t.Fatalf("%+v", in)
	}
	got = got[:0]
	for _, i := range in.Insights {
		got = append(got, i.Tone+": "+i.Text)
	}
	if got[0] != "up: Spent ₹46,000 this month, 35% above your 6-month average (₹34,000)." ||
		got[2] != "down: Food delivery: ₹1,000, 75% below its usual ₹4,000." {
		t.Fatal(strings.Join(got, "\n"))
	}
	// A category well above a usual it has: how many times over.
	mail("20,000.00", "EXAMPLE FOOD", "2026-11-20")
	in, _ = s.MonthInsights("2026-11", december)
	found := false
	for _, i := range in.Insights {
		found = found || i.Text == "Food delivery: ₹21,000, 5.2× its usual ₹4,000."
	}
	if !found {
		t.Fatal(in.Insights)
	}

	// The first month Ledger has: no baseline, so no comparisons.
	in, _ = s.MonthInsights("2026-05", december)
	if in.BaselineCount != 0 || in.Average != 0 || in.Insights[0].Text[:7] != "Largest" {
		t.Fatalf("%+v", in.Insights)
	}
}

func TestInsightsTakeRefundsOffTheirCategory(t *testing.T) {
	s := testStore(t)
	out := fixture.AlertParser()
	out.Issuer, out.AccountKind, out.Direction = "HDFC", "card", "debit"
	out, _ = s.SaveParser(out)
	in := fixture.AlertParser()
	in.Name, in.Issuer, in.AccountKind, in.Direction = "In", "HDFC", "card", "credit"
	in.Sender = "in@example.invalid"
	in, _ = s.SaveParser(in)
	n := 0
	mail := func(p alerts.Parser, amount, merchant, date string) {
		n++
		raw := strings.Replace(string(fixture.Mail(fmt.Sprintf("r%d", n),
			"INR "+amount+" at "+merchant+" card 4242 on "+date)), "alerts@example.invalid", p.Sender, 1)
		if _, err := s.Ingest([]byte(raw)); err != nil {
			t.Fatal(err)
		}
	}
	mail(out, "1,000.00", "EXAMPLE FOOD", "2026-11-02")
	mail(in, "400.00", "EXAMPLE FOOD REFUND", "2026-11-03")
	mail(out, "3,540.00", "ANNUAL FEE", "2026-11-04")
	mail(in, "5,000.00", "ANNUAL FEE REVERSAL", "2026-11-05")
	mail(in, "50,000.00", "SALARY", "2026-11-06")
	cats, _ := s.Categories()
	id := map[string]int64{}
	for _, c := range cats {
		id[c.Name] = c.ID
	}
	s.SaveCategoryRule(CategoryRule{Contains: "EXAMPLE FOOD", CategoryID: id["Food delivery"]})
	s.SaveCategoryRule(CategoryRule{Contains: "ANNUAL FEE", CategoryID: id["Fees & charges"]})

	got, err := s.MonthInsights("2026-11", time.Date(2026, 12, 1, 0, 0, 0, 0, time.Local))
	if err != nil {
		t.Fatal(err)
	}
	// Food: 1,000 - 400. Fees: 3,540 - 5,000, but never below zero. The
	// uncategorised salary isn't spending at all.
	if got.Total != 60000 || got.Refunds != 540000 || got.Count != 2 {
		t.Fatalf("%+v", got)
	}
	amounts := map[string][2]int64{}
	for _, c := range got.Categories {
		amounts[c.Category] = [2]int64{c.Amount, c.Refunds}
	}
	if amounts["Food delivery"] != [2]int64{60000, 40000} || amounts["Fees & charges"] != [2]int64{0, 500000} {
		t.Fatal(amounts)
	}
	if got.Daily[1] != 100000 || got.Daily[2] != -40000 || got.Daily[5] != 0 {
		t.Fatal(got.Daily)
	}
}
