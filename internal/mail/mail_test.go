package mail

import (
	"strings"
	"testing"

	"github.com/audemed44/ledger/internal/fixture"
)

func TestMIMEAlternativePrefersPlainAndDecodesTransferEncoding(t *testing.T) {
	raw := []byte("From: alerts@example.invalid\r\nSubject: Alert\r\nMIME-Version: 1.0\r\n" +
		"Content-Type: multipart/alternative; boundary=x\r\n\r\n" +
		"--x\r\nContent-Type: text/plain\r\nContent-Transfer-Encoding: quoted-printable\r\n\r\nINR=201.00 at Plain Shop\r\n" +
		"--x\r\nContent-Type: text/html\r\n\r\n<p>INR 1.00 at HTML Shop</p>\r\n--x--\r\n")
	m := Decode(raw)
	if m.BodyFormat != "plain" || !strings.Contains(m.Body, "INR 1.00 at Plain Shop") || strings.Contains(m.Body, "HTML Shop") {
		t.Fatalf("%+v", m)
	}
	base64 := []byte("Content-Type: text/html\r\nContent-Transfer-Encoding: base64\r\n\r\nPHA+SU5SIDEuMDA8L3A+")
	m = Decode(base64)
	if m.Body != "INR 1.00" || !m.CanParse {
		t.Fatalf("%+v", m)
	}
}

func TestOversizedTextRemainsQueued(t *testing.T) {
	m := Decode(fixture.HTMLMail("large", strings.Repeat("a", MaxText+1)))
	if m.CanParse || !strings.HasPrefix(m.Reason, "MIME:") {
		t.Fatal("oversized text accepted")
	}
}

func TestSenderWithEmptyEncodedName(t *testing.T) {
	m := Decode([]byte("From: =?UTF-8?B??= <noreply@example.invalid>\r\nSubject: Alert\r\n\r\nINR 1.00"))
	if m.Sender != "noreply@example.invalid" {
		t.Fatalf("%q", m.Sender)
	}
	if m = Decode([]byte("From: =?UTF-8?B??= <a@example.invalid>, <b@example.invalid>\r\n\r\nx")); m.Sender != "" {
		t.Fatal("ambiguous sender accepted", m.Sender)
	}
}

// Axis declares UTF-8 but sends a Latin-1 non-breaking space, with CRLF
// line endings: the text is repaired so patterns can match it.
func TestTextRepairsLatin1SpacesAndLineEndings(t *testing.T) {
	m := Decode([]byte("From: alerts@example.invalid\r\nContent-Type: text/plain; charset=utf-8\r\n" +
		"Content-Transfer-Encoding: quoted-printable\r\n\r\nAmount: INR=A0537\r\nCard: XX4242 \r\nPaid=C2=A0to: Shop\r\n"))
	if m.Body != "Amount: INR 537\nCard: XX4242\nPaid to: Shop\n\n" {
		t.Fatalf("%q", m.Body)
	}
}
