package statements

import (
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// The HDFC Bank savings account layout: rows of date, narration, reference,
// value date, withdrawal, deposit and closing balance, with narrations
// wrapped (mid-word) onto the lines around their row, then a summary of
// opening balance, debit and credit counts and totals, and closing balance.
// Every row's running balance is checked.
var (
	hdfcBankAccount = regexp.MustCompile(`Account number\s*:\s*\d*(\d{4})\b`)
	hdfcBankPeriod  = regexp.MustCompile(`Statement From\s*:\s*\d{2}/\d{2}/\d{2}\s+TO\s*:\s*(\d{2}/\d{2}/\d{2})`)
	hdfcBankRow     = regexp.MustCompile(`^(\d{2}/\d{2}/\d{4})\s.*\s(\d{2}/\d{2}/\d{2,4})\s+([\d,]+\.\d{2})\s+([\d,]+\.\d{2})\s+([\d,]+\.\d{2})\s*$`)
	hdfcBankDatedAt = regexp.MustCompile(`^\d{2}/\d{2}/\d{4}\s`)
)

// parseHDFCBank reads an HDFC Bank savings account statement.
func parseHDFCBank(t Text, tolerance int64) (Statement, error) {
	s := newStatement("HDFC", "bank")
	text := t.Layout
	if !strings.Contains(text, "HDFC BANK LIMITED") || !strings.Contains(text, "Withdrawal Amount") ||
		!strings.Contains(text, "STATEMENT SUMMARY") {
		return s, errors.New("unsupported statement layout; expected an HDFC Bank account statement")
	}
	for _, m := range hdfcBankAccount.FindAllStringSubmatch(text, -1) {
		if err := setAccount(&s, m[1]); err != nil {
			return s, err
		}
	}
	if s.Account == "" {
		return s, errors.New("account number not found")
	}
	m := hdfcBankPeriod.FindStringSubmatch(text)
	if m == nil {
		return s, errors.New("statement period not found")
	}
	var err error
	if s.Date, err = dayOnly("02/01/06", m[1]); err != nil {
		return s, errors.New("invalid statement end date")
	}

	lines := strings.Split(text, "\n")
	head, summary := -1, -1
	for i, l := range lines {
		if head < 0 && strings.Contains(l, "Narration") && strings.Contains(l, "Closing Balance") {
			head = i
		}
		if strings.Contains(l, "STATEMENT SUMMARY") {
			summary = i
		}
	}
	if head < 0 || summary < head {
		return s, errors.New("transaction table or statement summary not found")
	}
	// Opening balance, debit count, credit count, debits, credits, closing.
	figures := strings.Fields(within(lines, summary+1, 3, amountPattern, 4))
	if len(figures) != 6 {
		return s, errors.New("expected all six statement summary figures")
	}
	debitCount, e1 := strconv.Atoi(figures[1])
	creditCount, e2 := strconv.Atoi(figures[2])
	if e1 != nil || e2 != nil {
		return s, errors.New("invalid statement summary counts")
	}
	values := []*int64{&s.Opening, &s.Purchases, &s.Payments, &s.TotalDue}
	for i, f := range []string{figures[0], figures[3], figures[4], figures[5]} {
		if *values[i], err = money(f); err != nil {
			return s, err
		}
	}

	narration := column(lines[head], "Narration")
	reference := column(lines[head], "Chq. / Ref No.")
	// The narration keeps its leading space, if any: wraps split words.
	narrationAt := func(l string) string {
		r := []rune(l)
		if narration >= len(r) {
			return ""
		}
		return strings.TrimRight(string(r[narration:min(reference, len(r))]), " ")
	}
	var rowErrors []error
	rows, onRow, extra := []int{}, []string{}, map[int]string{}
	for i := head + 1; i < summary; i++ {
		l := lines[i]
		if !hdfcBankDatedAt.MatchString(l) {
			if at := indent(l); at >= narration && at <= narration+1 {
				extra[i] = narrationAt(l)
			}
			continue
		}
		if !hdfcBankRow.MatchString(l) {
			rowErrors = append(rowErrors, fmt.Errorf("unparsed transaction on line %d", i+1))
			continue
		}
		rows, onRow = append(rows, i), append(onRow, narrationAt(l))
	}
	if len(rows) == 0 {
		return finish(&s, 0, 0, tolerance, rowErrors)
	}
	narrations, err := wrapped(rows, extra, onRow, "")
	if err != nil {
		return s, err
	}
	balance := s.Opening
	var debits, credits int64
	debitRows, creditRows := 0, 0
	for n, i := range rows {
		m := hdfcBankRow.FindStringSubmatch(lines[i])
		date, e := dateIn("02/01/2006", m[1])
		debit, e2 := money(m[3])
		credit, e3 := money(m[4])
		running, e4 := money(m[5])
		if e != nil || e2 != nil || e3 != nil || e4 != nil || (credit == 0) == (debit == 0) {
			rowErrors = append(rowErrors, fmt.Errorf("invalid date or amounts on line %d", i+1))
			continue
		}
		balance += credit - debit
		if balance != running {
			rowErrors = append(rowErrors, fmt.Errorf("running balance doesn't follow on line %d", i+1))
			balance = running
		}
		if credit > 0 {
			creditRows++
		} else {
			debitRows++
		}
		credits += credit
		debits += debit
		line(&s, date, narrations[n], credit+debit, credit > 0, cell(lines[i], reference, column(lines[head], "Value Date")))
	}
	if debitRows != debitCount || creditRows != creditCount {
		rowErrors = append(rowErrors, errors.New("the number of lines does not match the summary's debit and credit counts"))
	}
	return finish(&s, debits, credits, tolerance, rowErrors)
}
