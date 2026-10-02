package server

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/audemed44/ledger/internal/fixture"
	"github.com/audemed44/ledger/internal/gmail"
	"github.com/audemed44/ledger/internal/statements"
	"github.com/audemed44/ledger/internal/store"
)

func TestAPIAuthOriginAndCSV(t *testing.T) {
	s := testStore(t)
	s.SaveParser(fixture.AlertParser())
	s.Ingest(fixture.Mail("csv", strings.Replace(fixture.AlertBody, "Example Shop", "=HYPERLINK(bad)", 1)))
	server := &Server{Store: s, Poller: &gmail.Poller{Store: s, Config: gmail.Config{Interval: time.Minute}}, Token: "synthetic-test-token-123456", SecureCookies: true}
	h := server.Handler()
	for _, tt := range []struct {
		method, path, origin, token string
		want                        int
	}{{"GET", "/api/transactions", "", "", 401}, {"GET", "/api/foyer/widget", "", "", 401}, {"GET", "/api/transactions", "", server.Token, 200}, {"POST", "/api/reprocess", "https://evil.invalid", server.Token, 403}, {"POST", "/api/login", "null", "", 403}} {
		r := httptest.NewRequest(tt.method, "http://ledger.test"+tt.path, nil)
		r.Header.Set("Origin", tt.origin)
		if tt.token != "" {
			r.Header.Set("Authorization", "Bearer "+tt.token)
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != tt.want {
			t.Errorf("%s: %d", tt.path, w.Code)
		}
	}
	r := httptest.NewRequest("POST", "http://ledger.test/api/login", strings.NewReader(`{"token":"`+server.Token+`"}`))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	cookies := w.Result().Cookies()
	if w.Code != 200 || len(cookies) != 1 || !cookies[0].HttpOnly || !cookies[0].Secure || cookies[0].SameSite != http.SameSiteStrictMode {
		t.Fatal("insecure session cookie")
	}
	r = httptest.NewRequest("GET", "http://ledger.test/api/transactions.csv", nil)
	r.AddCookie(cookies[0])
	w = httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if !strings.Contains(w.Body.String(), "'=HYPERLINK") || !strings.Contains(w.Body.String(), "1234.56") {
		t.Fatal(w.Body.String())
	}
}

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
	var p statements.Parser
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

func TestStatementImportAPIAndIdempotence(t *testing.T) {
	fixture.RequirePDFTools(t)
	s := testStore(t)
	p, err := s.SaveStatementParser(statements.Parser{Name: "HDFC Credit Card Parser v1", Adapter: "hdfc-credit-card", BalanceTolerancePaise: 99})
	if err != nil {
		t.Fatal(err)
	}
	id, err := s.Ingest(fixture.StatementMail("first", fixture.Statement))
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
	preview := func(id int64) store.PDFPreview {
		w := call("POST", fmt.Sprintf("/api/messages/%d/pdf", id), fmt.Sprintf(`{"part":0,"parser_id":%d}`, p.ID), "1234", "")
		if w.Code != 200 {
			t.Fatal(w.Code, w.Body.String())
		}
		var v store.PDFPreview
		if err := json.Unmarshal(w.Body.Bytes(), &v); err != nil {
			t.Fatal(err)
		}
		return v
	}
	v := preview(id)
	if v.Fingerprint == "" {
		t.Fatal("no importable preview", v.ParseError)
	}
	rows, _ := s.Transactions(store.Filter{})
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
	rows, _ = s.Transactions(store.Filter{})
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
	id2, err := s.Ingest(fixture.StatementMail("resent", fixture.Statement))
	if err != nil {
		t.Fatal(err)
	}
	v2 := preview(id2)
	out, err := s.ImportStatement(id2, 0, v2, v2.Fingerprint)
	if err != nil || !out.AlreadyImported {
		t.Fatal("resent statement duplicated", err)
	}
	rows, _ = s.Transactions(store.Filter{})
	if len(rows) != 3 {
		t.Fatal(len(rows))
	}
	// Deleting the parser never removes rows or statement provenance.
	if w := call("DELETE", fmt.Sprintf("/api/statement-parsers/%d", p.ID), "", "1234", ""); w.Code != 200 {
		t.Fatal(w.Code)
	}
	rows, _ = s.Transactions(store.Filter{})
	if len(rows) != 3 {
		t.Fatal("delete removed transactions")
	}
	if _, err = s.ArchivedRaw(id); err != nil {
		t.Fatal("delete removed archive", err)
	}
}

func testStore(t *testing.T) *store.Store {
	t.Helper()
	s, e := store.Open(t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func TestSummaryShape(t *testing.T) {
	s := testStore(t)
	h := (&Server{Store: s, Token: "1234"}).Handler()
	r := httptest.NewRequest("GET", "/api/summary", nil)
	r.Header.Set("Authorization", "Bearer 1234")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	var got map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	// The frontend reads these top-level fields; empty lists must be [], not null.
	for _, key := range []string{"totals", "accounts", "transactions", "queued", "parsers", "month", "demo"} {
		if _, ok := got[key]; !ok {
			t.Errorf("missing %s: %s", key, w.Body.String())
		}
	}
	if !strings.Contains(w.Body.String(), `"totals":[]`) || !strings.Contains(w.Body.String(), `"accounts":[]`) {
		t.Fatal(w.Body.String())
	}
}
