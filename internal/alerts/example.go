package alerts

import (
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf16"
	"unicode/utf8"

	"github.com/audemed44/ledger/internal/ledger"
	"github.com/audemed44/ledger/internal/pattern"
)

// Mark tags one span of an example email as a field. Start and End are
// UTF-16 offsets, as the browser counts them.
type Mark struct {
	Field string `json:"field"`
	Start int    `json:"start"`
	End   int    `json:"end"`
}

// Example is what FromExample writes: a body pattern, the date layout the
// tagged date uses and a subject pattern.
type Example struct {
	Pattern    string `json:"pattern"`
	DateLayout string `json:"date_layout,omitempty"`
	Subject    string `json:"subject"`
}

// Fields are the names a Mark can tag.
var Fields = []string{"amount", "merchant", "account", "date", "currency", "reference"}

// context is how many words around the tagged fields anchor the pattern, and
// gapLimit how long untagged text between fields can be before its middle
// is skipped.
const (
	context  = 3
	gapLimit = 80
)

// FromExample writes a parser pattern from an example email with its fields
// tagged. The pattern keeps a few words of literal context around each field
// (numbers and month names loosened), and must read back exactly the tagged
// text from the example, or it's refused.
func FromExample(subject, body string, marks []Mark) (Example, error) {
	if len(marks) == 0 {
		return Example{}, errors.New("select text in the email and tag it as a field")
	}
	if len(marks) > len(Fields) || len(body) > 200_000 {
		return Example{}, errors.New("too many fields or too long an email")
	}
	type span struct {
		field      string
		start, end int // byte offsets
	}
	spans := []span{}
	seen := map[string]bool{}
	for _, m := range marks {
		if !slices.Contains(Fields, m.Field) {
			return Example{}, fmt.Errorf("unknown field %q", m.Field)
		}
		if seen[m.Field] {
			return Example{}, fmt.Errorf("%s is tagged twice", m.Field)
		}
		seen[m.Field] = true
		start, end := byteOffset(body, m.Start), byteOffset(body, m.End)
		if start < 0 || end < 0 || end <= start {
			return Example{}, fmt.Errorf("the %s selection is outside the email text", m.Field)
		}
		if strings.TrimSpace(body[start:end]) != body[start:end] {
			return Example{}, fmt.Errorf("the %s selection starts or ends with a space", m.Field)
		}
		spans = append(spans, span{m.Field, start, end})
	}
	slices.SortFunc(spans, func(a, b span) int { return a.start - b.start })
	for i := 1; i < len(spans); i++ {
		if spans[i].start < spans[i-1].end {
			return Example{}, fmt.Errorf("%s and %s overlap", spans[i-1].field, spans[i].field)
		}
	}

	out := Example{Subject: pattern.Whole(subject)}
	var b strings.Builder
	first := spans[0].start
	line := body[strings.LastIndexByte(body[:first], '\n')+1 : first]
	if strings.TrimSpace(line) == "" {
		b.WriteString(`(?m:^)\s*`)
	} else {
		b.WriteString(pattern.Literal(lastWords(line, context)))
	}
	for i, s := range spans {
		if i > 0 {
			// A long gap keeps a few words at each end and skips the middle;
			// one with few words (however much whitespace) stays whole.
			gap := body[spans[i-1].end:s.start]
			if utf8.RuneCountInString(gap) <= gapLimit || len(strings.Fields(gap)) <= 2*context {
				b.WriteString(pattern.Literal(gap))
			} else {
				b.WriteString(pattern.Literal(firstWords(gap, context)) + `[\s\S]*?` +
					pattern.Literal(lastWords(gap, context)))
			}
		}
		group, layout, err := fieldPattern(s.field, body[s.start:s.end])
		if err != nil {
			return Example{}, err
		}
		if layout != "" {
			out.DateLayout = layout
		}
		b.WriteString("(?P<" + s.field + ">" + group + ")")
	}
	rest := body[spans[len(spans)-1].end:]
	if i := strings.IndexByte(rest, '\n'); i >= 0 {
		rest = rest[:i]
	}
	if strings.TrimSpace(rest) == "" {
		b.WriteString(`[ \t]*(?m:$)`)
	} else {
		b.WriteString(pattern.Literal(firstWords(rest, context)))
	}
	out.Pattern = b.String()

	// The pattern must read the example back exactly.
	re, err := regexp.Compile(out.Pattern)
	if err != nil {
		return Example{}, errors.New("could not build a pattern from this selection")
	}
	found := re.FindAllStringSubmatch(body, 2)
	if len(found) == 0 {
		return Example{}, errors.New("could not build a pattern that reads this email back; try tagging a slightly different span")
	}
	if len(found) > 1 {
		return Example{}, errors.New("the pattern matches this email more than once; tag fields closer together")
	}
	for _, s := range spans {
		if got := found[0][re.SubexpIndex(s.field)]; got != body[s.start:s.end] {
			return Example{}, fmt.Errorf("the pattern reads %s as %q; select the whole value and try again", s.field, got)
		}
	}
	return out, nil
}

