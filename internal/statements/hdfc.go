package statements

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/audemed44/ledger/internal/ledger"
)

// The HDFC credit card layout: a five-amount summary, then DATE & TIME
// transaction rows. pdftotext extracts the ₹ glyph as "C".
var (
	hdfcRow      = regexp.MustCompile(`^\s*(\d{2}/\d{2}/\d{4})\s*\|\s*(\d{2}:\d{2})(.*?)\s{2,}(\+?)\s*[C₹]\s*([\d,]+\.\d{2})\s*[A-Za-z]?\s*$`)
	hdfcGap      = regexp.MustCompile(`\s{3,}`)
	hdfcDatedRow = regexp.MustCompile(`^\s*\d{2}/\d{2}/\d{4}`)
	hdfcMoney    = regexp.MustCompile(`[C₹]\s*([\d,]+\.\d{2})`)
	hdfcDate     = regexp.MustCompile(`\d{2} [A-Za-z]{3}, \d{4}`)
	hdfcCardNo   = regexp.MustCompile(`\d[\dxX* ]*\d`)
	hdfcCardRows = regexp.MustCompile(`(?i)Card\s+No\s*:\s*([0-9xX*]+)`)
)

var errAdditionalCard = errors.New("statement contains an ambiguous or additional card; separate-account review is required")

// ParseHDFC parses an HDFC credit card statement with the default 99 paise
// rounding tolerance.
func ParseHDFC(text string) (Statement, error) {
	return ParseHDFCWithTolerance(text, 99)
}

