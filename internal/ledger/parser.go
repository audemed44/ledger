package ledger

import (
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"
)

type Parser struct {
	ID         int64  `json:"id" yaml:"-"`
	Name       string `json:"name"`
	Sender     string `json:"sender"`
	Subject    string `json:"subject"`
	Pattern    string `json:"pattern"`
	DateLayout string `json:"date_layout" yaml:"date_layout"`
	Timezone   string `json:"timezone"`
	Currency   string `json:"currency"`
	Direction  string `json:"direction"`
	Enabled    bool   `json:"enabled"`
}

type Transaction struct {
	ID        int64  `json:"id"`
	MessageID int64  `json:"message_id"`
	Merchant  string `json:"merchant"`
	Account   string `json:"account"`
	Amount    int64  `json:"amount"`
	Currency  string `json:"currency"`
	Direction string `json:"direction"`
	Date      string `json:"date"`
	Reference string `json:"reference"`
	Status    string `json:"status"`
	Issuer    string `json:"issuer"`
}

type Preview struct {
	Matched     bool         `json:"matched"`
	Ignored     bool         `json:"ignored"`
	Transaction *Transaction `json:"transaction,omitempty"`
}

var currencies = map[string]bool{"INR": true, "USD": true, "EUR": true, "GBP": true, "AUD": true, "CAD": true, "SGD": true, "AED": true, "CHF": true, "HKD": true}
var moneyPattern = regexp.MustCompile(`^(?:[0-9]+|[0-9]{1,3}(?:,[0-9]{3})+|[0-9]{1,2}(?:,[0-9]{2})*,[0-9]{3})(?:\.[0-9]{1,2})?$`)
var lastFour = regexp.MustCompile(`^[0-9]{4}$`)

// Amounts never pass through floating point. Unsupported precision is rejected.
func MinorUnits(value string) (int64, error) {
	value = strings.TrimSpace(value)
	if !moneyPattern.MatchString(value) {
		return 0, errors.New("amount must be positive with at most two decimal places")
	}
	parts := strings.Split(strings.ReplaceAll(value, ",", ""), ".")
	fraction := "00"
	if len(parts) == 2 {
		fraction = (parts[1] + "0")[:2]
	}
	n, err := strconv.ParseInt(parts[0]+fraction, 10, 64)
	if err != nil || n <= 0 || n > 1_000_000_000_000_00 {
		return 0, errors.New("amount outside supported range")
	}
	return n, nil
}

func (p Parser) Validate() error {
	if strings.TrimSpace(p.Name) == "" || len(p.Name) > 100 || !strings.Contains(p.Sender, "@") {
		return errors.New("issuer name and exact sender email are required")
	}
	if len(p.Pattern) > 16000 || len(p.Subject) > 2000 {
		return errors.New("pattern too long")
	}
	if _, err := regexp.Compile(p.Subject); err != nil {
		return fmt.Errorf("subject pattern: %w", err)
	}
	re, err := regexp.Compile(p.Pattern)
	if err != nil {
		return fmt.Errorf("body pattern: %w", err)
	}
	if p.Pattern == "" {
		return errors.New("body pattern is required")
	}
	if _, err = time.LoadLocation(p.Timezone); err != nil {
		return errors.New("use an IANA timezone such as Asia/Kolkata")
	}
	if p.Direction != "debit" && p.Direction != "credit" && p.Direction != "ignore" {
		return errors.New("direction must be debit, credit or ignore")
	}
	if p.Direction == "ignore" {
		return nil
	}
	if !currencies[p.Currency] {
		return errors.New("select a supported two-decimal currency")
	}
	if p.DateLayout == "" {
		return errors.New("date layout is required")
	}
	for _, name := range []string{"amount", "merchant", "account", "date"} {
		if re.SubexpIndex(name) < 0 {
			return fmt.Errorf("missing named group %s", name)
		}
	}
	seen := map[string]bool{}
	for _, name := range re.SubexpNames() {
		if name != "" {
			if seen[name] {
				return fmt.Errorf("duplicate group %s", name)
			}
			seen[name] = true
		}
	}
	return nil
}

func (p Parser) Parse(sender, subject, body string) (Preview, error) {
	if err := p.Validate(); err != nil {
		return Preview{}, err
	}
	if !strings.EqualFold(strings.TrimSpace(sender), strings.TrimSpace(p.Sender)) || !regexp.MustCompile(p.Subject).MatchString(subject) {
		return Preview{}, nil
	}
	re := regexp.MustCompile(p.Pattern)
	matches := re.FindAllStringSubmatch(body, 2)
	if len(matches) == 0 {
		return Preview{}, nil
	}
	if len(matches) > 1 {
		return Preview{Matched: true}, errors.New("multiple matches: an alert must produce exactly one transaction")
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
	amount, err := MinorUnits(get("amount"))
	if err != nil {
		return Preview{Matched: true}, err
	}
	if !lastFour.MatchString(get("account")) || get("merchant") == "" {
		return Preview{Matched: true}, errors.New("merchant and exactly four account digits are required")
	}
	loc, _ := time.LoadLocation(p.Timezone)
	date, err := time.ParseInLocation(p.DateLayout, get("date"), loc)
	if err != nil {
		return Preview{Matched: true}, errors.New("date does not match the layout")
	}
	currency := get("currency")
	if currency == "" {
		currency = p.Currency
	}
	currency = strings.ToUpper(currency)
	if !currencies[currency] {
		return Preview{Matched: true}, errors.New("unsupported currency")
	}
	direction := strings.ToLower(get("direction"))
	if direction == "" {
		direction = p.Direction
	}
	if direction != "debit" && direction != "credit" {
		return Preview{Matched: true}, errors.New("captured direction must be debit or credit")
	}
	return Preview{Matched: true, Transaction: &Transaction{Merchant: get("merchant"), Account: get("account"), Amount: amount, Currency: currency, Direction: direction, Date: date.Format(time.RFC3339), Reference: get("reference"), Status: "provisional", Issuer: p.Name}}, nil
}
