package statements

import (
	"errors"
	"fmt"
	"math"
	"regexp"
	"strings"
)

// The SBI savings account layout: a "TRANSACTION DETAILS" section naming the
// account, then a "TRANSACTION OVERVIEW" table: the opening balance, rows of
// date, details, reference, credit, debit and running balance ("0" for an
// empty column), and the closing balance. Every row's running balance is
// checked.
var (
	sbiAccount = regexp.MustCompile(`^\s*X{4,}(\d{4})\s*$`)
	sbiOpening = regexp.MustCompile(`Balance on (\d{2}-\d{2}-\d{2}):\s+([\d,]+\.\d{2})`)
	sbiClosing = regexp.MustCompile(`Closing Balance on (\d{2}-\d{2}-\d{2}):\s+([\d,]+\.\d{2})`)
	sbiRow     = regexp.MustCompile(`^\s*(\d{2}-\d{2}-\d{2})(\s+.*?)\s(\S+)\s+([\d,]+(?:\.\d{2})?)\s+([\d,]+(?:\.\d{2})?)\s+([\d,]+\.\d{2})\s*$`)
	sbiDatedAt = regexp.MustCompile(`^\s*\d{2}-\d{2}-\d{2}\s`)
)

// parseSBI reads an SBI savings account statement.
func parseSBI(t Text, tolerance int64) (Statement, error) {
	s := newStatement("SBI", "bank")
	text := t.Layout
	if !strings.Contains(text, "sbi.co.in") || strings.Count(text, "TRANSACTION OVERVIEW") != 1 {
		return s, errors.New("unsupported statement layout; expected an SBI statement with one account's transactions")
	}
	lines := strings.Split(text, "\n")
	details, overview, closing := -1, -1, -1
	for i, l := range lines {
		switch strings.TrimSpace(l) {
		case "TRANSACTION DETAILS":
			details = i
		case "TRANSACTION OVERVIEW":
			overview = i
		}
		if overview >= 0 && closing < 0 && sbiClosing.MatchString(l) {
			closing = i
		}
	}
	if details < 0 || overview < details || closing < 0 {
		return s, errors.New("transaction overview or closing balance not found")
	}
	for _, l := range lines[details:overview] {
		if m := sbiAccount.FindStringSubmatch(l); m != nil {
			if err := setAccount(&s, m[1]); err != nil {
				return s, err
			}
		}
	}
	if s.Account == "" {
		return s, errors.New("masked account number not found")
	}

	var err error
	m := sbiOpening.FindStringSubmatch(strings.Join(lines[overview:closing], "\n"))
	if m == nil {
		return s, errors.New("opening balance not found")
	}
	if s.Opening, err = money(m[2]); err != nil {
		return s, err
	}
	m = sbiClosing.FindStringSubmatch(lines[closing])
	if s.TotalDue, err = money(m[2]); err != nil {
		return s, err
	}
	if s.Date, err = dayOnly("02-01-06", m[1]); err != nil {
		return s, errors.New("invalid closing balance date")
	}

	// Details start where the rows' text after the date starts furthest left.
	var rowErrors []error
	rows, onRow, extra := []int{}, []string{}, map[int]string{}
	start := math.MaxInt
	for i := overview + 1; i < closing; i++ {
		l := lines[i]
		if !sbiDatedAt.MatchString(l) {
			continue
		}
		at := sbiRow.FindStringSubmatchIndex(l)
		if at == nil {
			rowErrors = append(rowErrors, fmt.Errorf("unparsed transaction on line %d", i+1))
			continue
		}
		middle := l[at[4]:at[5]]
		start = min(start, len([]rune(l[:at[4]]))+len([]rune(middle))-len([]rune(strings.TrimLeft(middle, " "))))
		rows, onRow = append(rows, i), append(onRow, strings.TrimSpace(middle))
	}
	for i := overview + 1; i < closing; i++ {
		if at := indent(lines[i]); !sbiDatedAt.MatchString(lines[i]) && at >= start-1 && at <= start+1 {
			extra[i] = strings.TrimSpace(gap.Split(strings.TrimSpace(lines[i]), 2)[0])
		}
	}
	if len(rows) == 0 {
		return finish(&s, 0, 0, tolerance, rowErrors)
	}
	merchants, err := wrapped(rows, extra, onRow, " ")
	if err != nil {
		return s, err
	}
	balance := s.Opening
	var debits, credits int64
	for n, i := range rows {
		m := sbiRow.FindStringSubmatch(lines[i])
		date, e := dateIn("02-01-06", m[1])
		credit, e2 := money(m[4])
		debit, e3 := money(m[5])
		running, e4 := money(m[6])
		if e != nil || e2 != nil || e3 != nil || e4 != nil || (credit == 0) == (debit == 0) {
			rowErrors = append(rowErrors, fmt.Errorf("invalid date or amounts on line %d", i+1))
			continue
		}
		balance += credit - debit
		if balance != running {
			rowErrors = append(rowErrors, fmt.Errorf("running balance doesn't follow on line %d", i+1))
			balance = running
		}
		reference := m[3]
		if reference == "-" {
			reference = ""
		}
		credits += credit
		debits += debit
		line(&s, date, merchants[n], credit+debit, credit > 0, reference)
	}
	s.Purchases, s.Payments = debits, credits
	return finish(&s, debits, credits, tolerance, rowErrors)
}
