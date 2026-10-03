// Package statements reads PDF statements: it decrypts and extracts their
// text with qpdf and pdftotext, then an issuer-specific adapter parses and
// validates it. Statement parsing is deliberately separate from the editable
// alert rules: layouts need page headers, columns, credit markers and summary
// checks a regex can't express.
package statements

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"regexp"
	"strings"

	"github.com/audemed44/ledger/internal/ledger"
	"github.com/audemed44/ledger/internal/pattern"
)

// Parser is a saved statement preset: a layout adapter plus which configured
// password to try. Passwords themselves never leave the environment.
type Parser struct {
	BalanceTolerancePaise int64  `json:"balance_tolerance_paise"`
	ID                    int64  `json:"id"`
	Name                  string `json:"name"`
	Adapter               string `json:"adapter"`
	PasswordSlot          int    `json:"password_slot"` // 0 tries every password
	// Triggers pick the statement emails this preset imports by itself.
	Triggers []Trigger `json:"triggers"`
}

// Trigger matches a statement email from one exact sender, by subject and
// attachment name regexes. A blank regex matches anything.
type Trigger struct {
	Sender   string `json:"sender"`
	Subject  string `json:"subject"`
	Filename string `json:"filename"`
}

// MaxTriggers is how many triggers one preset can have.
const MaxTriggers = 20

// TriggerFor is the trigger for statements like this email's attachment:
// its sender, and its subject and file name with numbers and month names
// loosened.
func TriggerFor(sender, subject, filename string) Trigger {
	return Trigger{
		Sender:   strings.ToLower(strings.TrimSpace(sender)),
		Subject:  pattern.Whole(subject),
		Filename: pattern.Whole(filename),
	}
}

// Validate checks a trigger before it's saved.
func (t Trigger) Validate() error {
	if !strings.Contains(t.Sender, "@") || len(t.Sender) > 320 {
		return errors.New("An automatic import needs the exact sender address")
	}
	if len(t.Subject) > 2000 || len(t.Filename) > 2000 {
		return errors.New("Automatic import pattern too long")
	}
	if _, err := regexp.Compile(t.Subject); err != nil {
		return errors.New("Invalid subject pattern for automatic import")
	}
	if _, err := regexp.Compile(t.Filename); err != nil {
		return errors.New("Invalid file name pattern for automatic import")
	}
	return nil
}

// Matches reports whether a PDF attachment of an email fits the trigger.
func (t Trigger) Matches(sender, subject, filename string) bool {
	if t.Validate() != nil || !strings.EqualFold(strings.TrimSpace(sender), t.Sender) {
		return false
	}
	return regexp.MustCompile(t.Subject).MatchString(subject) &&
		regexp.MustCompile(t.Filename).MatchString(filename)
}

// Matches reports whether any of the preset's triggers fit.
func (p Parser) Matches(sender, subject, filename string) bool {
	for _, t := range p.Triggers {
		if t.Matches(sender, subject, filename) {
			return true
		}
	}
	return false
}

// Adapter is a supported statement layout.
type Adapter struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description"`
	// parse reads a statement, allowing tolerance paise of rounding in
	// the final balance.
	parse func(text Text, tolerance int64) (Statement, error)
}

// Adapters lists the layouts Ledger can read.
var Adapters = []Adapter{{
	ID:          "hdfc-credit-card",
	Name:        "HDFC Credit Card Parser v1",
	Description: "HDFC credit card summary and dated transaction table",
	parse:       func(t Text, tolerance int64) (Statement, error) { return ParseHDFCWithTolerance(t.Layout, tolerance) },
}, {
	ID:          "axis-credit-card",
	Name:        "Axis Credit Card Parser v1",
	Description: "Axis Bank credit card payment summary and Dr/Cr transaction lines",
	parse:       parseAxis,
}, {
	ID:          "icici-credit-card",
	Name:        "ICICI Credit Card Parser v1",
	Description: "ICICI Bank credit card summary, with lines read from the PDF's raw text",
	parse:       parseICICI,
}, {
	ID:          "idfc-credit-card",
	Name:        "IDFC FIRST Credit Card Parser v1",
	Description: "IDFC FIRST Bank credit card summary and DR/CR transaction lines",
	parse:       parseIDFC,
}, {
	ID:          "sbi-savings",
	Name:        "SBI Savings Account Parser v1",
	Description: "SBI account statement's transaction overview, with running balances",
	parse:       parseSBI,
}}

// adapter finds a layout by ID.
func adapter(id string) (Adapter, bool) {
	for _, a := range Adapters {
		if a.ID == id {
			return a, true
		}
	}
	return Adapter{}, false
}

// MaxPasswords is how many LEDGER_PDF_PASSWORDS slots are supported.
const MaxPasswords = 32

// Validate checks a preset before it's saved.
func (p Parser) Validate() error {
	if p.BalanceTolerancePaise < 0 || p.BalanceTolerancePaise > 99 {
		return errors.New("Balance tolerance must be between 0 and 99 paise")
	}
	if strings.TrimSpace(p.Name) == "" || len(p.Name) > 100 {
		return errors.New("Statement parser name is required (up to 100 characters)")
	}
	if _, ok := adapter(p.Adapter); !ok {
		return errors.New("Unsupported statement layout; a dedicated adapter is needed")
	}
	if p.PasswordSlot < 0 || p.PasswordSlot > MaxPasswords {
		return errors.New("Invalid password slot")
	}
	if len(p.Triggers) > MaxTriggers {
		return errors.New("Too many automatic imports on one parser")
	}
	for _, t := range p.Triggers {
		if err := t.Validate(); err != nil {
			return err
		}
	}
	return nil
}

// Parse runs the preset's adapter over extracted text.
func (p Parser) Parse(text Text) (Statement, error) {
	a, ok := adapter(p.Adapter)
	if !ok {
		return Statement{}, errors.New("unsupported statement layout")
	}
	return a.parse(text, p.BalanceTolerancePaise)
}

// ParseWith runs the layout with ID id over text, with the default 99
// paise rounding tolerance.
func ParseWith(id string, text Text) (Statement, error) {
	return Parser{Adapter: id, BalanceTolerancePaise: 99}.Parse(text)
}

// Statement is one parsed statement: its summary, its lines and whether they
// add up.
type Statement struct {
	BalanceTolerancePaise int64                `json:"balance_tolerance_paise"`
	RoundingAccepted      bool                 `json:"rounding_accepted"`
	AccountID             string               `json:"account_id"`
	AccountKind           string               `json:"account_kind"`
	Issuer                string               `json:"issuer"`
	Account               string               `json:"account"`
	Date                  string               `json:"date"`
	DueDate               string               `json:"due_date"`
	MinimumDue            int64                `json:"minimum_due"`
	Opening               int64                `json:"opening"`
	Payments              int64                `json:"payments"`
	Purchases             int64                `json:"purchases"`
	FinanceCharges        int64                `json:"finance_charges"`
	TotalDue              int64                `json:"total_due"`
	Transactions          []ledger.Transaction `json:"transactions"`
	Discrepancy           int64                `json:"discrepancy"`
	Balanced              bool                 `json:"balanced"`
	Warnings              []string             `json:"warnings"`
}

// Fingerprint identifies a statement's financial content. Validation
// preferences don't change it.
func Fingerprint(s Statement) string {
	s.BalanceTolerancePaise = 0
	s.RoundingAccepted = false
	s.Warnings = nil
	raw, _ := json.Marshal(s)
	hash := sha256.Sum256(raw)
	return hex.EncodeToString(hash[:])
}
