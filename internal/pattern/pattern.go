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

// Literal matches s, with whitespace runs loosened to \s+, numbers to Number,
// month names to any word, and runs mixing letters and digits to any such
// run.
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
		case code(r, i) > i:
			// A run mixing letters and digits (a reference or random code
			// such as 19d48e58, a masked number, 16Jun2026) changes between
			// emails as a whole.
			j = code(r, i)
			b.WriteString(`[A-Za-z0-9]+`)
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

// code returns the end of the run of letters and digits starting at i when
// it mixes both, or i when it doesn't.
func code(r []rune, i int) int {
	letters, digits := false, false
	j := i
	for ; j < len(r) && (unicode.IsLetter(r[j]) || unicode.IsDigit(r[j])); j++ {
		letters = letters || unicode.IsLetter(r[j])
		digits = digits || unicode.IsDigit(r[j])
	}
	if letters && digits {
		return j
	}
	return i
}
