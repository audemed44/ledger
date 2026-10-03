// Package smartstatement downloads HDFC Bank account statements from the
// "View your SmartStatement" link HDFC emails instead of a PDF. It does what
// the bank's page does in a browser, with plain HTTP:
//
//  1. GET the link: a password form with the job key and a sequence number.
//  2. GET CRSGetToken?jobkey=…: a one-time token.
//  3. POST the form, with the password field set to encrypt(token +
//     password) (the page's own encrypt.js, ported below).
//  4. The response holds the statement page, AES-128-ECB encrypted with a
//     key it carries; decrypted, its "Save as PDF" button names a pdfformat
//     URL.
//  5. POST that URL: the bank's PDF of the statement.
//
// Links expire after about three months; the server then answers 401.
package smartstatement

import (
	"bytes"
	"context"
	"crypto/aes"
	"encoding/base64"
	"errors"
	"fmt"
	"html"
	"io"
	"math/rand/v2"
	"mime"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"regexp"
	"strings"
	"time"
)

// Host is the only host links may point at, so a forged email can't make
// Ledger send a password elsewhere.
const Host = "smartstatements.hdfc.bank.in"

// statementPath is the link's path.
const statementPath = "/HDFCRestFulService/GetStatement.jsp"

// MaxPDF bounds the downloaded PDF.
const MaxPDF = 18 << 20

// ErrExpired is returned when the bank no longer serves the link.
var ErrExpired = errors.New("the statement link has expired (HDFC keeps them for about three months)")

// ErrPassword is returned when the bank doesn't accept the password.
var ErrPassword = errors.New("the bank didn't accept the password")

// IsLink reports whether u is an HDFC SmartStatement link on host.
func IsLink(u *url.URL, host string) bool {
	return u.Scheme == "https" && strings.EqualFold(u.Host, host) && u.Path == statementPath && u.Query().Get("jobkey") != ""
}

// Fetcher downloads statements. Host is normally the constant Host; tests
// point it at their own server.
type Fetcher struct {
	Host   string
	Client *http.Client
}

// New returns a fetcher for HDFC's server.
func New() *Fetcher { return &Fetcher{Host: Host, Client: &http.Client{}} }

var (
	inputTag  = regexp.MustCompile(`(?is)<input[^>]*>`)
	idAttr    = regexp.MustCompile(`(?i)\bid\s*=\s*["']([^"']*)["']`)
	valueAttr = regexp.MustCompile(`(?i)\bvalue\s*=\s*(["'])(.*?)["']`)
	payload   = regexp.MustCompile(`Data\("([A-Za-z0-9+/=]+)"\s*,\s*"([^"]+)"\)`)
	pdfButton = regexp.MustCompile(`(?is)id="P_PRINT_BUTTON"[^>]*formaction="([^"]+)"`)
)

// browser is the User-Agent the bank's server expects; without one it
// answers with an empty page.
const browser = "Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/130.0 Safari/537.36"

