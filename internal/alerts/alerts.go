// Package alerts turns per-transaction alert emails into transactions. A
// parser matches one exact sender and a subject regex, then extracts one
// transaction from the body with a regex of named groups.
package alerts

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/audemed44/ledger/internal/ledger"
)

// Parser is one alert format from one issuer.
type Parser struct {
	Issuer      string `json:"issuer"`
	AccountKind string `json:"account_kind" yaml:"account_kind"`
	ID          int64  `json:"id" yaml:"-"`
	Name        string `json:"name"`
	Sender      string `json:"sender"`
	Subject     string `json:"subject"`
	Pattern     string `json:"pattern"`
	DateLayout  string `json:"date_layout" yaml:"date_layout"`
	Timezone    string `json:"timezone"`
	Currency    string `json:"currency"`
	Direction   string `json:"direction"` // debit, credit or ignore
	Enabled     bool   `json:"enabled"`
	// Description stands in for the merchant when the email names none
	// ("Payment received"); a statement line that confirms the
	// transaction replaces it.
	Description string `json:"description,omitempty" yaml:"description,omitempty"`
	// Wordings are other ways the bank words the same alert, each with its
	// own body pattern; an email must match exactly one wording.
	Wordings []Wording `json:"wordings,omitempty" yaml:"wordings,omitempty"`
}

// Wording is one way an alert is worded: a body pattern, its date layout,
// and (when it differs from the parser's) the account type.
type Wording struct {
	Pattern     string `json:"pattern"`
	DateLayout  string `json:"date_layout" yaml:"date_layout"`
	AccountKind string `json:"account_kind,omitempty" yaml:"account_kind,omitempty"`
}

// MaxWordings bounds the extra wordings of one parser.
const MaxWordings = 20

// All is the parser's main wording followed by its extra ones, each with
// its account type filled in.
func (p Parser) All() []Wording {
	out := []Wording{{Pattern: p.Pattern, DateLayout: p.DateLayout, AccountKind: p.AccountKind}}
	for _, w := range p.Wordings {
		if w.AccountKind == "" {
			w.AccountKind = p.AccountKind
		}
		out = append(out, w)
	}
	return out
}

// Preview is what a parser makes of one email.
type Preview struct {
	Matched     bool                `json:"matched"`
	Ignored     bool                `json:"ignored"`
	Transaction *ledger.Transaction `json:"transaction,omitempty"`
}

var required = []string{"amount", "merchant", "account", "date"}

// Validate checks the definition before it's saved or run.
func (p Parser) Validate() error {
	if len(p.Issuer) > 100 || strings.ContainsRune(p.Issuer, 0) {
		return errors.New("invalid issuer")
	}
	if strings.TrimSpace(p.Name) == "" || len(p.Name) > 100 || !strings.Contains(p.Sender, "@") {
		return errors.New("issuer name and exact sender email are required")
	}
	if len(p.Subject) > 2000 || len(p.Description) > 100 {
		return errors.New("pattern too long")
	}
	if _, err := regexp.Compile(p.Subject); err != nil {
		return fmt.Errorf("subject pattern: %w", err)
	}
	if _, err := time.LoadLocation(p.Timezone); err != nil {
		return errors.New("use an IANA timezone such as Asia/Kolkata")
	}
	if p.Direction != "debit" && p.Direction != "credit" && p.Direction != "ignore" {
		return errors.New("direction must be debit, credit or ignore")
	}
	if p.Direction != "ignore" && !ledger.Currencies[p.Currency] {
		return errors.New("select a supported two-decimal currency")
	}
	if len(p.Wordings) > MaxWordings {
		return fmt.Errorf("at most %d extra wordings", MaxWordings)
	}
	for i, w := range p.All() {
		if err := p.validateWording(w); err != nil {
			if i > 0 {
				return fmt.Errorf("wording %d: %w", i+1, err)
			}
			return err
		}
	}
	return nil
}

