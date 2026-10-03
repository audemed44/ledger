package fixture

import (
	"bytes"
	"crypto/aes"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

// SmartStatementBank imitates HDFC's SmartStatement server: a password form,
// a token, the statement page AES-encrypted in a Data() call with a Save as
// PDF button, and the PDF. Job key "expired" answers 401, as old links do.
type SmartStatementBank struct {
	*httptest.Server
	// Hits counts requests, to check what was (not) fetched.
	Hits atomic.Int32
}

// SmartStatementLink is the link an email from bank would carry.
func (b *SmartStatementBank) SmartStatementLink(jobKey string) string {
	return b.URL + "/HDFCRestFulService/GetStatement.jsp?jobkey=" + jobKey + "&utm_source=email"
}

// NewSmartStatementBank serves pdf to whoever sends password.
func NewSmartStatementBank(t *testing.T, password string, pdf []byte) *SmartStatementBank {
	t.Helper()
	b := &SmartStatementBank{}
	const token, session = "TOKEN0123456789", "session-cookie"
	mux := http.NewServeMux()
	mux.HandleFunc("/HDFCRestFulService/GetStatement.jsp", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("jobkey") == "expired" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		http.SetCookie(w, &http.Cookie{Name: "JSESSIONID", Value: session})
		fmt.Fprintf(w, `<form action="./webresources/app/htmlformat" method="post">
<input type="hidden" name ="ke" id="ke" value='%s'/>
<input type="hidden" name ="seqence" id="seqence" value='123456789'/>
<input type ="password" name="pwd" id="pwd"/></form>`, r.URL.Query().Get("jobkey"))
	})
	mux.HandleFunc("/HDFCRestFulService/CRSGetToken", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(token))
	})
	mux.HandleFunc("/HDFCRestFulService/webresources/app/htmlformat", func(w http.ResponseWriter, r *http.Request) {
		r.ParseForm()
		if c, err := r.Cookie("JSESSIONID"); err != nil || c.Value != session || decrypt(r.Form.Get("pwd")) != token+password {
			w.Write([]byte("<html>Please enter the valid password</html>"))
			return
		}
		page := fmt.Sprintf(`<button type="submit" id="P_PRINT_BUTTON" value="Save as PDF" formaction="%s/HDFCRestFulService/webresources/app/pdfformat?jobkey=%s&amp;reqid=987654321&amp;format=pdf" formmethod="post">Save as PDF</button>`,
			b.URL, r.Form.Get("ke"))
		fmt.Fprintf(w, `<script>Data("MTIzNF5eXl5eXl5eXl5eXg==","%s");</script>`, encryptECB([]byte("1234^^^^^^^^^^^^"), page))
	})
	mux.HandleFunc("/HDFCRestFulService/webresources/app/pdfformat", func(w http.ResponseWriter, r *http.Request) {
		if c, err := r.Cookie("JSESSIONID"); err != nil || c.Value != session || r.URL.Query().Get("reqid") != "987654321" {
			w.WriteHeader(http.StatusForbidden)
			return
		}
		w.Header().Set("Content-Type", "application/pdf")
		w.Header().Set("Content-Disposition", `attachment; filename="XXXXXXXX4242_16Sep2026_TO_15Oct2026.pdf"`)
		w.Write(pdf)
	})
	b.Server = httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b.Hits.Add(1)
		mux.ServeHTTP(w, r)
	}))
	t.Cleanup(b.Close)
	return b
}

// decrypt reverses the page's encrypt.js, as the bank does.
func decrypt(text string) string {
	raw, err := hex.DecodeString(text)
	if err != nil || len(raw) < 1 {
		return ""
	}
	const key = "toUpperCase"
	var out strings.Builder
	previous := int(raw[0])
	for i, c := range raw[1:] {
		v := int(c) ^ int(key[i%len(key)])
		out.WriteByte(byte(((v-previous)%255 + 255) % 255))
		previous = int(c)
	}
	return out.String()
}

// encryptECB is AES-128-ECB with PKCS#7 padding, base64-encoded.
func encryptECB(key []byte, text string) string {
	block, _ := aes.NewCipher(key)
	pad := block.BlockSize() - len(text)%block.BlockSize()
	data := append([]byte(text), bytes.Repeat([]byte{byte(pad)}, pad)...)
	for i := 0; i < len(data); i += block.BlockSize() {
		block.Encrypt(data[i:], data[i:])
	}
	return base64.StdEncoding.EncodeToString(data)
}

// SmartStatementMail is an HDFC statement email with only a link.
func SmartStatementMail(key, link string) []byte {
	return []byte("From: HDFC Bank <hdfcbanksmartstatement@example.invalid>\r\nSubject: Email Account Statement\r\n" +
		"Message-ID: <" + key + "@example.invalid>\r\nMIME-Version: 1.0\r\nContent-Type: text/html; charset=utf-8\r\n\r\n" +
		`<p>Dear Customer, your statement is ready. <a href="` + strings.ReplaceAll(link, "&", "&amp;") + `">View your SmartStatement</a></p>`)
}
