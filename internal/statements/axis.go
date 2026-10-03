package statements

import (
	"errors"
	"fmt"
	"math"
	"regexp"
	"strings"
)

// The Axis Bank credit card layout: a payment summary (due amounts, period
// and dates), a "Previous Balance - Payments - Credits + Purchase + Cash
// Advance + Other Debit&Charges = Total Payment Due" row, then dated lines
// ending in Dr or Cr until "End of Statement".
var (
	axisCard    = regexp.MustCompile(`\d{6}\*{6}(\d{4})`)
	axisDate    = regexp.MustCompile(`\d{2}/\d{2}/\d{4}`)
	axisSigned  = regexp.MustCompile(`([\d,]+\.\d{2})(?:\s+(Dr|Cr)\b)?`)
	axisRow     = regexp.MustCompile(`^\s*(\d{2}/\d{2}/\d{4})(.*?)([\d,]+\.\d{2})\s+(Dr|Cr)\s*$`)
	axisDatedAt = regexp.MustCompile(`^\s*\d{2}/\d{2}/\d{4}\s`)
	gap         = regexp.MustCompile(`\s{2,}`)
)

// parseAxis reads an Axis Bank credit card statement.
func parseAxis(t Text, tolerance int64) (Statement, error) {
	s := newStatement("Axis", "card")
	text := t.Layout
	if !strings.Contains(text, "Axis Bank") || !strings.Contains(text, "PAYMENT SUMMARY") ||
		!strings.Contains(text, "Previous Balance - Payments - Credits") {
		return s, errors.New("unsupported statement layout; expected an Axis Bank credit card statement")
	}
	lines := strings.Split(text, "\n")
	for _, m := range axisCard.FindAllStringSubmatch(text, -1) {
		if err := setAccount(&s, m[1]); err != nil {
			return s, err
		}
	}
	if s.Account == "" {
		return s, errors.New("masked card number not found")
	}

	head, summary, table, end := -1, -1, -1, len(lines)
	for i, l := range lines {
		switch {
		case head < 0 && strings.Contains(l, "Total Payment Due") && strings.Contains(l, "Statement Generation Date"):
			head = i
		case summary < 0 && strings.Contains(l, "Previous Balance - Payments - Credits"):
			summary = i
		case table < 0 && strings.Contains(l, "TRANSACTION DETAILS") && strings.Contains(l, "AMOUNT"):
			table = i
		case table >= 0 && end == len(lines) && strings.Contains(l, "End of Statement"):
			end = i
		}
	}
	if head < 0 || summary < 0 || table < 0 {
		return s, errors.New("payment summary or transaction table not found")
	}

	// Due amounts, then the period, due date and generation date.
	values := within(lines, head, 4, axisDate, 4)
	dates := axisDate.FindAllString(values, -1)
	dues := axisSigned.FindAllStringSubmatch(values, -1)
	if len(dates) != 4 || len(dues) < 2 {
		return s, errors.New("payment summary values not found")
	}
	var err error
	if s.DueDate, err = dayOnly("02/01/2006", dates[2]); err != nil {
		return s, errors.New("invalid payment due date")
	}
	if s.Date, err = dayOnly("02/01/2006", dates[3]); err != nil {
		return s, errors.New("invalid statement date")
	}
	if s.MinimumDue, err = money(dues[1][1]); err != nil {
		return s, err
	}

	// Previous, payments, credits, purchases, cash, other charges, total.
	figures := axisSigned.FindAllStringSubmatch(within(lines, summary, 4, axisSigned, 7), -1)
	if len(figures) != 7 {
		return s, errors.New("expected all seven account summary amounts")
	}
	amounts := make([]int64, 7)
	for i, f := range figures {
		if amounts[i], err = money(f[1]); err != nil {
			return s, err
		}
	}
	s.Opening = signed(amounts[0], figures[0][2])
	s.Payments = amounts[1] + amounts[2]
	s.Purchases = amounts[3] + amounts[4] + amounts[5]
	s.TotalDue = signed(amounts[6], figures[6][2])

	// Details start where the rows' text after the date starts furthest
	// left (the header doesn't line up with them); a merchant category may
	// follow after a wide gap, or stand alone on a wrapped row.
	var debits, credits int64
	var rowErrors []error
	rows, starts, texts := []int{}, []int{}, []string{}
	details := math.MaxInt
	for i := table + 1; i < end; i++ {
		l := lines[i]
		if !axisDatedAt.MatchString(l) {
			continue
		}
		m := axisRow.FindStringSubmatchIndex(l)
		if m == nil {
			rowErrors = append(rowErrors, fmt.Errorf("unparsed transaction on line %d", i+1))
			continue
		}
		middle := l[m[4]:m[5]]
		start := len([]rune(l[:m[4]])) + len([]rune(middle)) - len([]rune(strings.TrimLeft(middle, " ")))
		rows, starts = append(rows, i), append(starts, start)
		texts = append(texts, strings.TrimSpace(gap.Split(strings.TrimSpace(middle), 2)[0]))
		if strings.TrimSpace(middle) != "" {
			details = min(details, start)
		}
	}
	onRow, extra := make([]string, len(rows)), map[int]string{}
	for n := range rows {
		if starts[n] <= details+1 {
			onRow[n] = texts[n]
		}
	}
	for i := table + 1; i < end; i++ {
		l := lines[i]
		if at := indent(l); !axisDatedAt.MatchString(l) && at >= details-1 && at <= details+1 && !strings.Contains(l, "Card No") {
			extra[i] = strings.TrimSpace(gap.Split(strings.TrimSpace(l), 2)[0])
		}
	}
	for i := 0; i < table; i++ {
		if axisRow.MatchString(lines[i]) {
			rowErrors = append(rowErrors, fmt.Errorf("dated row outside the transaction table on line %d", i+1))
		}
	}
	if len(rows) == 0 {
		return finish(&s, 0, 0, tolerance, rowErrors)
	}
	merchants, err := wrapped(rows, extra, onRow, " ")
	if err != nil {
		return s, err
	}
	for n, i := range rows {
		m := axisRow.FindStringSubmatch(lines[i])
		date, e := dateIn("02/01/2006", m[1])
		amount, e2 := money(m[3])
		if e != nil || e2 != nil || amount == 0 {
			rowErrors = append(rowErrors, fmt.Errorf("invalid date or amount on line %d", i+1))
			continue
		}
		credit := m[4] == "Cr"
		if credit {
			credits += amount
		} else {
			debits += amount
		}
		line(&s, date, merchants[n], amount, credit, "")
	}
	return finish(&s, debits, credits, tolerance, rowErrors)
}

// within is the first of the n lines after line i with exactly count
// matches of pattern, or "". Text from a neighbouring column can sit
// between a label row and its values.
func within(lines []string, i, n int, pattern *regexp.Regexp, count int) string {
	for _, l := range lines[i+1 : min(i+1+n, len(lines))] {
		if len(pattern.FindAllString(l, -1)) == count {
			return l
		}
	}
	return ""
}
