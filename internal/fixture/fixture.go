// Package fixture holds the handwritten synthetic emails, statements and PDFs
// the tests use. None of it is, or is derived from, real financial mail.
package fixture

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"os/exec"
	"strings"
	"testing"

	"github.com/audemed44/ledger/internal/alerts"
)

// AlertBody is an alert AlertParser reads as INR 1,234.56 on card 4242.
const AlertBody = "INR 1,234.56 at Example Shop card 4242 on 2026-10-01"

// AlertParser reads AlertBody from alerts@example.invalid.
func AlertParser() alerts.Parser {
	return alerts.Parser{
		Name:       "Example Bank",
		Sender:     "alerts@example.invalid",
		Subject:    "^Alert$",
		Pattern:    `(?P<currency>[A-Z]{3}) (?P<amount>[\d,.]+) at (?P<merchant>.+) card (?P<account>\d{4}) on (?P<date>\d{4}-\d{2}-\d{2})`,
		DateLayout: "2006-01-02",
		Timezone:   "Asia/Kolkata",
		Currency:   "INR",
		Direction:  "debit",
		Enabled:    true,
	}
}

// Mail is a plain-text alert email with the given Message-ID local part.
func Mail(id, body string) []byte {
	return []byte("From: Example <alerts@example.invalid>\r\nSubject: Alert\r\nMessage-ID: <" + id +
		"@example.invalid>\r\nContent-Type: text/plain; charset=utf-8\r\n\r\n" + body)
}

// DatedMail is Mail(id, AlertBody) with a Date header.
func DatedMail(id, date string) []byte {
	return append([]byte("Date: "+date+"\r\n"), Mail(id, AlertBody)...)
}

// HTMLMail is an HTML-only alert email.
func HTMLMail(id, body string) []byte {
	return []byte("From: alerts@example.invalid\r\nSubject: Alert\r\nMessage-ID: <" + id +
		"@example.invalid>\r\nContent-Type: text/html; charset=utf-8\r\n\r\n" + body)
}

// Statement mirrors the structural quirks of an HDFC credit card statement
// as pdftotext -layout extracts it, including ₹ coming out as "C".
const Statement = `HDFC Tata Neu Credit Card Statement
Credit Card No. 111111xxxxxx4242
Statement Date 02 Oct, 2026
PAYMENTS/CREDITS PURCHASES/DEBIT
PREVIOUS STATEMENT DUES FINANCE CHARGES TOTAL AMOUNT DUE
C1,000.00
- C600.00 + C550.00 + C0.00 = C950.00
TOTAL CREDIT LIMIT
AVAILABLE CREDIT LIMIT MINIMUM DUE DUE DATE
C50.00 22 Oct, 2026
Domestic Transactions
DATE & TIME TRANSACTION DESCRIPTION Base NeuCoins* AMOUNT PI
01/10/2026| 12:10   EXAMPLE SHOP                  C 500.00 l
01/10/2026| 14:10   EXAMPLE GROCER                C 50.00 l
HDFC Tata Neu Credit Card Statement
DATE & TIME TRANSACTION DESCRIPTION Base NeuCoins* AMOUNT PI
01/10/2026| 16:10   CREDIT CARD PAYMENT (Ref# 123456)       + C 600.00 l
`

// StatementMail is an email with one PDF attachment per text.
func StatementMail(key string, texts ...string) []byte {
	var b strings.Builder
	fmt.Fprintf(&b, "From: statements@example.invalid\r\nDate: Fri, 02 Oct 2026 12:00:00 +0530\r\n"+
		"Subject: Statement\r\nMessage-ID: <%s@example.invalid>\r\nMIME-Version: 1.0\r\n"+
		"Content-Type: multipart/mixed; boundary=x\r\n\r\n", key)
	for _, text := range texts {
		b.WriteString("--x\r\nContent-Type: application/pdf\r\n" +
			"Content-Disposition: attachment; filename=statement.pdf\r\n" +
			"Content-Transfer-Encoding: base64\r\n\r\n")
		b.WriteString(base64.StdEncoding.EncodeToString(PDF(text)))
		b.WriteString("\r\n")
	}
	b.WriteString("--x--\r\n")
	return []byte(b.String())
}

// PDF builds a tiny valid one-page PDF of the text, so no real PDFs or PDF
// generator dependency are needed.
func PDF(text string) []byte {
	var body strings.Builder
	body.WriteString("BT /F1 9 Tf 40 800 Td 12 TL\n")
	for _, line := range strings.Split(text, "\n") {
		line = strings.NewReplacer(`\`, `\\`, "(", `\(`, ")", `\)`).Replace(line)
		fmt.Fprintf(&body, "(%s) Tj T*\n", line)
	}
	body.WriteString("ET\n")
	objects := []string{
		"<< /Type /Catalog /Pages 2 0 R >>",
		"<< /Type /Pages /Kids [3 0 R] /Count 1 >>",
		"<< /Type /Page /Parent 2 0 R /MediaBox [0 0 1000 1100] /Resources << /Font << /F1 4 0 R >> >> /Contents 5 0 R >>",
		"<< /Type /Font /Subtype /Type1 /BaseFont /Courier >>",
		fmt.Sprintf("<< /Length %d >>\nstream\n%sendstream", body.Len(), body.String()),
	}
	var out bytes.Buffer
	out.WriteString("%PDF-1.4\n")
	offsets := []int{0}
	for i, obj := range objects {
		offsets = append(offsets, out.Len())
		fmt.Fprintf(&out, "%d 0 obj\n%s\nendobj\n", i+1, obj)
	}
	xref := out.Len()
	fmt.Fprintf(&out, "xref\n0 %d\n0000000000 65535 f \n", len(offsets))
	for _, offset := range offsets[1:] {
		fmt.Fprintf(&out, "%010d 00000 n \n", offset)
	}
	fmt.Fprintf(&out, "trailer\n<< /Size %d /Root 1 0 R >>\nstartxref\n%d\n%%%%EOF\n", len(offsets), xref)
	return out.Bytes()
}

// RequirePDFTools skips the test when qpdf or pdftotext isn't installed.
func RequirePDFTools(t *testing.T) {
	t.Helper()
	for _, tool := range []string{"qpdf", "pdftotext"} {
		if _, e := exec.LookPath(tool); e != nil {
			t.Skip(tool + " required for PDF integration tests")
		}
	}
}
