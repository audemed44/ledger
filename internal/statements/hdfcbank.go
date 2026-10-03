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
// Every row's running balance is checked. Statements downloaded from
// NetBanking print the same table differently; see parseHDFCNetBanking.
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
	if !strings.Contains(text, "HDFC BANK LIMITED") || !strings.Contains(text, "STATEMENT SUMMARY") {
		return s, errors.New("unsupported statement layout; expected an HDFC Bank account statement")
	}
	if strings.Contains(text, "Withdrawal Amt.") && strings.Contains(text, "Statement of account") {
		return parseHDFCNetBanking(s, text, tolerance)
	}
	if !strings.Contains(text, "Withdrawal Amount") {
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
		if err := follow(&s, balance, running, tolerance, i+1); err != nil {
			rowErrors = append(rowErrors, err)
		}
		balance = running
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

// The NetBanking download: two-digit years, only the withdrawal or the
// deposit printed on each row (which one is told by its column), the
// column heads on the first page only, and narrations wrapped onto the
// lines below their row. A continuation line one column left of the row's
// narration starts a new word; one in line with it carries on mid-word.
var (
	hdfcNetAccount = regexp.MustCompile(`Account No\s*:\s*\d*(\d{4})\b`)
	hdfcNetPeriod  = regexp.MustCompile(`From\s*:\s*\d{2}/\d{2}/\d{4}\s+To\s*:\s*(\d{2}/\d{2}/\d{4})`)
	hdfcNetRow     = regexp.MustCompile(`^\s?(\d{2}/\d{2}/\d{2})\s+(\S.*?)\s+(\S+)\s+\d{2}/\d{2}/\d{2}\s+([\d,]+\.\d{2})\s+([\d,]+\.\d{2})\s*$`)
	hdfcNetDatedAt = regexp.MustCompile(`^\s?\d{2}/\d{2}/\d{2}\s`)
)

func parseHDFCNetBanking(s Statement, text string, tolerance int64) (Statement, error) {
	for _, m := range hdfcNetAccount.FindAllStringSubmatch(text, -1) {
		if err := setAccount(&s, m[1]); err != nil {
			return s, err
		}
	}
	if s.Account == "" {
		return s, errors.New("account number not found")
	}
	m := hdfcNetPeriod.FindStringSubmatch(text)
	if m == nil {
		return s, errors.New("statement period not found")
	}
	var err error
	if s.Date, err = dayOnly("02/01/2006", m[1]); err != nil {
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

	// Amounts are right-aligned a little past their heads: one ending
	// before the midpoint of the withdrawal and deposit heads' ends is a
	// withdrawal, and the balance ends past the deposit and balance heads'.
	end := func(label string) int {
		if at := column(lines[head], label); at >= 0 {
			return at + len(label)
		}
		return -1
	}
	withdrawal, deposit, closing := end("Withdrawal Amt."), end("Deposit Amt."), end("Closing Balance")
	if withdrawal < 0 || deposit < withdrawal || closing < deposit {
		return s, errors.New("transaction table columns not found")
	}
	split, balanceFrom := (withdrawal+deposit)/2, (deposit+closing)/2

	var rowErrors []error
	balance := s.Opening
	var debits, credits int64
	debitRows, creditRows := 0, 0
	narration := -1
	var merchant []string
	add := func() {}
	for i := head + 1; i < summary; i++ {
		l := lines[i]
		if !hdfcNetDatedAt.MatchString(l) {
			// Lines between pages sit in other columns.
			if at := indent(l); narration > 0 && merchant != nil && (at == narration || at == narration-1) {
				sep := ""
				if at < narration {
					sep = " "
				}
				merchant = append(merchant, sep+strings.TrimSpace(l))
			}
			continue
		}
		add()
		add, merchant = func() {}, nil
		m := hdfcNetRow.FindStringSubmatch(l)
		if m == nil {
			rowErrors = append(rowErrors, fmt.Errorf("unparsed transaction on line %d", i+1))
			continue
		}
		r := []rune(l)
		narration = len([]rune(l[:strings.Index(l, m[2])]))
		amountEnd := len([]rune(l[:strings.LastIndex(l, m[4]+" ")+len(m[4])]))
		balanceEnd := len(r) - (len(l) - len(strings.TrimRight(l, " ")))
		date, e := dateIn("02/01/06", m[1])
		amount, e2 := money(m[4])
		running, e3 := money(m[5])
		if e != nil || e2 != nil || e3 != nil || amount == 0 || balanceEnd <= balanceFrom || amountEnd > balanceFrom {
			rowErrors = append(rowErrors, fmt.Errorf("invalid date or amounts on line %d", i+1))
			continue
		}
		credit := amountEnd > split
		if credit {
			balance += amount
			creditRows++
			credits += amount
		} else {
			balance -= amount
			debitRows++
			debits += amount
		}
		if err := follow(&s, balance, running, tolerance, i+1); err != nil {
			rowErrors = append(rowErrors, err)
		}
		balance = running
		merchant = []string{m[2]}
		reference := m[3]
		add = func() { line(&s, date, strings.Join(merchant, ""), amount, credit, reference) }
	}
	add()
	if debitRows != debitCount || creditRows != creditCount {
		rowErrors = append(rowErrors, errors.New("the number of lines does not match the summary's debit and credit counts"))
	}
	return finish(&s, debits, credits, tolerance, rowErrors)
}
