package statements

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
)

// The ICICI Bank credit card layout. Its summary reads well in the layout
// text, but the transaction amounts drop out of it, so the lines come from
// the raw text: "date serial details reward-points amount [CR]".
var (
	iciciCard   = regexp.MustCompile(`\d{4}X{8}(\d{4})`)
	iciciLong   = regexp.MustCompile(`[A-Z][a-z]+ \d{1,2}, \d{4}`)
	iciciAmount = regexp.MustCompile("`\\s*([\\d,]+\\.\\d{2})(?:\\s+(CR|Cr)\\b)?")
	iciciRow    = regexp.MustCompile(`^(\d{2}/\d{2}/\d{4}) (\d{6,}) (.+) (-?\d+) ([\d,]+\.\d{2})( CR)?$`)
	iciciDated  = regexp.MustCompile(`^\d{2}/\d{2}/\d{4} \d{6,} `)
)

// parseICICI reads an ICICI Bank credit card statement.
func parseICICI(t Text, tolerance int64) (Statement, error) {
	s := newStatement("ICICI", "card")
	if !strings.Contains(t.Layout, "STATEMENT SUMMARY") || !strings.Contains(t.Layout, "icicibank") ||
		!strings.Contains(t.Layout, "Purchases / Charges") {
		return s, errors.New("unsupported statement layout; expected an ICICI Bank credit card statement")
	}
	if strings.TrimSpace(t.Raw) == "" {
		return s, errors.New("the ICICI layout needs the PDF's raw text; extract the PDF again")
	}
	for _, m := range iciciCard.FindAllStringSubmatch(t.Raw, -1) {
		if err := setAccount(&s, m[1]); err != nil {
			return s, err
		}
	}
	if s.Account == "" {
		return s, errors.New("masked card number not found")
	}

	lines := strings.Split(t.Layout, "\n")
	var err error
	for i, l := range lines {
		switch {
		case s.Date == "" && strings.Contains(l, "STATEMENT DATE"):
			if s.Date, err = dayOnly("January 2, 2006", iciciLong.FindString(within(lines, i, 3, iciciLong, 1))); err != nil {
				return s, errors.New("invalid statement date")
			}
		case s.DueDate == "" && strings.Contains(l, "PAYMENT DUE DATE"):
			if s.DueDate, err = dayOnly("January 2, 2006", iciciLong.FindString(within(lines, i, 3, iciciLong, 1))); err != nil {
				return s, errors.New("invalid payment due date")
			}
		case strings.Contains(l, "Total Amount due") && strings.Contains(l, "Previous Balance"):
			// Total due = previous + purchases + cash advances - payments.
			figures := iciciAmount.FindAllStringSubmatch(within(lines, i, 4, iciciAmount, 5), -1)
			if len(figures) != 5 {
				return s, errors.New("expected all five statement summary amounts")
			}
			amounts := make([]int64, 5)
			for n, f := range figures {
				if amounts[n], err = money(f[1]); err != nil {
					return s, err
				}
			}
			s.TotalDue = signed(amounts[0], figures[0][2])
			s.Opening = signed(amounts[1], figures[1][2])
			s.Purchases = amounts[2] + amounts[3]
			s.Payments = amounts[4]
		case strings.Contains(l, "Minimum Amount due"):
			if m := iciciAmount.FindStringSubmatch(within(lines, i, 2, iciciAmount, 1)); m != nil {
				if s.MinimumDue, err = money(m[1]); err != nil {
					return s, err
				}
			}
		}
	}
	if s.Date == "" || s.DueDate == "" || (s.TotalDue == 0 && s.Opening == 0 && s.Purchases == 0 && s.Payments == 0) {
		return s, errors.New("statement date, due date or summary not found")
	}

	var debits, credits int64
	var rowErrors []error
	raw := strings.Split(t.Raw, "\n")
	for i, l := range raw {
		l = strings.TrimSpace(l)
		if !iciciDated.MatchString(l) {
			continue
		}
		m := iciciRow.FindStringSubmatch(l)
		// A long merchant wraps the row onto the next lines.
		for j := i + 1; m == nil && j < min(i+4, len(raw)) && !iciciDated.MatchString(strings.TrimSpace(raw[j])); j++ {
			l += " " + strings.TrimSpace(raw[j])
			m = iciciRow.FindStringSubmatch(l)
		}
		if m == nil {
			rowErrors = append(rowErrors, fmt.Errorf("unparsed transaction on raw line %d", i+1))
			continue
		}
		date, e := dateIn("02/01/2006", m[1])
		amount, e2 := money(m[5])
		if e != nil || e2 != nil || amount == 0 {
			rowErrors = append(rowErrors, fmt.Errorf("invalid date or amount on raw line %d", i+1))
			continue
		}
		credit := m[6] != ""
		if credit {
			credits += amount
		} else {
			debits += amount
		}
		line(&s, date, m[3], amount, credit, "")
	}
	return finish(&s, debits, credits, tolerance, rowErrors)
}
