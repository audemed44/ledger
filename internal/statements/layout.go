package statements

import (
	"errors"
	"fmt"
	"math"
	"regexp"
	"slices"
	"strings"
	"time"
	"unicode"

	"github.com/audemed44/ledger/internal/ledger"
)

// Helpers shared by the layouts that read pdftotext -layout columns.

var india, _ = time.LoadLocation("Asia/Kolkata")

// column is the rune offset of label in line, or -1.
func column(line, label string) int {
	i := strings.Index(line, label)
	if i < 0 {
		return -1
	}
	return len([]rune(line[:i]))
}

// cell is the trimmed text of line between rune columns from and to (to < 0
// runs to the end).
func cell(line string, from, to int) string {
	r := []rune(line)
	if to < 0 || to > len(r) {
		to = len(r)
	}
	if from >= to {
		return ""
	}
	return strings.TrimSpace(string(r[from:to]))
}

// indent is the rune column of line's first non-space character, or -1.
func indent(line string) int {
	for i, r := range []rune(line) {
		if !unicode.IsSpace(r) {
			return i
		}
	}
	return -1
}

// money parses an amount that may be zero, such as a summary figure or an
// empty debit column ("0" or "0.00").
func money(s string) (int64, error) {
	s = strings.TrimSpace(s)
	if strings.Trim(s, "0.,") == "" && s != "" {
		return 0, nil
	}
	return ledger.MinorUnits(s)
}

// signed applies a CR/DR marker: on a card, a credit balance is negative.
func signed(amount int64, marker string) int64 {
	if strings.EqualFold(strings.TrimSpace(marker), "cr") {
		return -amount
	}
	return amount
}

// wrapped joins each table row's wrapped text. pdftotext centres a wrapped
// cell on its row, so a row has as many continuation lines above it as below
// it, and the continuation lines between two rows split accordingly. rows
// are the rows' line numbers in order, and extra the continuation lines'
// numbers and text (only those in the cell's column, after the table head).
// Extra lines after the last row's share are ignored.
func wrapped(rows []int, extra map[int]string, onRow []string, join string) ([]string, error) {
	out := make([]string, len(rows))
	numbers := make([]int, 0, len(extra))
	for n := range extra {
		numbers = append(numbers, n)
	}
	slices.Sort(numbers)
	// between is the extra lines strictly between two line numbers, in order.
	between := func(from, to int) []string {
		texts := []string{}
		for _, n := range numbers {
			if n > from && n < to {
				texts = append(texts, extra[n])
			}
		}
		return texts
	}
	above := between(-1, rows[0])
	for i, row := range rows {
		next := math.MaxInt
		if i+1 < len(rows) {
			next = rows[i+1]
		}
		gap := between(row, next)
		if len(gap) < len(above) {
			return nil, fmt.Errorf("could not attribute wrapped text near line %d", row+1)
		}
		parts := append(append(append([]string{}, above...), onRow[i]), gap[:len(above)]...)
		kept := parts[:0]
		for _, p := range parts {
			if p != "" {
				kept = append(kept, p)
			}
		}
		out[i] = strings.Join(kept, join)
		above = gap[len(above):]
	}
	return out, nil
}

// finish checks the lines against the summary, which the layout has already
// set: Opening and TotalDue (the closing balance), Purchases (debits) and
// Payments (credits). It sets the discrepancy and whether the statement
// balanced, within tolerance paise.
func finish(s *Statement, debits, credits, tolerance int64, rowErrors []error) (Statement, error) {
	if tolerance < 0 || tolerance > 99 {
		return *s, errors.New("balance tolerance must be between 0 and 99 paise")
	}
	s.BalanceTolerancePaise = tolerance
	if len(s.Transactions) == 0 {
		rowErrors = append(rowErrors, errors.New("no statement transactions found"))
	}
	if debits != s.Purchases {
		rowErrors = append(rowErrors, errors.New("transaction debits do not equal the summary's debits"))
	}
	if credits != s.Payments {
		rowErrors = append(rowErrors, errors.New("transaction credits do not equal the summary's credits"))
	}
	if len(rowErrors) > 0 {
		return *s, errors.Join(rowErrors...)
	}
	var err error
	s.Discrepancy, err = ledger.BalanceDifference(s.AccountKind, s.Opening, debits, credits, s.FinanceCharges, s.TotalDue)
	if err != nil {
		return *s, err
	}
	s.Balanced = s.Discrepancy >= -tolerance && s.Discrepancy <= tolerance
	s.RoundingAccepted = s.Balanced && s.Discrepancy != 0
	if s.RoundingAccepted {
		s.Warnings = append(s.Warnings, fmt.Sprintf(
			"Final balance difference of %d paise accepted within the configured %d-paise rounding tolerance",
			s.Discrepancy, tolerance))
	}
	if !s.Balanced {
		s.Warnings = append(s.Warnings, "Opening balance plus the lines differs from the closing balance; review required, do not import")
	}
	return *s, nil
}

// newStatement starts a statement for one account.
func newStatement(issuer, kind string) Statement {
	return Statement{Issuer: issuer, AccountKind: kind, Transactions: []ledger.Transaction{}, Warnings: []string{}}
}

// setAccount records the account's last four digits, refusing a second,
// different account: supplementary cards and other accounts aren't
// assigned to the first.
func setAccount(s *Statement, lastFour string) error {
	if !ledger.LastFour.MatchString(lastFour) {
		return errors.New("masked account number not found")
	}
	if s.Account != "" && s.Account != lastFour {
		return errAdditionalCard
	}
	s.Account = lastFour
	s.AccountID = ledger.AccountKey(s.Issuer, s.AccountKind, lastFour)
	return nil
}

// line adds one statement line, dated at midnight in India unless the date
// has a time.
func line(s *Statement, date time.Time, merchant string, amount int64, credit bool, reference string) {
	direction := "debit"
	if credit {
		direction = "credit"
	}
	s.Transactions = append(s.Transactions, ledger.Transaction{
		Merchant:    strings.Join(strings.Fields(merchant), " "),
		Account:     s.Account,
		Amount:      amount,
		Currency:    "INR",
		Direction:   direction,
		Date:        date.Format(time.RFC3339),
		Reference:   reference,
		Issuer:      s.Issuer,
		AccountID:   s.AccountID,
		AccountKind: s.AccountKind,
		Status:      "flagged",
	})
}

// dateIn parses value with layout in India's time zone.
func dateIn(layout, value string) (time.Time, error) {
	return time.ParseInLocation(layout, strings.TrimSpace(value), india)
}

// dayOnly is a layout's statement date as YYYY-MM-DD.
func dayOnly(layout, value string) (string, error) {
	t, err := dateIn(layout, value)
	if err != nil {
		return "", err
	}
	return t.Format("2006-01-02"), nil
}

var amountPattern = regexp.MustCompile(`[\d,]+\.\d{2}`)