// fieldPattern is the group pattern for a tagged value, after checking the
// value is usable. Dates also return their Go layout.
func fieldPattern(field, value string) (string, string, error) {
	switch field {
	case "amount":
		if _, err := ledger.MinorUnits(value); err != nil {
			return "", "", errors.New("select just the amount's digits, such as 1,234.56, without the currency")
		}
		return `\d[\d,]*(?:\.\d{1,2})?`, "", nil
	case "account":
		if !ledger.LastFour.MatchString(value) {
			return "", "", errors.New("select just the last four digits of the card or account")
		}
		return `\d{4}`, "", nil
	case "currency":
		if !ledger.Currencies[strings.ToUpper(value)] {
			return "", "", errors.New("currency must be a code such as INR; leave symbols like Rs. untagged and set the default currency")
		}
		return `[A-Za-z]{3}`, "", nil
	case "reference":
		if !strings.ContainsFunc(value, unicode.IsSpace) {
			return `\S+`, "", nil
		}
	case "date":
		return dateLayout(value)
	}
	if strings.Contains(value, "\n") {
		return `(?s:.+?)`, "", nil
	}
	return `[^\n]+?`, "", nil
}

var weekdays = map[string]string{}

func init() {
	for d := time.Sunday; d <= time.Saturday; d++ {
		weekdays[strings.ToLower(d.String())] = "Monday"
		weekdays[strings.ToLower(d.String()[:3])] = "Mon"
	}
}

