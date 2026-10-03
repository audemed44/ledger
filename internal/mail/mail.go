// Package mail decodes raw MIME messages into what Ledger needs: sender,
// subject, date, readable text and which attachments are PDFs. HTML becomes
// inert text; markup is never rendered and remote resources never fetched.
package mail

import (
	"unicode/utf8"
	"unicode"
	"bytes"
	"errors"
	"io"
	"regexp"
	"strings"
	"time"

	_ "github.com/emersion/go-message/charset" // decode non-UTF-8 bodies
	"github.com/emersion/go-message/mail"
	"golang.org/x/net/html"
)

const (
	// MaxSize bounds a whole message, attachments included.
	MaxSize = 25 << 20
	// MaxText bounds the decoded text of one message.
	MaxText = 1 << 20
	// StatementReason is why a message with a PDF waits in the inbox.
	StatementReason = "PDF statement — review and import"
)

// Attachment is one attachment, by its MIME part index.
type Attachment struct {
	Part        int    `json:"part"`
	Name        string `json:"name"`
	ContentType string `json:"content_type"`
}

// Message is a decoded email. Reason is set when the message can't be read
// as an alert: MIME problems (prefixed "MIME:") or a PDF statement.
type Message struct {
	Key         string // "message-id:<id>", or empty when there's no Message-ID
	Sender      string
	Subject     string
	Date        string // RFC 3339, or empty when there's no Date header
	Body        string
	BodyFormat  string // plain or html
	Reason      string
	Attachments []Attachment
	HasPDF      bool
	CanParse    bool
}

// IsPDF reports whether a part is a PDF, by type or file name.
func IsPDF(contentType, name string) bool {
	return contentType == "application/pdf" || strings.HasSuffix(strings.ToLower(name), ".pdf")
}

// Decode reads a raw message. Real plain text is preferred when there's an
// HTML alternative.
// bracketed finds an address in angle brackets.
var bracketed = regexp.MustCompile(`<([^<>@\s]+@[^<>@\s]+)>`)

func Decode(raw []byte) Message {
	var m Message
	reader, err := mail.CreateReader(bytes.NewReader(raw))
	if err != nil {
		m.Reason = "MIME: unable to decode message; original archived"
		return m
	}
	defer reader.Close()
	if id, e := reader.Header.MessageID(); e == nil && id != "" {
		m.Key = "message-id:" + id
	}
	m.Subject, _ = reader.Header.Subject()
	if addrs, e := reader.Header.AddressList("From"); e == nil && len(addrs) == 1 {
		m.Sender = addrs[0].Address
	} else if found := bracketed.FindAllStringSubmatch(reader.Header.Get("From"), -1); len(found) == 1 {
		// Some banks send a display name the strict parser refuses, such as
		// an empty encoded word ("=?UTF-8?B??="); the address is still
		// plain in angle brackets.
		m.Sender = found[0][1]
	}
	if date, e := reader.Header.Date(); e == nil && !date.IsZero() {
		m.Date = date.Format(time.RFC3339)
	}

	var plain, rich strings.Builder
	partIndex := -1
	for {
		part, e := reader.NextPart()
		if e == io.EOF {
			break
		}
		if e != nil {
			m.Reason = "MIME: unable to decode a part; original archived"
			break
		}
		partIndex++
		switch h := part.Header.(type) {
		case *mail.InlineHeader:
			kind, _, _ := h.ContentType()
			// Some issuers label PDFs as inline. Keep them out of the alert pipeline too.
			if kind == "application/pdf" {
				m.HasPDF = true
				m.Attachments = append(m.Attachments, Attachment{Part: partIndex, Name: "Inline PDF", ContentType: kind})
				continue
			}
			if kind != "text/plain" && kind != "text/html" && kind != "" {
				continue
			}
			b, e := io.ReadAll(io.LimitReader(part.Body, MaxText+1))
			if e != nil || len(b) > MaxText {
				m.Reason = "MIME: text part too large or unreadable"
				continue
			}
			target := &plain
			if kind == "text/html" {
				target = &rich
			}
			if target.Len()+len(b) > MaxText {
				m.Reason = "MIME: combined text parts too large"
				continue
			}
			target.Write(b)
			target.WriteByte('\n')
		case *mail.AttachmentHeader:
			kind, _, _ := h.ContentType()
			name, _ := h.Filename()
			if name == "" {
				name = "Unnamed attachment"
			}
			m.Attachments = append(m.Attachments, Attachment{Part: partIndex, Name: name, ContentType: kind})
			if IsPDF(kind, name) {
				m.HasPDF = true
			}
		}
	}

	m.Body = plain.String()
	if strings.TrimSpace(m.Body) != "" {
		m.BodyFormat = "plain"
	} else if strings.TrimSpace(rich.String()) != "" {
		text, e := htmlText(rich.String())
		if e != nil {
			m.Reason = "MIME: unable to extract HTML text; original archived"
		} else {
			m.Body = text
			m.BodyFormat = "html"
		}
	}
	m.Body = cleanText(m.Body)
	if m.Reason == "" {
		if m.HasPDF {
			m.Reason = StatementReason
		} else if strings.TrimSpace(m.Body) == "" {
			m.Reason = "MIME: no readable email text; original archived"
		}
	}
	m.CanParse = m.Reason == "" && strings.TrimSpace(m.Body) != ""
	return m
}

