package statements

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"
)

// The HSBC India credit card layout: a payment summary (due date and
// minimum due, then the statement period and total due), the primary card
// and account numbers, and on each page a DATE / TRANSACTION DETAILS /
// AMOUNTS table followed by an ACCOUNT SUMMARY of opening balance, purchases
// and charges, payments and credits, and net outstanding. Lines are dated
// "19OCT", with no year; credits end in CR. The ₹ glyph doesn't extract.
var (
	hsbcCard    = regexp.MustCompile(`\d{2}xx xxxx xxxx (\d{4})`)
	hsbcDate    = regexp.MustCompile(`\d{2} [A-Z]{3} \d{4}`)
	hsbcAmount  = regexp.MustCompile(`([\d,]+\.\d{2})(?:\s*(CR|DR)\b)?`)
	hsbcPeriod  = regexp.MustCompile(`(\d{2} [A-Z]{3} \d{4}) To (\d{2} [A-Z]{3} \d{4})`)
	hsbcRow     = regexp.MustCompile(`^\s*(\d{2}[A-Z]{3})\s+(.*?)\s{2,}([\d,]+\.\d{2})(?:\s*(CR))?\s*$`)
	hsbcDatedAt = regexp.MustCompile(`^\s*\d{2}[A-Z]{3}\s`)
	hsbcEndsIn  = regexp.MustCompile(`[\d,]+\.\d{2}(?:\s*CR)?\s*$`)
	// Table lines that carry an amount but aren't transactions.
	hsbcBalance = regexp.MustCompile(`^\s*(?:\d{2}[A-Z]{3}\s+)?(?:Available Credit Limit|OPENING BALANCE|TOTAL [A-Z ]+ OUTSTANDING|NET OUTSTANDING BALANCE)\s`)
)

// parseHSBC reads an HSBC India credit card statement.
func parseHSBC(t Text, tolerance int64) (Statement, error) {
	s := newStatement("HSBC", "card")
	text := t.Layout
	if !strings.Contains(text, "Hongkong and Shanghai Banking Corporation") || !strings.Contains(text, "PAYMENT SUMMARY") ||
		!strings.Contains(text, "ACCOUNT SUMMARY") {
		return s, errors.New("unsupported statement layout; expected an HSBC credit card statement")
	}
	lines := strings.Split(text, "\n")

	// Later pages repeat the payment summary's labels without values, so
	// the first labels with values below them count.
	var start, end time.Time
	var err error
	summary := false
	for i, l := range lines {
		switch {
		case s.DueDate == "" && strings.Contains(l, "Payment due date") && strings.Contains(l, "Minimal payment due"):
			values := within(lines, i, 2, hsbcDate, 1)
			if values == "" {
				continue
			}
			amounts := hsbcAmount.FindAllString(hsbcDate.ReplaceAllString(values, ""), -1)
			if len(amounts) != 1 {
				return s, errors.New("minimum payment due not found")
			}
			if s.DueDate, err = dayOnly("02 Jan 2006", hsbcDate.FindString(values)); err != nil {
				return s, errors.New("invalid payment due date")
			}
			if s.MinimumDue, err = money(amounts[0]); err != nil {
				return s, err
			}
		case s.Date == "" && strings.Contains(l, "Statement period"):
			m := hsbcPeriod.FindStringSubmatch(within(lines, i, 2, hsbcPeriod, 1))
			if m == nil {
				continue
			}
			start, err = dateIn("02 Jan 2006", m[1])
			if err == nil {
				end, err = dateIn("02 Jan 2006", m[2])
			}
			if err != nil || end.Before(start) {
				return s, errors.New("invalid statement period")
			}
			s.Date = end.Format("2006-01-02")
		case s.Account == "" && strings.Contains(l, "Primary card number"):
			// The primary card, then the account number.
			if m := hsbcCard.FindStringSubmatch(within(lines, i, 3, hsbcCard, 2)); m != nil {
				if err := setAccount(&s, m[1]); err != nil {
					return s, err
				}
			}
		case !summary && strings.Contains(l, "Opening balance") && strings.Contains(l, "Net Outstanding balance"):
			// Opening, purchases and charges, payments and credits, net
			// outstanding.
			figures := hsbcAmount.FindAllStringSubmatch(within(lines, i, 2, hsbcAmount, 4), -1)
			if len(figures) != 4 {
				return s, errors.New("expected all four account summary amounts")
			}
			amounts := make([]int64, 4)
			for n, f := range figures {
				if amounts[n], err = money(f[1]); err != nil {
					return s, err
				}
			}
			s.Opening = signed(amounts[0], figures[0][2])
			s.Purchases, s.Payments = amounts[1], amounts[2]
			s.TotalDue = signed(amounts[3], figures[3][2])
			summary = true
		}
	}
	if s.Account == "" {
		return s, errors.New("masked card number not found")
	}
	if s.Date == "" || s.DueDate == "" || !summary {
		return s, errors.New("payment summary, statement period or account summary not found")
	}

	// Each page's table runs from its head to its account summary. Every
	// card number in it has to be the primary card's; every line with an
	// amount has to be a transaction or a known balance line.
	var debits, credits int64
	var rowErrors []error
	inTable := false
	for i, l := range lines {
		switch {
		case strings.Contains(l, "TRANSACTION DETAILS") && strings.Contains(l, "AMOUNTS"):
			inTable = true
			continue
		case strings.Contains(l, "ACCOUNT SUMMARY"):
			inTable = false
			continue
		}
		if !inTable {
			if hsbcDatedAt.MatchString(l) && hsbcEndsIn.MatchString(l) {
				rowErrors = append(rowErrors, fmt.Errorf("dated row outside the transaction table on line %d", i+1))
			}
			continue
		}
		for _, m := range hsbcCard.FindAllStringSubmatch(l, -1) {
			if m[1] != s.Account {
				return s, errAdditionalCard
			}
		}
		if hsbcBalance.MatchString(l) || (!hsbcDatedAt.MatchString(l) && !hsbcEndsIn.MatchString(l)) {
			continue
		}
		m := hsbcRow.FindStringSubmatch(l)
		if m == nil || !hsbcDatedAt.MatchString(l) {
			rowErrors = append(rowErrors, fmt.Errorf("unparsed transaction on line %d", i+1))
			continue
		}
		date, e := hsbcDay(m[1], start, end)
		amount, e2 := money(m[3])
		if e != nil || e2 != nil || amount == 0 {
			rowErrors = append(rowErrors, fmt.Errorf("invalid date or amount on line %d", i+1))
			continue
		}
		credit := m[4] == "CR"
		if credit {
			credits += amount
		} else {
			debits += amount
		}
		line(&s, date, m[2], amount, credit, "")
	}
	return finish(&s, debits, credits, tolerance, rowErrors)
}

// hsbcDay dates a "19OCT" line within the statement period, which can span
// a new year. A line can't be dated after the period ends or more than a
// month before it starts.
func hsbcDay(day string, start, end time.Time) (time.Time, error) {
	d, err := dateIn("02Jan2006", day[:2]+day[2:3]+strings.ToLower(day[3:])+fmt.Sprint(end.Year()))
	if err != nil {
		return d, err
	}
	if d.After(end) {
		d = d.AddDate(-1, 0, 0)
	}
	if d.Before(start.AddDate(0, -1, 0)) {
		return d, errors.New("line dated outside the statement period")
	}
	return d, nil
}
