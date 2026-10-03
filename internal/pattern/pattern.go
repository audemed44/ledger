// Package pattern turns example text into regular expressions that still
// match the next email of the same kind: numbers and month names change
// between emails, so they're loosened; everything else stays literal.
package pattern

import (
	"regexp"
	"strings"
	"unicode"
)

// Number matches a run of digits with optional separators, such as 12,345.67
// or 01.10.26. It ends in a digit, so a full stop after it stays literal.
const Number = `\d(?:[\d,.]*\d)?`

var months = map[string]bool{}

func init() {
	for _, m := range []string{"january", "february", "march", "april", "may", "june", "july",
		"august", "september", "october", "november", "december"} {
		months[m] = true
		months[m[:3]] = true
	}
	months["sept"] = true
}

// IsMonth reports whether word is an English month name or abbreviation.
func IsMonth(word string) bool { return months[strings.ToLower(word)] }

// Literal matches s, with whitespace runs loosened to \s+, numbers to Number
// and month names to any word.
func Literal(s string) string {
	var b strings.Builder
	r := []rune(s)
	for i := 0; i < len(r); {
		j := i + 1
		switch {
		case unicode.IsSpace(r[i]):
			for j < len(r) && unicode.IsSpace(r[j]) {
				j++
			}
			b.WriteString(`\s+`)
		case unicode.IsDigit(r[i]):
			end := i
			for j = i; j < len(r) && (unicode.IsDigit(r[j]) || r[j] == ',' || r[j] == '.'); j++ {
				if unicode.IsDigit(r[j]) {
					end = j
				}
			}
			j = end + 1
			b.WriteString(Number)
		case unicode.IsLetter(r[i]):
			for j < len(r) && unicode.IsLetter(r[j]) {
				j++
			}
			if word := string(r[i:j]); IsMonth(word) {
				b.WriteString(`[A-Za-z]+`)
			} else {
				b.WriteString(regexp.QuoteMeta(word))
			}
		default:
			b.WriteString(regexp.QuoteMeta(string(r[i])))
		}
		i = j
	}
	return b.String()
}

// Whole matches exactly s, loosened like Literal and case-insensitively. It
// suits a subject line or attachment name.
func Whole(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}
	return "(?i)^" + Literal(s) + "$"
}
