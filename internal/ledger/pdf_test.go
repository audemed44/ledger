package ledger

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestPDFConfigNeverExposesPasswords(t *testing.T) {
	s := testStore(t)
	server := &Server{Store: s, Token: "test-token", PDFPasswords: []string{"secret-one", "secret-two"}}
	h := server.Handler()
	request := func(method, path, body string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, path, strings.NewReader(body))
		r.Header.Set("Authorization", "Bearer test-token")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w
	}
	w := request("GET", "/api/pdf-config", "")
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"password_slots":[1,2]`) {
		t.Fatal(w.Body.String())
	}
	if strings.Contains(w.Body.String(), "secret-") {
		t.Fatal("password leaked")
	}
	w = request("POST", "/api/statement-parsers", `{"name":"HDFC Credit Card","adapter":"hdfc-credit-card","password_slot":2}`)
	if w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	var p StatementParser
	json.Unmarshal(w.Body.Bytes(), &p)
	p.Name = "HDFC alternate"
	raw, _ := json.Marshal(p)
	w = request("POST", "/api/statement-parsers", string(raw))
	if w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	list, e := s.StatementParsers()
	if e != nil || len(list) != 1 || list[0].Name != "HDFC alternate" || list[0].PasswordSlot != 2 {
		t.Fatalf("%+v %v", list, e)
	}
	w = request("POST", "/api/statement-parsers", `{"name":"Bad slot","adapter":"hdfc-credit-card","password_slot":3}`)
	if w.Code != 400 {
		t.Fatal("accepted missing password")
	}
	w = request("POST", "/api/statement-parsers", `{"name":"Bad field","adapter":"hdfc-credit-card","password":"secret-one"}`)
	if w.Code != 400 {
		t.Fatal("accepted password into DB")
	}
	r := httptest.NewRequest("GET", "/api/pdf-config", nil)
	w = httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 401 {
		t.Fatal("unauthenticated password metadata")
	}
}

// Construct a tiny valid PDF entirely from synthetic test text; no real PDFs
// or additional test-only PDF generator dependency are needed.
func syntheticPDF(text string) []byte {
	var body strings.Builder
	body.WriteString("BT /F1 9 Tf 40 800 Td 12 TL\n")
	for _, line := range strings.Split(text, "\n") {
		line = strings.NewReplacer(`\`, `\\`, "(", `\(`, ")", `\)`).Replace(line)
		fmt.Fprintf(&body, "(%s) Tj T*\n", line)
	}
	body.WriteString("ET\n")
	objects := []string{"<< /Type /Catalog /Pages 2 0 R >>", "<< /Type /Pages /Kids [3 0 R] /Count 1 >>", "<< /Type /Page /Parent 2 0 R /MediaBox [0 0 1000 1100] /Resources << /Font << /F1 4 0 R >> >> /Contents 5 0 R >>", "<< /Type /Font /Subtype /Type1 /BaseFont /Courier >>", fmt.Sprintf("<< /Length %d >>\nstream\n%sendstream", body.Len(), body.String())}
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
func pdfTools(t *testing.T) {
	t.Helper()
	for _, tool := range []string{"qpdf", "pdftotext"} {
		if _, e := exec.LookPath(tool); e != nil {
			t.Skip(tool + " required for PDF integration tests")
		}
	}
}
func TestPDFPasswordTrialsAndNamedAdapter(t *testing.T) {
	pdfTools(t)
	dir := t.TempDir()
	plain := filepath.Join(dir, "plain.pdf")
	encrypted := filepath.Join(dir, "locked.pdf")
	os.WriteFile(plain, syntheticPDF(syntheticStatement), 0600)
	if out, e := exec.Command("qpdf", "--encrypt", "fixture-two", "fixture-owner", "256", "--", plain, encrypted).CombinedOutput(); e != nil {
		t.Fatalf("encrypt fixture: %v %s", e, out)
	}
	text, slot, e := extractPDFPasswords(t.Context(), encrypted, []string{"fixture-one", "fixture-two", "fixture-three"}, 0)
	if e != nil || slot != 2 || !strings.Contains(text, "EXAMPLE SHOP") {
		t.Fatalf("slot=%d err=%v", slot, e)
	}
	if _, _, e = extractPDFPasswords(t.Context(), encrypted, []string{"fixture-one", "fixture-two"}, 1); e == nil {
		t.Fatal("specific slot fell back to other passwords")
	}
	if _, slot, e = extractPDFPasswords(t.Context(), plain, nil, 0); e != nil || slot != 0 {
		t.Fatalf("unencrypted: %d %v", slot, e)
	}
	cancelled, cancel := context.WithCancel(t.Context())
	cancel()
	if _, _, e = extractPDFPasswords(cancelled, encrypted, []string{"fixture-two"}, 0); e == nil {
		t.Fatal("ignored cancellation")
	}
	raw, _ := os.ReadFile(encrypted)
	mime := []byte("From: statements@example.invalid\r\nSubject: Statement\r\nMIME-Version: 1.0\r\nContent-Type: multipart/mixed; boundary=x\r\n\r\n--x\r\nContent-Type: text/plain\r\n\r\nAttached.\r\n--x\r\nContent-Type: application/pdf\r\nContent-Disposition: attachment; filename=statement.pdf\r\nContent-Transfer-Encoding: base64\r\n\r\n" + base64.StdEncoding.EncodeToString(raw) + "\r\n--x--\r\n")
	s := testStore(t)
	id, e := s.Ingest(mime)
	if e != nil {
		t.Fatal(e)
	}
	p := StatementParser{Name: "HDFC Credit Card", Adapter: "hdfc-credit-card"}
	result, e := s.previewPDF(t.Context(), id, 1, &p, []string{"fixture-one", "fixture-two"})
	if e != nil || result.ParseError != "" || result.Statement == nil || len(result.Statement.Transactions) != 3 || !result.Statement.Balanced || result.ParserName != p.Name {
		t.Fatalf("PDF preview: %v %s", e, result.ParseError)
	}
	rows, _ := s.Transactions(Filter{})
	if len(rows) != 0 {
		t.Fatal("preview imported transactions")
	}
	if _, e = s.previewPDF(t.Context(), id, 0, &p, []string{"fixture-two"}); e == nil {
		t.Fatal("accepted non-PDF MIME part")
	}
}
