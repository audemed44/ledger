package ledger

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"strings"
	"testing"
)

func statementMail(key string, texts ...string) []byte {
	var b strings.Builder
	fmt.Fprintf(&b, "From: statements@example.invalid\r\nDate: Fri, 02 Oct 2026 12:00:00 +0530\r\nSubject: Statement\r\nMessage-ID: <%s@example.invalid>\r\nMIME-Version: 1.0\r\nContent-Type: multipart/mixed; boundary=x\r\n\r\n", key)
	for _, text := range texts {
		b.WriteString("--x\r\nContent-Type: application/pdf\r\nContent-Disposition: attachment; filename=statement.pdf\r\nContent-Transfer-Encoding: base64\r\n\r\n")
		b.WriteString(base64.StdEncoding.EncodeToString(syntheticPDF(text)))
		b.WriteString("\r\n")
	}
	b.WriteString("--x--\r\n")
	return []byte(b.String())
}

func TestStatementImportAPIAndIdempotence(t *testing.T) {
	pdfTools(t)
	s := testStore(t)
	p, err := s.SaveStatementParser(StatementParser{Name: "HDFC Credit Card Parser v1", Adapter: "hdfc-credit-card", BalanceTolerancePaise: 99})
	if err != nil {
		t.Fatal(err)
	}
	id, err := s.Ingest(statementMail("first", syntheticStatement))
	if err != nil {
		t.Fatal(err)
	}
	h := (&Server{Store: s, Token: "1234"}).Handler()
	call := func(method, path, body, token, origin string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, path, strings.NewReader(body))
		r.Header.Set("Authorization", "Bearer "+token)
		if origin != "" {
			r.Header.Set("Origin", origin)
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w
	}
	preview := func(id int64) PDFPreview {
		w := call("POST", fmt.Sprintf("/api/messages/%d/pdf", id), fmt.Sprintf(`{"part":0,"parser_id":%d}`, p.ID), "1234", "")
		if w.Code != 200 {
			t.Fatal(w.Code, w.Body.String())
		}
		var v PDFPreview
		if err := json.Unmarshal(w.Body.Bytes(), &v); err != nil {
			t.Fatal(err)
		}
		return v
	}
	v := preview(id)
	if v.Fingerprint == "" {
		t.Fatal("no importable preview", v.ParseError)
	}
	rows, _ := s.Transactions(Filter{})
	if len(rows) != 0 {
		t.Fatal("preview mutated transactions")
	}
	body := fmt.Sprintf(`{"part":0,"parser_id":%d,"import":true,"fingerprint":%q}`, p.ID, v.Fingerprint)
	path := fmt.Sprintf("/api/messages/%d/pdf", id)
	if w := call("POST", path, body, "", ""); w.Code != 401 {
		t.Fatal("unauthenticated import", w.Code)
	}
	if w := call("POST", path, body, "1234", "https://evil.invalid"); w.Code != 403 {
		t.Fatal("cross-origin import", w.Code)
	}
	if w := call("POST", path, strings.Replace(body, v.Fingerprint, "stale", 1), "1234", ""); w.Code != 409 {
		t.Fatal("stale preview accepted")
	}
	for i := 0; i < 2; i++ {
		if w := call("POST", path, body, "1234", ""); w.Code != 200 {
			t.Fatal(w.Code, w.Body.String())
		}
	}
	rows, _ = s.Transactions(Filter{})
	if len(rows) != 3 {
		t.Fatal("duplicate or missing rows", len(rows))
	}
	for _, r := range rows {
		if r.Status != "confirmed" || r.AccountKind != "card" {
			t.Fatal(r)
		}
	}
	m, _ := s.Message(id)
	if m.State != "statement" {
		t.Fatal("import still in queue", m.State)
	}
	id2, err := s.Ingest(statementMail("resent", syntheticStatement))
	if err != nil {
		t.Fatal(err)
	}
	v2 := preview(id2)
	out, err := s.importStatement(id2, 0, v2, v2.Fingerprint)
	if err != nil || !out.AlreadyImported {
		t.Fatal("resent statement duplicated", err)
	}
	rows, _ = s.Transactions(Filter{})
	if len(rows) != 3 {
		t.Fatal(len(rows))
	}
	// Deleting the parser never removes rows or statement provenance.
	if w := call("DELETE", fmt.Sprintf("/api/statement-parsers/%d", p.ID), "", "1234", ""); w.Code != 200 {
		t.Fatal(w.Code)
	}
	rows, _ = s.Transactions(Filter{})
	if len(rows) != 3 {
		t.Fatal("delete removed transactions")
	}
	if _, err = s.archivedRaw(id); err != nil {
		t.Fatal("delete removed archive", err)
	}
}

