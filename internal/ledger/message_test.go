package ledger

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func htmlMail(id, body string) []byte {
	return []byte("From: alerts@example.invalid\r\nSubject: Alert\r\nMessage-ID: <" + id + "@example.invalid>\r\nContent-Type: text/html; charset=utf-8\r\n\r\n" + body)
}
func TestHTMLMailIsReadableAndParseable(t *testing.T) {
	s := testStore(t)
	body := `<html><head><title>Not the message</title><style>.a{color:red}</style></head><body><div hidden>Hidden decoy INR 99.00</div><div style="display: none">Hide this</div><script>fetch("https://evil.invalid")</script><p>INR&nbsp;<b>1,234.56</b> at Example &amp; Shop card 4242 on 2026-10-01</p><img src="https://tracker.invalid/pixel"><table><tr><td>Reference</td><td>FAKE-01</td></tr></table></body></html>`
	id, e := s.Ingest(htmlMail("html", body))
	if e != nil {
		t.Fatal(e)
	}
	m, e := s.ReviewMessage(id)
	if e != nil {
		t.Fatal(e)
	}
	if !m.CanParse || m.BodyFormat != "html" || !strings.Contains(m.Body, "INR 1,234.56 at Example & Shop") || !strings.Contains(m.Body, "Reference FAKE-01") {
		t.Fatalf("unexpected review: %+v", m)
	}
	for _, bad := range []string{"Hidden", "Hide this", "fetch", "tracker.invalid", "<html>", "Not the message"} {
		if strings.Contains(m.Body, bad) {
			t.Errorf("included %s", bad)
		}
	}
	if _, e = s.SaveParser(testParser()); e != nil {
		t.Fatal(e)
	}
	if _, e = s.Reprocess(); e != nil {
		t.Fatal(e)
	}
	rows, e := s.Transactions(Filter{})
	if e != nil || len(rows) != 1 || rows[0].Merchant != "Example & Shop" {
		t.Fatalf("%+v %v", rows, e)
	}
}
func TestMIMEAlternativePrefersPlainAndDecodesTransferEncoding(t *testing.T) {
	raw := []byte("From: alerts@example.invalid\r\nSubject: Alert\r\nMIME-Version: 1.0\r\nContent-Type: multipart/alternative; boundary=x\r\n\r\n--x\r\nContent-Type: text/plain\r\nContent-Transfer-Encoding: quoted-printable\r\n\r\nINR=201.00 at Plain Shop\r\n--x\r\nContent-Type: text/html\r\n\r\n<p>INR 1.00 at HTML Shop</p>\r\n--x--\r\n")
	m := decodeMail(raw, Message{})
	if m.BodyFormat != "plain" || !strings.Contains(m.Body, "INR 1.00 at Plain Shop") || strings.Contains(m.Body, "HTML Shop") {
		t.Fatalf("%+v", m)
	}
	base64 := []byte("Content-Type: text/html\r\nContent-Transfer-Encoding: base64\r\n\r\nPHA+SU5SIDEuMDA8L3A+")
	m = decodeMail(base64, Message{})
	if m.Body != "INR 1.00" || !m.CanParse {
		t.Fatalf("%+v", m)
	}
}
func TestInlinePDFIsNotAnAlert(t *testing.T) {
	s := testStore(t)
	raw := []byte("From: alerts@example.invalid\r\nSubject: Alert\r\nMIME-Version: 1.0\r\nContent-Type: multipart/mixed; boundary=x\r\n\r\n--x\r\nContent-Type: text/html\r\n\r\n<p>" + testBody + "</p>\r\n--x\r\nContent-Type: application/pdf\r\nContent-Disposition: inline\r\n\r\n%PDF-synthetic\r\n--x--\r\n")
	id, e := s.Ingest(raw)
	if e != nil {
		t.Fatal(e)
	}
	m, e := s.ReviewMessage(id)
	if e != nil || !m.HasPDF || m.CanParse || len(m.Attachments) != 1 || m.Attachments[0].Part != 1 || m.Body == "" {
		t.Fatalf("%+v %v", m, e)
	}
	s.SaveParser(testParser())
	s.Reprocess()
	rows, _ := s.Transactions(Filter{})
	if len(rows) > 0 {
		t.Fatal("inline PDF imported as alert")
	}
}
func TestRecoverLegacyHTMLArchivesOnOpen(t *testing.T) {
	dir := t.TempDir()
	s, e := Open(dir)
	if e != nil {
		t.Fatal(e)
	}
	ids := []int64{}
	for i := range 205 {
		id, e := s.Ingest(htmlMail(fmt.Sprint(i), "<p>"+testBody+"</p>"))
		if e != nil {
			t.Fatal(e)
		}
		ids = append(ids, id)
	}
	// Simulate the old release: messages archived with no extracted body.
	s.DB.Exec("UPDATE messages SET body='',reason='MIME: no plain-text body; original archived'")
	s.DB.Exec("DELETE FROM settings WHERE key='mail-text-version'")
	s.SetSetting("cursor:test", "unchanged")
	s.DB.Close()
	s, e = Open(dir)
	if e != nil {
		t.Fatal(e)
	}
	defer s.DB.Close()
	var recovered int
	s.DB.QueryRow("SELECT count(*) FROM messages WHERE body<>'' AND reason='No matching alert parser'").Scan(&recovered)
	if recovered != 205 {
		t.Fatalf("recovered %d", recovered)
	}
	v, _ := s.Setting("cursor:test")
	if v != "unchanged" {
		t.Fatal("cursor changed")
	}
	if _, e = s.SaveParser(testParser()); e != nil {
		t.Fatal(e)
	}
	if _, e = s.Reprocess(); e != nil {
		t.Fatal(e)
	}
	var count int
	s.DB.QueryRow("SELECT count(*) FROM transactions").Scan(&count)
	if count != 205 {
		t.Fatal(count)
	}
	for _, id := range ids {
		m, e := s.Message(id)
		if e != nil || m.State != "parsed" {
			t.Fatal("message ID/state lost")
		}
	}
	if _, e = s.Reprocess(); e != nil {
		t.Fatal(e)
	}
	s.DB.QueryRow("SELECT count(*) FROM transactions").Scan(&count)
	if count != 205 {
		t.Fatal("duplicates")
	}
}
func TestMissingArchiveHasVisibleErrorAndCannotEscape(t *testing.T) {
	s := testStore(t)
	id, e := s.Ingest(htmlMail("missing", "<p>hello</p>"))
	if e != nil {
		t.Fatal(e)
	}
	var name string
	s.DB.QueryRow("SELECT archive FROM messages WHERE id=?", id).Scan(&name)
	os.Remove(filepath.Join(s.Dir, "archive", name))
	m, e := s.ReviewMessage(id)
	if e != nil || m.ContentError == "" || m.CanParse {
		t.Fatalf("%+v %v", m, e)
	}
	s.DB.Exec("UPDATE messages SET archive='../ledger.db' WHERE id=?", id)
	if _, e = s.archivedRaw(id); e == nil {
		t.Fatal("archive path escaped")
	}
}
func TestOversizedTextRemainsQueued(t *testing.T) {
	m := decodeMail(htmlMail("large", strings.Repeat("a", maxText+1)), Message{})
	if m.CanParse || !strings.HasPrefix(m.Reason, "MIME:") {
		t.Fatal("oversized text accepted")
	}
}
