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
	"strings"

	"github.com/audemed44/ledger/internal/ledger"
)

// Parser is a saved statement preset: a layout adapter plus which configured
// password to try. Passwords themselves never leave the environment.
type Parser struct {
	BalanceTolerancePaise int64  `json:"balance_tolerance_paise"`
	ID                    int64  `json:"id"`
	Name                  string `json:"name"`
	Adapter               string `json:"adapter"`
	PasswordSlot          int    `json:"password_slot"` // 0 tries every password
}

// Adapter is a supported statement layout.
type Adapter struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description"`
}

// Adapters lists the layouts Ledger can read.
var Adapters = []Adapter{{
	ID:          "hdfc-credit-card",
	Name:        "HDFC Credit Card Parser v1",
	Description: "HDFC credit card summary and dated transaction table",
}}

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
	if p.Adapter != "hdfc-credit-card" {
		return errors.New("Unsupported statement layout; a dedicated adapter is needed")
	}
	if p.PasswordSlot < 0 || p.PasswordSlot > MaxPasswords {
		return errors.New("Invalid password slot")
	}
	return nil
}

// Parse runs the preset's adapter over extracted text.
func (p Parser) Parse(text string) (Statement, error) {
	return ParseHDFCWithTolerance(text, p.BalanceTolerancePaise)
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