// validateWording checks one wording's pattern, layout and account type.
func (p Parser) validateWording(w Wording) error {
	switch w.AccountKind {
	case "", "unknown", "card", "bank", "debit":
	default:
		return errors.New("account type must be card, bank or debit card")
	}
	if len(w.Pattern) > 16000 {
		return errors.New("pattern too long")
	}
	re, err := regexp.Compile(w.Pattern)
	if err != nil {
		return fmt.Errorf("body pattern: %w", err)
	}
	// An ignore rule without a body pattern ignores every email from the
	// sender whose subject matches: OTPs, notices, statement links.
	if p.Direction == "ignore" {
		return nil
	}
	if w.Pattern == "" {
		return errors.New("body pattern is required")
	}
	if w.DateLayout == "" {
		return errors.New("date layout is required")
	}
	for _, name := range required {
		if name == "merchant" && strings.TrimSpace(p.Description) != "" {
			continue
		}
		if re.SubexpIndex(name) < 0 {
			return fmt.Errorf("missing named group %s", name)
		}
	}
	seen := map[string]bool{}
	for _, name := range re.SubexpNames() {
		if name == "" {
			continue
		}
		if seen[name] {
			return fmt.Errorf("duplicate group %s", name)
		}
		seen[name] = true
	}
	return nil
}

// Parse runs the parser over one email. An email that isn't from the sender
// or doesn't match isn't an error; an email that matches but can't produce
// exactly one valid transaction is.
func (p Parser) Parse(sender, subject, body string) (Preview, error) {
	if err := p.Validate(); err != nil {
		return Preview{}, err
	}
	fromSender := strings.EqualFold(strings.TrimSpace(sender), strings.TrimSpace(p.Sender))
	if !fromSender || !regexp.MustCompile(p.Subject).MatchString(subject) {
		return Preview{}, nil
	}
	if p.Pattern == "" && len(p.Wordings) == 0 {
		return Preview{Matched: true, Ignored: true}, nil
	}
	var re *regexp.Regexp
	var matches [][]string
	var wording Wording
	for _, w := range p.All() {
		if w.Pattern == "" {
			continue
		}
		candidate := regexp.MustCompile(w.Pattern)
		found := candidate.FindAllStringSubmatch(body, 2)
		if len(found) == 0 {
			continue
		}
		if len(found) > 1 {
			return Preview{Matched: true}, errors.New("multiple matches: an alert must produce exactly one transaction")
		}
		if re != nil {
			return Preview{Matched: true}, errors.New("several wordings match; narrow one of them")
		}
		re, matches, wording = candidate, found, w
	}
	if re == nil {
		return Preview{}, nil
	}
	if p.Direction == "ignore" {
		return Preview{Matched: true, Ignored: true}, nil
	}
	get := func(name string) string {
		i := re.SubexpIndex(name)
		if i < 0 {
			return ""
		}
		return strings.TrimSpace(matches[0][i])
	}
	amount, err := ledger.MinorUnits(get("amount"))
	if err != nil {
		return Preview{Matched: true}, err
	}
	merchant, placeholder := get("merchant"), false
	if merchant == "" && strings.TrimSpace(p.Description) != "" {
		merchant, placeholder = strings.TrimSpace(p.Description), true
	}
	if !ledger.LastFour.MatchString(get("account")) || merchant == "" {
		return Preview{Matched: true}, errors.New("merchant and exactly four account digits are required")
	}
	loc, _ := time.LoadLocation(p.Timezone)
	date, err := time.ParseInLocation(wording.DateLayout, get("date"), loc)
	if err != nil {
		return Preview{Matched: true}, errors.New("date does not match the layout")
	}
	currency := get("currency")
	if currency == "" {
		currency = p.Currency
	}
	currency = strings.ToUpper(currency)
	if !ledger.Currencies[currency] {
		return Preview{Matched: true}, errors.New("unsupported currency")
	}
	direction := strings.ToLower(get("direction"))
	if direction == "" {
		direction = p.Direction
	}
	if direction != "debit" && direction != "credit" {
		return Preview{Matched: true}, errors.New("captured direction must be debit or credit")
	}
	issuer := strings.TrimSpace(p.Issuer)
	if issuer == "" {
		issuer = p.Name
	}
	kind := wording.AccountKind
	if kind == "" {
		kind = "unknown"
	}
	return Preview{Matched: true, Transaction: &ledger.Transaction{
		AccountID:   ledger.AccountKey(issuer, kind, get("account")),
		AccountKind: kind,
		Merchant:    merchant,
		Placeholder: placeholder,
		Account:     get("account"),
		Amount:      amount,
		Currency:    currency,
		Direction:   direction,
		Date:        date.Format(time.RFC3339),
		Reference:   get("reference"),
		Status:      "provisional",
		Issuer:      issuer,
	}}, nil
}