// Fetch downloads the statement PDF behind link with password. It returns
// the PDF and the file name the bank gives it.
func (f *Fetcher) Fetch(ctx context.Context, link, password string) ([]byte, string, error) {
	u, err := url.Parse(link)
	if err != nil || !IsLink(u, f.Host) {
		return nil, "", errors.New("not an HDFC SmartStatement link")
	}
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	jar, _ := cookiejar.New(nil)
	client := *f.Client
	client.Jar = jar
	client.CheckRedirect = func(r *http.Request, _ []*http.Request) error {
		if !strings.EqualFold(r.URL.Host, f.Host) || r.URL.Scheme != "https" {
			return errors.New("the bank redirected elsewhere")
		}
		return nil
	}
	do := func(method, target string, body []byte, contentType string, limit int64) ([]byte, http.Header, error) {
		r, err := http.NewRequestWithContext(ctx, method, target, bytes.NewReader(body))
		if err != nil {
			return nil, nil, err
		}
		r.Header.Set("User-Agent", browser)
		if contentType != "" {
			r.Header.Set("Content-Type", contentType)
		}
		resp, err := client.Do(r)
		if err != nil {
			return nil, nil, fmt.Errorf("could not reach the bank: %w", err)
		}
		defer resp.Body.Close()
		if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusNotFound || resp.StatusCode == http.StatusGone {
			return nil, nil, ErrExpired
		}
		if resp.StatusCode != http.StatusOK {
			return nil, nil, fmt.Errorf("the bank answered %s", resp.Status)
		}
		b, err := io.ReadAll(io.LimitReader(resp.Body, limit+1))
		if err != nil {
			return nil, nil, err
		}
		if int64(len(b)) > limit {
			return nil, nil, errors.New("the bank's answer was unexpectedly large")
		}
		return b, resp.Header, nil
	}

	page, _, err := do("GET", link, nil, "", 1<<20)
	if err != nil {
		return nil, "", err
	}
	fields := map[string]string{}
	for _, tag := range inputTag.FindAllString(string(page), -1) {
		id, value := idAttr.FindStringSubmatch(tag), valueAttr.FindStringSubmatch(tag)
		if id != nil && value != nil {
			fields[id[1]] = html.UnescapeString(value[2])
		}
	}
	jobKey, sequence := fields["ke"], fields["seqence"]
	if jobKey == "" || sequence == "" {
		return nil, "", ErrExpired
	}
	base := u.ResolveReference(&url.URL{Path: "./"})
	token, _, err := do("GET", base.ResolveReference(&url.URL{Path: "CRSGetToken", RawQuery: "jobkey=" + url.QueryEscape(jobKey)}).String(), nil, "", 4<<10)
	if err != nil {
		return nil, "", err
	}
	form := url.Values{"ke": {jobKey}, "seqence": {sequence}, "pwd": {Encrypt(strings.TrimSpace(string(token))+password, rand.IntN(255)+1)}}
	statement, _, err := do("POST", base.ResolveReference(&url.URL{Path: "webresources/app/htmlformat"}).String(),
		[]byte(form.Encode()), "application/x-www-form-urlencoded", 16<<20)
	if err != nil {
		return nil, "", err
	}
	m := payload.FindSubmatch(statement)
	if m == nil {
		return nil, "", ErrPassword
	}
	decrypted, err := decryptPage(string(m[1]), string(m[2]))
	if err != nil {
		return nil, "", err
	}
	button := pdfButton.FindStringSubmatch(decrypted)
	if button == nil {
		return nil, "", errors.New("the statement page has no Save as PDF button")
	}
	target, err := url.Parse(html.UnescapeString(button[1]))
	if err != nil || !strings.EqualFold(target.Host, f.Host) || target.Scheme != "https" {
		return nil, "", errors.New("the statement's PDF link points somewhere unexpected")
	}
	pdf, header, err := do("POST", target.String(), nil, "text/plain", MaxPDF)
	if err != nil {
		return nil, "", err
	}
	if !bytes.HasPrefix(pdf, []byte("%PDF-")) {
		return nil, "", errors.New("the bank didn't send a PDF")
	}
	name := "hdfc-statement.pdf"
	if _, params, err := mime.ParseMediaType(header.Get("Content-Disposition")); err == nil && params["filename"] != "" {
		name = params["filename"]
	}
	return pdf, name, nil
}

// key is encrypt.js's key: the page reuses the string "toUpperCase".
const key = "toUpperCase"

// Encrypt is the page's encrypt.js: a seed byte (1–255), then each
// character plus the previous output byte, modulo 255, XORed with the key;
// upper-case hex.
func Encrypt(text string, seed int) string {
	var b strings.Builder
	fmt.Fprintf(&b, "%02X", seed)
	previous := seed
	for i := 0; i < len(text); i++ {
		v := (int(text[i]) + previous) % 255
		v ^= int(key[i%len(key)])
		fmt.Fprintf(&b, "%02X", v)
		previous = v
	}
	return b.String()
}

// decryptPage reverses the page's Data(key, text): AES-128-ECB with a
// base64 key, PKCS#7 padding, over base64 text.
func decryptPage(base64Key, text string) (string, error) {
	k, err := base64.StdEncoding.DecodeString(base64Key)
	if err != nil {
		return "", errors.New("unreadable statement key")
	}
	data, err := base64.StdEncoding.DecodeString(strings.ReplaceAll(text, `\n`, ""))
	if err != nil {
		return "", errors.New("unreadable statement data")
	}
	block, err := aes.NewCipher(k)
	if err != nil || len(data) == 0 || len(data)%block.BlockSize() != 0 {
		return "", errors.New("unreadable statement data")
	}
	out := make([]byte, len(data))
	for i := 0; i < len(data); i += block.BlockSize() {
		block.Decrypt(out[i:], data[i:])
	}
	pad := int(out[len(out)-1])
	if pad == 0 || pad > block.BlockSize() || pad > len(out) {
		return "", errors.New("unreadable statement data")
	}
	return string(out[:len(out)-pad]), nil
}
