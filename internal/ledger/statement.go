package ledger

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"time"
)

// Statement parsing is deliberately separate from editable single-alert rules.
// This adapter supports the HDFC Tata Neu layout with DATE & TIME transaction
// rows and a five-field summary. Unknown layouts and missing rows fail closed.
type Statement struct {
	Issuer         string        `json:"issuer"`
	Account        string        `json:"account"`
	Date           string        `json:"date"`
	DueDate        string        `json:"due_date"`
	MinimumDue     int64         `json:"minimum_due"`
	Opening        int64         `json:"opening"`
	Payments       int64         `json:"payments"`
	Purchases      int64         `json:"purchases"`
	FinanceCharges int64         `json:"finance_charges"`
	TotalDue       int64         `json:"total_due"`
	Transactions   []Transaction `json:"transactions"`
	Discrepancy    int64         `json:"discrepancy"`
	Balanced       bool          `json:"balanced"`
	Warnings       []string      `json:"warnings"`
}

var statementRow = regexp.MustCompile(`^\s*(\d{2}/\d{2}/\d{4})\|\s*(\d{2}:\d{2})\s+(.+?)\s{2,}(\+?)\s*[C₹]\s*([\d,]+\.\d{2})\s*[A-Za-z]?\s*$`)
var datedRow = regexp.MustCompile(`^\s*\d{2}/\d{2}/\d{4}`)
var statementMoney = regexp.MustCompile(`[C₹]\s*([\d,]+\.\d{2})`)
var statementDate = regexp.MustCompile(`\d{2} [A-Za-z]{3}, \d{4}`)

func ParseHDFCStatement(text string) (Statement, error) {
	s := Statement{Issuer: "HDFC", Transactions: []Transaction{}, Warnings: []string{}}
	if !strings.Contains(strings.ToLower(text), "hdfc") || !strings.Contains(text, "NeuCoins") || !strings.Contains(text, "PREVIOUS STATEMENT DUES") {
		return s, errors.New("unsupported statement layout; expected HDFC Tata Neu")
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
			if d := statementDate.FindString(line); d != "" {
				v, e := time.Parse("02 Jan, 2006", d)
				if e != nil {
					return s, errors.New("invalid statement date")
				}
				s.Date = v.Format("2006-01-02")
			}
		}
		if strings.Contains(line, "Credit Card No.") {
			digits := regexp.MustCompile(`\d[\dxX* ]*\d`).FindString(strings.SplitN(line, "Credit Card No.", 2)[1])
			digits = strings.TrimSpace(digits)
			if len(digits) >= 4 && lastFour.MatchString(digits[len(digits)-4:]) {
				s.Account = digits[len(digits)-4:]
			}
		}
	}
	if s.Date == "" || s.Account == "" {
		return s, errors.New("statement date or masked account not found")
	}
	if summaryStart < 0 || summaryEnd <= summaryStart || summaryEnd-summaryStart > 12 {
		return s, errors.New("statement summary not found")
	}
	amounts := statementMoney.FindAllStringSubmatch(strings.Join(lines[summaryStart:summaryEnd], "\n"), -1)
	if len(amounts) != 5 {
		return s, errors.New("expected all five statement summary amounts")
	}
	values := []*int64{&s.Opening, &s.Payments, &s.Purchases, &s.FinanceCharges, &s.TotalDue}
	for i, a := range amounts {
		n, e := statementMinor(a[1])
		if e != nil {
			return s, e
		}
		*values[i] = n
	}
	if dueStart >= 0 {
		for _, line := range lines[dueStart+1 : min(dueStart+7, len(lines))] {
			d := statementDate.FindString(line)
			a := statementMoney.FindStringSubmatch(line)
			if d != "" && a != nil {
				v, e := time.Parse("02 Jan, 2006", d)
				if e != nil {
					return s, errors.New("invalid payment due date")
				}
				s.DueDate = v.Format("2006-01-02")
				s.MinimumDue, e = statementMinor(a[1])
				if e != nil {
					return s, e
				}
				break
			}
		}
	}
	if s.DueDate == "" {
		return s, errors.New("minimum payment and due date not found")
	}
	loc, _ := time.LoadLocation("Asia/Kolkata")
	var debits, credits int64
	table := false
	for i, line := range lines {
		if strings.Contains(line, "TRANSACTION DESCRIPTION") && strings.Contains(line, "AMOUNT") {
			table = true
			continue
		}
		if !datedRow.MatchString(line) {
			continue
		}
		if !table {
			return s, fmt.Errorf("dated row outside a recognised table on line %d", i+1)
		}
		m := statementRow.FindStringSubmatch(line)
		if m == nil {
			return s, fmt.Errorf("unparsed transaction on line %d", i+1)
		}
		date, e := time.ParseInLocation("02/01/2006 15:04", m[1]+" "+m[2], loc)
		if e != nil {
			return s, fmt.Errorf("invalid date on line %d", i+1)
		}
		amount, e := MinorUnits(m[5])
		if e != nil {
			return s, fmt.Errorf("invalid amount on line %d", i+1)
		}
		direction := "debit"
		if m[4] == "+" {
			direction = "credit"
			credits += amount
		} else {
			debits += amount
		}
		s.Transactions = append(s.Transactions, Transaction{Merchant: strings.TrimSpace(m[3]), Account: s.Account, Amount: amount, Currency: "INR", Direction: direction, Date: date.Format(time.RFC3339), Issuer: s.Issuer, Status: "flagged"})
	}
	if len(s.Transactions) == 0 {
		return s, errors.New("no statement transactions found")
	}
	if debits != s.Purchases {
		return s, errors.New("transaction debits do not equal summary purchases")
	}
	if credits != s.Payments {
		return s, errors.New("transaction credits do not equal summary payments")
	}
	s.Discrepancy = s.Opening + debits + s.FinanceCharges - credits - s.TotalDue
	s.Balanced = s.Discrepancy == 0 && s.FinanceCharges == 0
	if s.Discrepancy != 0 {
		s.Warnings = append(s.Warnings, "Opening plus charges minus credits differs from total due; review required, do not import")
	}
	if s.FinanceCharges != 0 {
		s.Warnings = append(s.Warnings, "Finance charges need separate reconciliation; do not import")
	}
	return s, nil
}
func statementMinor(s string) (int64, error) {
	if s == "0.00" {
		return 0, nil
	}
	return MinorUnits(s)
}