// dateLayout works out the Go layout of an example date, such as
// "01-Oct-26 14:05" or "2026-10-01", and a pattern for dates like it.
// Numeric dates are read day first unless the year leads or the middle
// number can't be a month.
func dateLayout(value string) (string, string, error) {
	type token struct {
		text string
		kind byte // d digits, a letters, s space, p punctuation
	}
	tokens := []token{}
	r := []rune(value)
	for i := 0; i < len(r); {
		j := i + 1
		kind := byte('p')
		switch {
		case unicode.IsDigit(r[i]):
			kind = 'd'
			for j < len(r) && unicode.IsDigit(r[j]) {
				j++
			}
		case unicode.IsLetter(r[i]):
			kind = 'a'
			for j < len(r) && unicode.IsLetter(r[j]) {
				j++
			}
		case unicode.IsSpace(r[i]):
			kind = 's'
			for j < len(r) && unicode.IsSpace(r[j]) {
				j++
			}
		}
		tokens = append(tokens, token{string(r[i:j]), kind})
		i = j
	}

	layout := make([]string, len(tokens))
	var re strings.Builder
	twelveHour := false
	for _, t := range tokens {
		if w := strings.ToUpper(t.text); t.kind == 'a' && (w == "AM" || w == "PM") {
			twelveHour = true
		}
	}
	dateNumbers := []int{}
	hasMonthName := false
	for i, t := range tokens {
		switch t.kind {
		case 'd':
			if len(t.text) <= 2 {
				re.WriteString(`\d{1,2}`)
			} else {
				re.WriteString(`\d{` + strconv.Itoa(len(t.text)) + `}`)
			}
			timePart := (i+1 < len(tokens) && tokens[i+1].text == ":") || (i > 0 && tokens[i-1].text == ":")
			switch {
			case timePart && i > 0 && tokens[i-1].text == ":" && i > 2 && tokens[i-3].text == ":":
				layout[i] = "05"
			case timePart && i > 0 && tokens[i-1].text == ":":
				layout[i] = "04"
			case timePart && twelveHour:
				layout[i] = "3"
			case timePart:
				layout[i] = "15"
			case len(t.text) == 4:
				layout[i] = "2006"
			case len(t.text) <= 2:
				dateNumbers = append(dateNumbers, i)
			default:
				return "", "", errors.New("could not read that date; select just the date (and time)")
			}
		case 'a':
			lower := strings.ToLower(t.text)
			switch {
			case pattern.IsMonth(lower) && lower != "sept":
				hasMonthName = true
				layout[i] = "January"
				if len(lower) == 3 {
					layout[i] = "Jan"
				}
				re.WriteString(`[A-Za-z]+`)
			case weekdays[lower] != "":
				layout[i] = weekdays[lower]
				re.WriteString(`[A-Za-z]+`)
			case lower == "am" || lower == "pm":
				layout[i] = "pm"
				if t.text == strings.ToUpper(t.text) {
					layout[i] = "PM"
				}
				re.WriteString(`[AaPp][Mm]`)
			default:
				layout[i] = t.text
				re.WriteString(regexp.QuoteMeta(t.text))
			}
		case 's':
			layout[i] = t.text
			re.WriteString(`\s+`)
		default:
			layout[i] = t.text
			re.WriteString(regexp.QuoteMeta(t.text))
		}
	}

	year := slices.Index(layout, "2006")
	number := func(i int) int { n, _ := strconv.Atoi(tokens[i].text); return n }
	var order []string
	switch {
	case hasMonthName && year >= 0:
		order = []string{"2"}
	case hasMonthName:
		order = []string{"2", "06"}
	case year >= 0 && len(dateNumbers) > 0 && year < dateNumbers[0]:
		order = []string{"1", "2"}
	case len(dateNumbers) >= 2 && number(dateNumbers[1]) > 12:
		order = []string{"1", "2", "06"}
	default:
		order = []string{"2", "1", "06"}
	}
	if !hasMonthName && year >= 0 {
		order = order[:2]
	}
	if len(dateNumbers) != len(order) {
		return "", "", errors.New("could not read that date; select the day, month and year")
	}
	for n, i := range dateNumbers {
		layout[i] = order[n]
	}
	joined := strings.Join(layout, "")
	if _, err := time.Parse(joined, value); err != nil {
		return "", "", errors.New("could not read that date; set the date layout under Advanced")
	}
	return re.String(), joined, nil
}

// byteOffset converts a UTF-16 offset into s to a byte offset, or -1 when
// it's out of range or splits a character.
func byteOffset(s string, units int) int {
	if units < 0 {
		return -1
	}
	n := 0
	for i, r := range s {
		if n == units {
			return i
		}
		if n > units {
			return -1
		}
		n += utf16.RuneLen(r)
	}
	if n == units {
		return len(s)
	}
	return -1
}

// lastWords is the end of s holding its last n words, with the spacing after them.
func lastWords(s string, n int) string {
	words := 0
	inWord := false
	for i := len(s); i > 0; {
		r, size := utf8.DecodeLastRuneInString(s[:i])
		if unicode.IsSpace(r) {
			if inWord && words == n {
				return s[i:]
			}
			inWord = false
		} else if !inWord {
			inWord = true
			words++
		}
		i -= size
	}
	return s
}

// firstWords is the start of s through its first n words.
func firstWords(s string, n int) string {
	words := 0
	inWord := false
	for i, r := range s {
		if unicode.IsSpace(r) {
			if inWord && words == n {
				return s[:i]
			}
			inWord = false
		} else if !inWord {
			inWord = true
			words++
		}
	}
	return s
}
