package ledger

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	_ "github.com/emersion/go-message/charset"
	"github.com/emersion/go-message/mail"
	"golang.org/x/net/html"
)

const maxText = 1 << 20
const statementReason = "PDF statement — review and import"

type Attachment struct {
	Part        int    `json:"part"`
	Name        string `json:"name"`
	ContentType string `json:"content_type"`
}

// Decode both mail formats; prefer real plain text when an alternative exists.
// HTML becomes inert text, never browser-rendered markup or fetched resources.
func decodeMail(raw []byte, m Message) Message {
	m.Body, m.Reason = "", ""
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
			b, e := io.ReadAll(io.LimitReader(part.Body, maxText+1))
			if e != nil || len(b) > maxText {
				m.Reason = "MIME: text part too large or unreadable"
				continue
			}
			target := &plain
			if kind == "text/html" {
				target = &rich
			}
			if target.Len()+len(b) > maxText {
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
			if kind == "application/pdf" || strings.HasSuffix(strings.ToLower(name), ".pdf") {
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
	if m.Reason == "" {
		if m.HasPDF {
			m.Reason = statementReason
		} else if strings.TrimSpace(m.Body) == "" {
			m.Reason = "MIME: no readable email text; original archived"
		}
	}
	m.CanParse = m.Reason == "" && strings.TrimSpace(m.Body) != ""
	return m
}

func htmlText(source string) (string, error) {
	root, err := html.Parse(strings.NewReader(source))
	if err != nil {
		return "", err
	}
	var output strings.Builder
	blocks := map[string]bool{"p": true, "div": true, "tr": true, "table": true, "h1": true, "h2": true, "h3": true, "h4": true, "h5": true, "h6": true, "li": true, "ul": true, "ol": true, "section": true, "article": true, "header": true, "footer": true, "blockquote": true, "pre": true}
	var walk func(*html.Node, int) error
	walk = func(n *html.Node, depth int) error {
		if depth > 256 {
			return errors.New("HTML too deeply nested")
		}
		if output.Len() > maxText {
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
			for _, a := range n.Attr {
				style := strings.ToLower(strings.Join(strings.Fields(a.Val), ""))
				if a.Key == "hidden" || (a.Key == "aria-hidden" && strings.EqualFold(a.Val, "true")) || (a.Key == "style" && (strings.Contains(style, "display:none") || strings.Contains(style, "visibility:hidden"))) {
					return nil
				}
			}
			if n.Data == "br" || blocks[n.Data] {
				output.WriteByte('\n')
			}
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			if err := walk(c, depth+1); err != nil {
				return err
			}
		}
		if n.Type == html.ElementNode {
			if blocks[n.Data] {
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

var archiveName = regexp.MustCompile(`^[a-f0-9]{64}\.eml$`)

func (s *Store) archivedRaw(id int64) ([]byte, error) {
	var name string
	if err := s.DB.QueryRow("SELECT archive FROM messages WHERE id=?", id).Scan(&name); err != nil {
		return nil, err
	}
	if !archiveName.MatchString(name) {
		return nil, errors.New("invalid archive reference")
	}
	f, err := os.Open(filepath.Join(s.Dir, "archive", name))
	if err != nil {
		return nil, err
	}
	defer f.Close()
	raw, err := io.ReadAll(io.LimitReader(f, maxMail+1))
	if err != nil {
		return nil, err
	}
	if len(raw) > maxMail {
		return nil, errors.New("archive too large")
	}
	return raw, nil
}
func (s *Store) archivedMessage(id int64) (Message, error) {
	raw, err := s.archivedRaw(id)
	if err != nil {
		return Message{}, err
	}
	return decodeMail(raw, Message{ID: id}), nil
}

func (s *Store) ReviewMessage(id int64) (Message, error) {
	m, err := s.Message(id)
	if err != nil {
		return m, err
	}
	decoded, err := s.archivedMessage(id)
	if err != nil {
		m.ContentError = "The archived email could not be read. Check the archive volume or restore it from backup."
		return m, nil
	}
	m.Body, m.BodyFormat = decoded.Body, decoded.BodyFormat
	m.Attachments, m.HasPDF, m.CanParse = decoded.Attachments, decoded.HasPDF, decoded.CanParse
	if strings.HasPrefix(decoded.Reason, "MIME:") {
		m.ContentError = decoded.Reason
	}
	return m, nil
}

// One-time recovery from immutable archives for pre-HTML deployments. Updates
// queued bodies only: cursor, IDs, imported transactions and archives stay intact.
// Individual missing archives remain visible instead of preventing app startup.
func (s *Store) recoverArchivedText() error {
	const version = "html-text-pdf-v2"
	saved, err := s.Setting("mail-text-version")
	if err != nil || saved == version {
		return err
	}
	if err = s.recoverQueuedText(true); err != nil {
		return err
	}
	return s.SetSetting("mail-text-version", version)
}
func (s *Store) recoverQueuedText(all bool) error {
	var last int64
	for {
		query := "SELECT id FROM messages WHERE state='queued' AND id>?"
		if !all {
			query += " AND (body='' OR reason LIKE 'MIME:%')"
		}
		query += " ORDER BY id LIMIT 100"
		rows, err := s.DB.Query(query, last)
		if err != nil {
			return err
		}
		ids := []int64{}
		for rows.Next() {
			var id int64
			if err = rows.Scan(&id); err != nil {
				rows.Close()
				return err
			}
			ids = append(ids, id)
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return err
		}
		if len(ids) == 0 {
			return nil
		}
		for _, id := range ids {
			last = id
			decoded, e := s.archivedMessage(id)
			reason := decoded.Reason
			if e != nil {
				reason = "MIME: archive could not be read; restore it and retry"
			} else if reason == "" {
				reason = "No matching alert parser"
			}
			if e != nil {
				_, err = s.DB.Exec("UPDATE messages SET reason=? WHERE id=? AND state='queued'", reason, id)
			} else {
				_, err = s.DB.Exec("UPDATE messages SET body=?,reason=?,has_pdf=? WHERE id=? AND state='queued'", decoded.Body, reason, decoded.HasPDF, id)
			}
			if err != nil {
				return fmt.Errorf("recover archived text: %w", err)
			}
		}
	}
}