// WritePDF copies the PDF in MIME part `part` to w, up to MaxSize bytes.
func WritePDF(raw []byte, part int, w io.Writer) error {
	if part < 0 {
		return errors.New("Invalid attachment")
	}
	reader, err := mail.CreateReader(bytes.NewReader(raw))
	if err != nil {
		return errors.New("Could not decode archived email")
	}
	defer reader.Close()
	for index := 0; ; index++ {
		item, e := reader.NextPart()
		if e == io.EOF {
			return errors.New("PDF attachment not found")
		}
		if e != nil {
			return errors.New("Could not decode attachment")
		}
		if index != part {
			continue
		}
		kind, name := "", ""
		switch h := item.Header.(type) {
		case *mail.AttachmentHeader:
			kind, _, _ = h.ContentType()
			name, _ = h.Filename()
		case *mail.InlineHeader:
			kind, _, _ = h.ContentType()
		}
		if !IsPDF(kind, name) {
			return errors.New("Selected attachment is not a PDF")
		}
		n, e := io.Copy(w, io.LimitReader(item.Body, MaxSize+1))
		if e != nil || n > MaxSize {
			return errors.New("PDF exceeds size limit or could not be read")
		}
		return nil
	}
}

var blockElements = map[string]bool{
	"p": true, "div": true, "tr": true, "table": true, "h1": true, "h2": true, "h3": true,
	"h4": true, "h5": true, "h6": true, "li": true, "ul": true, "ol": true, "section": true,
	"article": true, "header": true, "footer": true, "blockquote": true, "pre": true,
}

// htmlText extracts the visible text of an HTML email, one line per block.
// Scripts, styles and hidden elements are dropped.
func htmlText(source string) (string, error) {
	root, err := html.Parse(strings.NewReader(source))
	if err != nil {
		return "", err
	}
	var output strings.Builder
	var walk func(*html.Node, int) error
	walk = func(n *html.Node, depth int) error {
		if depth > 256 {
			return errors.New("HTML too deeply nested")
		}
		if output.Len() > MaxText {
			return errors.New("HTML text too large")
		}
		if n.Type == html.TextNode {
			output.WriteString(n.Data)
			return nil
		}
		if n.Type == html.ElementNode {
			switch n.Data {
			case "script", "style", "head", "template", "svg", "noscript", "iframe", "object":
				return nil
			}
			if hidden(n) {
				return nil
			}
			if n.Data == "br" || blockElements[n.Data] {
				output.WriteByte('\n')
			}
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			if err := walk(c, depth+1); err != nil {
				return err
			}
		}
		if n.Type == html.ElementNode {
			if blockElements[n.Data] {
				output.WriteByte('\n')
			} else if n.Data == "td" || n.Data == "th" {
				output.WriteByte('\t')
			}
		}
		return nil
	}
	if err = walk(root, 0); err != nil {
		return "", err
	}
	// Whitespace in bank HTML is presentation, not part of merchant names.
	lines := []string{}
	for _, line := range strings.Split(output.String(), "\n") {
		line = strings.Join(strings.Fields(line), " ")
		if line != "" {
			lines = append(lines, line)
		}
	}
	return strings.Join(lines, "\n"), nil
}

func hidden(n *html.Node) bool {
	for _, a := range n.Attr {
		style := strings.ToLower(strings.Join(strings.Fields(a.Val), ""))
		switch {
		case a.Key == "hidden",
			a.Key == "aria-hidden" && strings.EqualFold(a.Val, "true"),
			a.Key == "style" && (strings.Contains(style, "display:none") || strings.Contains(style, "visibility:hidden")):
			return true
		}
	}
	return false
}

// cleanText repairs text for matching. Bytes that aren't UTF-8 (some banks
// send Latin-1 non-breaking spaces in "UTF-8" mail) are read as Latin-1,
// non-breaking and other Unicode spaces become plain spaces, because a
// pattern's \s only matches ASCII whitespace, and line endings become \n,
// so a pattern's end of line works.
func cleanText(s string) string {
	s = strings.NewReplacer("\r\n", "\n", "\r", "\n").Replace(s)
	if !utf8.ValidString(s) {
		var b strings.Builder
		for i := 0; i < len(s); {
			r, size := utf8.DecodeRuneInString(s[i:])
			if r == utf8.RuneError && size == 1 {
				r = rune(s[i])
			}
			b.WriteRune(r)
			i += size
		}
		s = b.String()
	}
	return strings.Map(func(r rune) rune {
		if r != '\n' && r != '\t' && unicode.IsSpace(r) {
			return ' '
		}
		return r
	}, s)
}