// ParseHDFCWithTolerance parses an HDFC credit card statement. It fails
// closed: unknown layouts, unparsed dated rows and line totals that don't
// match the summary are errors. Rows it could read are still returned, for
// diagnosis. The final balance may differ from the summary by up to
// tolerance paise.
func ParseHDFCWithTolerance(text string, tolerance int64) (Statement, error) {
	if tolerance < 0 || tolerance > 99 {
		return Statement{}, errors.New("balance tolerance must be between 0 and 99 paise")
	}
	s := Statement{
		BalanceTolerancePaise: tolerance,
		Issuer:                "HDFC",
		AccountKind:           "card",
		Transactions:          []ledger.Transaction{},
		Warnings:              []string{},
	}
	if !strings.Contains(strings.ToLower(text), "hdfc") || !strings.Contains(text, "PREVIOUS STATEMENT DUES") {
		return s, errors.New("unsupported statement layout; expected HDFC credit card summary")
	}
	lines := strings.Split(text, "\n")
	summaryStart, summaryEnd, dueStart := -1, -1, -1
	for i, line := range lines {
		if strings.Contains(line, "PREVIOUS STATEMENT DUES") && strings.Contains(line, "TOTAL AMOUNT DUE") {
			summaryStart = i
		}
		if summaryStart >= 0 && summaryEnd < 0 && strings.Contains(line, "TOTAL CREDIT LIMIT") {
			summaryEnd = i
		}
		if strings.Contains(line, "MINIMUM DUE") && strings.Contains(line, "DUE DATE") {
			dueStart = i
		}
		if strings.Contains(line, "Statement Date") {
			if d := hdfcDate.FindString(line); d != "" {
				v, e := time.Parse("02 Jan, 2006", d)
				if e != nil {
					return s, errors.New("invalid statement date")
				}
				s.Date = v.Format("2006-01-02")
			}
		}
		if strings.Contains(line, "Credit Card No.") {
			digits := strings.TrimSpace(hdfcCardNo.FindString(strings.SplitN(line, "Credit Card No.", 2)[1]))
			if len(digits) >= 4 && ledger.LastFour.MatchString(digits[len(digits)-4:]) {
				if s.Account != "" && s.Account != digits[len(digits)-4:] {
					return s, errAdditionalCard
				}
				s.Account = digits[len(digits)-4:]
			}
		}
	}
	if s.Date == "" || s.Account == "" {
		return s, errors.New("statement date or masked account not found")
	}

	// Some statements contain supplementary cards. Don't silently assign all
	// rows to the primary card; this adapter supports one card only.
	for _, m := range hdfcCardRows.FindAllStringSubmatch(text, -1) {
		identifier := m[1]
		if len(identifier) < 4 || identifier[len(identifier)-4:] != s.Account {
			return s, errAdditionalCard
		}
	}
	s.AccountID = ledger.AccountKey(s.Issuer, s.AccountKind, s.Account)
	if summaryStart < 0 || summaryEnd <= summaryStart || summaryEnd-summaryStart > 12 {
		return s, errors.New("statement summary not found")
	}
	amounts := hdfcMoney.FindAllStringSubmatch(strings.Join(lines[summaryStart:summaryEnd], "\n"), -1)
	if len(amounts) != 5 {
		return s, errors.New("expected all five statement summary amounts")
	}
	values := []*int64{&s.Opening, &s.Payments, &s.Purchases, &s.FinanceCharges, &s.TotalDue}
	for i, a := range amounts {
		n, e := summaryAmount(a[1])
		if e != nil {
			return s, e
		}
		*values[i] = n
	}
	if dueStart >= 0 {
		for _, line := range lines[dueStart+1 : min(dueStart+7, len(lines))] {
			d := hdfcDate.FindString(line)
			a := hdfcMoney.FindStringSubmatch(line)
			if d == "" || a == nil {
				continue
			}
			v, e := time.Parse("02 Jan, 2006", d)
			if e != nil {
				return s, errors.New("invalid payment due date")
			}
			s.DueDate = v.Format("2006-01-02")
			if s.MinimumDue, e = summaryAmount(a[1]); e != nil {
				return s, e
			}
			break
		}
	}
	if s.DueDate == "" {
		return s, errors.New("minimum payment and due date not found")
	}

	// Each "TRANSACTION DESCRIPTION" header starts a table (domestic,
	// international, one per page) with its own columns. A description sits
	// in the header's column, may wrap onto the lines around its row, and
	// can be followed by reward points or a foreign amount after a wide gap.
	type table struct {
		desc    int
		rows    []int
		onRow   []string
		extra   map[int]string
		matches [][]string
	}
	var tables []*table
	var rowErrors []error
	for i, l := range lines {
		if strings.Contains(l, "TRANSACTION DESCRIPTION") && strings.Contains(l, "AMOUNT") {
			tables = append(tables, &table{desc: column(l, "TRANSACTION DESCRIPTION"), extra: map[int]string{}})
			continue
		}
		if len(tables) == 0 {
			if hdfcDatedRow.MatchString(l) {
				rowErrors = append(rowErrors, fmt.Errorf("dated row outside a recognised table on line %d", i+1))
			}
			continue
		}
		t := tables[len(tables)-1]
		if !hdfcDatedRow.MatchString(l) {
			if at := indent(l); at >= t.desc-1 && at <= t.desc+1 && !strings.Contains(l, "CKYC") {
				t.extra[i] = strings.TrimSpace(hdfcGap.Split(strings.TrimSpace(l), 2)[0])
			}
			continue
		}
		at := hdfcRow.FindStringSubmatchIndex(l)
		if at == nil {
			rowErrors = append(rowErrors, fmt.Errorf("unparsed transaction on line %d", i+1))
			continue
		}
		m := make([]string, len(at)/2)
		for n := range m {
			if at[2*n] >= 0 {
				m[n] = l[at[2*n]:at[2*n+1]]
			}
		}
		// The description: from its column (or the end of the time, when
		// the header isn't aligned) to where the amount begins.
		text := ""
		from := max(t.desc, len([]rune(l[:at[5]])))
		if r := []rune(l[:at[7]]); len(r) > from {
			text = strings.TrimSpace(hdfcGap.Split(strings.TrimSpace(string(r[from:])), 2)[0])
		}
		t.rows, t.onRow, t.matches = append(t.rows, i), append(t.onRow, text), append(t.matches, m)
	}
	var debits, credits int64
	for _, t := range tables {
		if len(t.rows) == 0 {
			continue
		}
		merchants, err := wrapped(t.rows, t.extra, t.onRow, " ")
		if err != nil {
			return s, err
		}
		for n, m := range t.matches {
			date, e := dateIn("02/01/2006 15:04", m[1]+" "+m[2])
			amount, e2 := ledger.MinorUnits(m[5])
			if e != nil || e2 != nil {
				rowErrors = append(rowErrors, fmt.Errorf("invalid date or amount on line %d", t.rows[n]+1))
				continue
			}
			credit := m[4] == "+"
			if credit {
				credits += amount
			} else {
				debits += amount
			}
			line(&s, date, merchants[n], amount, credit, "")
		}
	}
	if len(s.Transactions) == 0 {
		return s, errors.New("no statement transactions found")
	}
	if debits != s.Purchases {
		rowErrors = append(rowErrors, errors.New("transaction debits do not equal summary purchases"))
	}
	if credits != s.Payments {
		rowErrors = append(rowErrors, errors.New("transaction credits do not equal summary payments"))
	}
	if len(rowErrors) > 0 {
		return s, errors.Join(rowErrors...)
	}

	s.Discrepancy, _ = ledger.BalanceDifference(s.AccountKind, s.Opening, debits, credits, s.FinanceCharges, s.TotalDue)
	s.Balanced = s.Discrepancy >= -tolerance && s.Discrepancy <= tolerance && s.FinanceCharges == 0
	s.RoundingAccepted = s.Balanced && s.Discrepancy != 0
	if s.RoundingAccepted {
		s.Warnings = append(s.Warnings, fmt.Sprintf(
			"Final balance difference of %d paise accepted within the configured %d-paise rounding tolerance",
			s.Discrepancy, tolerance))
	}
	if s.Discrepancy != 0 && !s.RoundingAccepted {
		s.Warnings = append(s.Warnings, "Opening plus charges minus credits differs from total due; review required, do not import")
	}
	if s.FinanceCharges != 0 {
		s.Warnings = append(s.Warnings, "Finance charges need separate reconciliation; do not import")
	}
	return s, nil
}

// Summary amounts can be zero; transaction amounts can't.
func summaryAmount(s string) (int64, error) {
	if s == "0.00" {
		return 0, nil
	}
	return ledger.MinorUnits(s)
}
