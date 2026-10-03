package statements

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
)

// The IDFC FIRST Bank credit card layout: a summary of opening balance,
// purchases, EMI & other debits, payments & refunds and total due (CR when
// in credit), then "dd Mon yy" lines whose details may wrap onto the lines
// around them. pdftotext extracts the ₹ glyph as "r".
var (
	idfcCard    = regexp.MustCompile(`Card Number:\s*X+\s*(\d{4})|\(FIRST [^)]*XX(\d{4})\)`)
	idfcDate    = regexp.MustCompile(`\d{2}/[A-Za-z]{3}/\d{4}`)
	idfcAmount  = regexp.MustCompile(`[r₹]([\d,]+\.\d{2})(?:\s+(CR|DR)\b)?`)
	idfcRow     = regexp.MustCompile(`^\s*(\d{2} [A-Za-z]{3} \d{2})\s.*?([\d,]+\.\d{2})\s+(DR|CR)\s*$`)
	idfcDatedAt = regexp.MustCompile(`^\s*\d{2} [A-Za-z]{3} \d{2}\s`)
	idfcSummary = map[string]*regexp.Regexp{
		"opening":   regexp.MustCompile(`^\s*Opening Balance\s+[r₹]([\d,]+\.\d{2})(?:\s+(CR|DR)\b)?`),
		"purchases": regexp.MustCompile(`^\s*Purchases\s+\+\s+[r₹]([\d,]+\.\d{2})()`),
		"debits":    regexp.MustCompile(`^\s*EMI & Other Debits\s+\+\s+[r₹]([\d,]+\.\d{2})()`),
		"payments":  regexp.MustCompile(`^\s*Payments & Refunds\s+-\s+[r₹]([\d,]+\.\d{2})()`),
		"total":     regexp.MustCompile(`^\s*Total Amount Due\s+=\s+[r₹]([\d,]+\.\d{2})(?:\s+(CR|DR)\b)?`),
	}
)

// parseIDFC reads an IDFC FIRST Bank credit card statement.
func parseIDFC(t Text, tolerance int64) (Statement, error) {
	s := newStatement("IDFC", "card")
	text := t.Layout
	// A month with no transactions has no transaction section at all.
	if !strings.Contains(text, "IDFC FIRST") || !strings.Contains(text, "Statement Summary") {
		return s, errors.New("unsupported statement layout; expected an IDFC FIRST Bank credit card statement")
	}
	for _, m := range idfcCard.FindAllStringSubmatch(text, -1) {
		if err := setAccount(&s, m[1]+m[2]); err != nil {
			return s, err
		}
	}
	if s.Account == "" {
		return s, errors.New("masked card number not found")
	}

	lines := strings.Split(text, "\n")
	found := map[string]int64{}
	table := -1
	var err error
	for i, l := range lines {
		for name, re := range idfcSummary {
			if _, seen := found[name]; seen {
				continue
			}
			if m := re.FindStringSubmatch(l); m != nil {
				v, e := money(m[1])
				if e != nil {
					return s, e
				}
				found[name] = signed(v, m[2])
			}
		}
		switch {
		case s.DueDate == "" && strings.Contains(l, "Payment Due Date") && strings.Contains(l, "Statement Period"):
			values := within(lines, i, 3, idfcDate, 3)
			amounts := idfcAmount.FindAllStringSubmatch(values, -1)
			if values == "" || len(amounts) < 2 {
				return s, errors.New("payment due date and minimum due not found")
			}
			if s.DueDate, err = dayOnly("02/Jan/2006", idfcDate.FindString(values)); err != nil {
				return s, errors.New("invalid payment due date")
			}
			if s.MinimumDue, err = money(amounts[1][1]); err != nil {
				return s, err
			}
		case s.Date == "" && strings.Contains(l, "Statement Date:"):
			if s.Date, err = dayOnly("02/Jan/2006", idfcDate.FindString(within(lines, i, 2, idfcDate, 1))); err != nil {
				return s, errors.New("invalid statement date")
			}
		case table < 0 && strings.Contains(l, "Transaction Details") && strings.Contains(l, "Amount"):
			table = i
		}
	}
	if len(found) != len(idfcSummary) || s.Date == "" || s.DueDate == "" {
		return s, errors.New("statement summary, dates or transaction table not found")
	}
	s.Opening, s.TotalDue = found["opening"], found["total"]
	s.Purchases, s.Payments = found["purchases"]+found["debits"], found["payments"]

	details, emi := -10, -1
	if table >= 0 {
		details, emi = column(lines[table], "Transaction Details"), column(lines[table], "EMI")
	}
	var debits, credits int64
	var rowErrors []error
	rows, onRow, extra := []int{}, []string{}, map[int]string{}
	for i, l := range lines {
		if !idfcDatedAt.MatchString(l) {
			if at := indent(l); i > table && at >= details-1 && at <= details+1 {
				extra[i] = cell(l, details, emi)
			}
			continue
		}
		if table < 0 || i < table || idfcRow.FindStringSubmatch(l) == nil {
			rowErrors = append(rowErrors, fmt.Errorf("unparsed transaction on line %d", i+1))
			continue
		}
		rows = append(rows, i)
		onRow = append(onRow, cell(l, details, emi))
	}
	if len(rows) == 0 {
		return finish(&s, 0, 0, tolerance, rowErrors)
	}
	merchants, err := wrapped(rows, extra, onRow, " ")
	if err != nil {
		return s, err
	}
	for n, i := range rows {
		m := idfcRow.FindStringSubmatch(lines[i])
		date, e := dateIn("02 Jan 06", m[1])
		amount, e2 := money(m[2])
		if e != nil || e2 != nil || amount == 0 {
			rowErrors = append(rowErrors, fmt.Errorf("invalid date or amount on line %d", i+1))
			continue
		}
		credit := m[3] == "CR"
		if credit {
			credits += amount
		} else {
			debits += amount
		}
		line(&s, date, merchants[n], amount, credit, "")
	}
	return finish(&s, debits, credits, tolerance, rowErrors)
}
