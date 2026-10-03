// Package ledger holds the types every part of Ledger shares: transactions,
// account identities and money in integer minor units.
package ledger

import (
	"encoding/base64"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// Transaction is one movement of money on one account, from an alert or a
// statement line.
type Transaction struct {
	AccountID   string `json:"account_id"`
	AccountKind string `json:"account_kind"`
	ID          int64  `json:"id"`
	MessageID   int64  `json:"message_id"`
	Merchant    string `json:"merchant"`
	Account     string `json:"account"`
	Amount      int64  `json:"amount"`
	Currency    string `json:"currency"`
	Direction   string `json:"direction"`
	Date        string `json:"date"`
	Reference   string `json:"reference"`
	Status      string `json:"status"`
	Issuer      string `json:"issuer"`
	// Matched is set when an alert and a statement line are both behind it.
	Matched bool `json:"matched,omitempty"`
	// Placeholder is set when Merchant is the parser's description, the
	// email having named no merchant.
	Placeholder bool `json:"placeholder,omitempty"`
	// Transfer is how it was found to move money between your accounts
	// ("paired", "rule" or "manual"), and TransferOf the other side of a pair.
	Transfer   string `json:"transfer,omitempty"`
	TransferOf int64  `json:"transfer_of,omitempty"`
}

// Account is a card or bank account, identified by issuer, type and the last
// four digits.
type Account struct {
	ID       string `json:"id"`
	Issuer   string `json:"issuer"`
	Kind     string `json:"kind"`
	LastFour string `json:"last_four"`
}

// Currencies are the supported ones; all use two decimal places.
var Currencies = map[string]bool{
	"INR": true, "USD": true, "EUR": true, "GBP": true, "AUD": true,
	"CAD": true, "SGD": true, "AED": true, "CHF": true, "HKD": true,
}

// LastFour matches an account suffix.
var LastFour = regexp.MustCompile(`^[0-9]{4}$`)

// Western (1,234,567) and Indian (12,34,567) grouping, at most two decimals.
var moneyPattern = regexp.MustCompile(
	`^(?:[0-9]+|[0-9]{1,3}(?:,[0-9]{3})+|[0-9]{1,2}(?:,[0-9]{2})*,[0-9]{3})(?:\.[0-9]{1,2})?$`,
)

// MinorUnits parses a positive amount such as "1,234.56" into 123456.
// Amounts never pass through floating point; unsupported precision is rejected.
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

// Decimal formats minor units as "1234.56".
func Decimal(v int64) string { return fmt.Sprintf("%d.%02d", v/100, v%100) }

// Money formats minor units for people, with the currency in front: rupees
// as ₹1,23,456.78 (Indian digit grouping), other currencies as
// "USD 123,456.78".
func Money(v int64, currency string) string {
	sign := ""
	if v < 0 {
		sign, v = "-", -v
	}
	whole := strconv.FormatInt(v/100, 10)
	indian := currency == "" || strings.EqualFold(currency, "INR")
	if len(whole) > 3 {
		head, tail := whole[:len(whole)-3], whole[len(whole)-3:]
		size := 3
		if indian {
			size = 2
		}
		groups := []string{tail}
		for len(head) > size {
			groups = append([]string{head[len(head)-size:]}, groups...)
			head = head[:len(head)-size]
		}
		whole = strings.Join(append([]string{head}, groups...), ",")
	}
	symbol := "₹"
	if !indian {
		symbol = strings.ToUpper(currency) + " "
	}
	return fmt.Sprintf("%s%s%s.%02d", sign, symbol, whole, v%100)
}

// AccountKey is an account's identity, independent of parser names. Account
// types and issuers are part of it, so a bank account and a card sharing a
// suffix stay separate.
func AccountKey(issuer, kind, lastFour string) string {
	if kind == "" {
		kind = "unknown"
	}
	raw := strings.ToLower(strings.TrimSpace(issuer)) + "\x00" + kind + "\x00" + lastFour
	return base64.RawURLEncoding.EncodeToString([]byte(raw))
}

// AccountParts splits an AccountKey into issuer (lower case), kind and suffix.
func AccountParts(key string) ([]string, error) {
	raw, err := base64.RawURLEncoding.DecodeString(key)
	if err != nil {
		return nil, errors.New("invalid account filter")
	}
	parts := strings.Split(string(raw), "\x00")
	if len(parts) != 3 {
		return nil, errors.New("invalid account filter")
	}
	return parts, nil
}

// BalanceDifference is how far a statement's closing balance is from opening
// plus its movements. Cards are debt and bank accounts are funds, so debits
// and credits move them in opposite directions.
func BalanceDifference(kind string, opening, debits, credits, charges, closing int64) (int64, error) {
	switch kind {
	case "card":
		return opening + debits + charges - credits - closing, nil
	case "bank":
		return opening + credits - debits - charges - closing, nil
	default:
		return 0, errors.New("account type is missing or unknown")
	}
}