type limitedBuffer struct{ bytes.Buffer }

func (b *limitedBuffer) Write(p []byte) (int, error) {
	if b.Len()+len(p) > 4<<20 {
		return 0, errors.New("extracted text exceeds 4 MiB")
	}
	return b.Buffer.Write(p)
}

// Password is supplied over stdin, never in the process argument list.
// Decrypted bytes live only in a private temporary directory until extraction ends.
func ExtractPDF(ctx context.Context, path, password string) (string, error) {
	info, err := os.Stat(path)
	if err != nil || !info.Mode().IsRegular() || info.Size() > maxMail {
		return "", errors.New("PDF must be a regular file below 25 MiB")
	}
	ctx, cancel := context.WithTimeout(ctx, time.Minute)
	defer cancel()
	dir, err := os.MkdirTemp("", "ledger-pdf-*")
	if err != nil {
		return "", err
	}
	defer os.RemoveAll(dir)
	decoded := dir + "/statement.pdf"
	cmd := exec.CommandContext(ctx, "qpdf", "--password-file=-", "--decrypt", path, decoded)
	cmd.Stdin = strings.NewReader(password + "\n")
	// qpdf code 3 means completed with warnings; validate output via pdftotext.
	if err = cmd.Run(); err != nil {
		var exit *exec.ExitError
		if !errors.As(err, &exit) || exit.ExitCode() != 3 {
			return "", errors.New("PDF decryption failed; check password and qpdf installation")
		}
	}
	var output limitedBuffer
	cmd = exec.CommandContext(ctx, "pdftotext", "-layout", decoded, "-")
	cmd.Stdout = &output
	if err = cmd.Run(); err != nil {
		return "", errors.New("PDF text extraction failed; check pdftotext installation")
	}
	return output.String(), nil
}