func TestStatementImportValidationOverlapAndMultipleAttachments(t *testing.T) {
	s := testStore(t)
	valid, err := ParseHDFCStatement(syntheticStatement)
	if err != nil {
		t.Fatal(err)
	}
	preview := PDFPreview{Statement: &valid, Fingerprint: statementFingerprint(valid)}
	second, err := ParseHDFCStatement(strings.ReplaceAll(syntheticStatement, "4242", "8080"))
	if err != nil {
		t.Fatal(err)
	}
	id, err := s.Ingest(statementMail("two", syntheticStatement, strings.ReplaceAll(syntheticStatement, "4242", "8080")))
	if err != nil {
		t.Fatal(err)
	}
	failed := preview
	failed.ParseError = "missing row"
	if _, err = s.importStatement(id, 0, failed, preview.Fingerprint); err == nil {
		t.Fatal("invalid statement imported")
	}
	if _, err = s.importStatement(id, 0, preview, preview.Fingerprint); err != nil {
		t.Fatal(err)
	}
	m, _ := s.Message(id)
	if m.State != "queued" || !m.HasPDF {
		t.Fatal("other attachment hidden", m)
	}
	if err = s.Process(id); err != nil {
		t.Fatal(err)
	}
	preview2 := PDFPreview{Statement: &second, Fingerprint: statementFingerprint(second)}
	if _, err = s.importStatement(id, 1, preview2, preview2.Fingerprint); err != nil {
		t.Fatal(err)
	}
	rows, _ := s.Transactions(Filter{})
	if len(rows) != 6 {
		t.Fatal("multiple attachments missing", len(rows))
	}
	// Late alerts that could duplicate statement rows remain queued.
	p := testParser()
	p.Issuer = "HDFC"
	p.AccountKind = "card"
	s.SaveParser(p)
	alert, err := s.Ingest(testMail("late-alert", "INR 500.00 at EXAMPLE SHOP card 4242 on 2026-10-01"))
	if err != nil {
		t.Fatal(err)
	}
	m, _ = s.Message(alert)
	if m.State != "queued" || !strings.Contains(m.Reason, "reconciliation") {
		t.Fatal("late alert double counted", m)
	}
	// Different content for the same account/date cannot overwrite the import.
	revision := valid
	revision.TotalDue++
	if _, err = s.importStatement(id, 0, PDFPreview{Statement: &revision}, statementFingerprint(revision)); err == nil {
		t.Fatal("revision silently replaced")
	}
}

func TestStatementImportBlocksEarlierAlertsAtomically(t *testing.T) {
	s := testStore(t)
	p := testParser()
	p.Issuer = "HDFC"
	p.AccountKind = "card"
	s.SaveParser(p)
	s.Ingest(testMail("earlier", "INR 50.00 at DIFFERENT DESCRIPTION card 4242 on 2026-10-01"))
	id, err := s.Ingest(statementMail("overlap", syntheticStatement))
	if err != nil {
		t.Fatal(err)
	}
	st, _ := ParseHDFCStatement(syntheticStatement)
	if _, err = s.importStatement(id, 0, PDFPreview{Statement: &st}, statementFingerprint(st)); err == nil {
		t.Fatal("overlapping statement accepted")
	}
	rows, _ := s.Transactions(Filter{})
	if len(rows) != 1 {
		t.Fatal("partially imported", len(rows))
	}
	var count int
	s.DB.QueryRow("SELECT count(*) FROM statements").Scan(&count)
	if count != 0 {
		t.Fatal("partial statement persisted")
	}
}

func TestPDFPartialRowsAndCardBranding(t *testing.T) {
	text := strings.ReplaceAll(strings.ReplaceAll(syntheticStatement, "Tata Neu", "Regalia"), "Base NeuCoins*", "Reward Points")
	st, err := ParseHDFCStatement(text)
	if err != nil || !st.Balanced {
		t.Fatal("branding blocks shared structure", err)
	}
	text = strings.Replace(text, "C 500.00", "? 500.00", 1)
	st, err = ParseHDFCStatement(text)
	if err == nil || st.Balanced || len(st.Transactions) != 2 {
		t.Fatal("partial rows discarded or treated as validated", len(st.Transactions), err)
	}
	pdfTools(t)
	s := testStore(t)
	id, err := s.Ingest(statementMail("partial", text))
	if err != nil {
		t.Fatal(err)
	}
	p := StatementParser{Adapter: "hdfc-credit-card", BalanceTolerancePaise: 99}
	v, err := s.previewPDF(t.Context(), id, 0, &p, nil)
	if err != nil || v.ParseError == "" || v.Statement == nil || len(v.Statement.Transactions) != 2 || v.Fingerprint != "" {
		t.Fatal("failed preview lost diagnosis", err)
	}
}
