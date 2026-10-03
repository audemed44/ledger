package server

import (
	"bytes"
	"encoding/json"
	"fmt"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/audemed44/ledger/internal/fixture"
	"github.com/audemed44/ledger/internal/gmail"
	"github.com/audemed44/ledger/internal/ledger"
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
	s.PDFPasswords = []string{"secret-one", "secret-two"}
	server := &Server{Store: s, Token: "test-token"}
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
	// Another card's statement waits in the inbox: saving the automatic
	// import imports it too.
	waiting, _ := s.Ingest(fixture.StatementMail("waiting", strings.ReplaceAll(fixture.Statement, "4242", "8080")))
	body := fmt.Sprintf(`{"part":0,"parser_id":%d,"import":true,"automatic":true,"fingerprint":%q}`, p.ID, v.Fingerprint)
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
		w := call("POST", path, body, "1234", "")
		if w.Code != 200 || !strings.Contains(w.Body.String(), `"automatic":true`) {
			t.Fatal(w.Code, w.Body.String())
		}
	}
	for start := time.Now(); ; time.Sleep(50 * time.Millisecond) {
		if m, _ := s.Message(waiting); m.State == "statement" {
			break
		} else if time.Since(start) > 15*time.Second {
			t.Fatal("waiting statement not imported", m.Reason)
		}
	}
	rows, _ = s.Transactions(store.Filter{})
	if len(rows) != 6 {
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
	// The saved automatic import takes the resent copy out of the inbox.
	id2, err := s.Ingest(fixture.StatementMail("resent", fixture.Statement))
	if err != nil {
		t.Fatal(err)
	}
	if m, _ := s.Message(id2); m.State != "statement" {
		t.Fatal("resent statement not imported automatically", m.Reason)
	}
	v2 := preview(id2)
	out, err := s.ImportStatement(id2, 0, v2, v2.Fingerprint)
	if err != nil || !out.AlreadyImported {
		t.Fatal("resent statement duplicated", err)
	}
	rows, _ = s.Transactions(store.Filter{})
	if len(rows) != 6 {
		t.Fatal(len(rows))
	}
	// Deleting the parser never removes rows or statement provenance.
	if w := call("DELETE", fmt.Sprintf("/api/statement-parsers/%d", p.ID), "", "1234", ""); w.Code != 200 {
		t.Fatal(w.Code)
	}
	rows, _ = s.Transactions(store.Filter{})
	if len(rows) != 6 {
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

func TestParserFromExample(t *testing.T) {
	h := (&Server{Store: testStore(t), Token: "1234"}).Handler()
	call := func(body string) *httptest.ResponseRecorder {
		r := httptest.NewRequest("POST", "/api/parsers/from-example", strings.NewReader(body))
		r.Header.Set("Authorization", "Bearer 1234")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w
	}
	// "INR 1,234.56 at Example Shop card 4242 on 2026-10-01"
	w := call(`{"subject":"Alert","body":` + fmt.Sprintf("%q", fixture.AlertBody) + `,"marks":[
		{"field":"amount","start":4,"end":12},{"field":"merchant","start":16,"end":28},
		{"field":"account","start":34,"end":38},{"field":"date","start":42,"end":52}]}`)
	var got struct{ Pattern, DateLayout, Subject string }
	json.Unmarshal(w.Body.Bytes(), &got)
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"date_layout":"2006-1-2"`) || got.Subject != "(?i)^Alert$" {
		t.Fatal(w.Code, w.Body.String())
	}
	if w = call(`{"body":"x","marks":[{"field":"amount","start":0,"end":1}]}`); w.Code != 422 {
		t.Fatal(w.Code, w.Body.String())
	}
}

func TestStatementUpload(t *testing.T) {
	fixture.RequirePDFTools(t)
	s := testStore(t)
	h := (&Server{Store: s, Token: "1234"}).Handler()
	upload := func(name string, content []byte) (int, map[string]any) {
		var body bytes.Buffer
		form := multipart.NewWriter(&body)
		part, _ := form.CreateFormFile("file", name)
		part.Write(content)
		form.Close()
		r := httptest.NewRequest("POST", "/api/statements/upload", &body)
		r.Header.Set("Content-Type", form.FormDataContentType())
		r.Header.Set("Authorization", "Bearer 1234")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		var out map[string]any
		json.Unmarshal(w.Body.Bytes(), &out)
		return w.Code, out
	}
	if code, _ := upload("notes.txt", []byte("not a pdf")); code != 400 {
		t.Fatal("non-PDF accepted", code)
	}
	pdf := fixture.PDF(fixture.Statement)
	code, out := upload("../../Statement Oct.pdf", pdf)
	if code != 200 || out["state"] != "queued" {
		t.Fatal(code, out)
	}
	id := int64(out["id"].(float64))
	m, _ := s.ReviewMessage(id)
	if m.Sender != store.UploadSender || len(m.Attachments) != 1 || m.Attachments[0].Name != "Statement Oct.pdf" || !m.HasPDF {
		t.Fatalf("%+v", m)
	}
	if code, again := upload("Statement Oct.pdf", pdf); code != 200 || again["id"] != out["id"] {
		t.Fatal("same file filed twice", again)
	}

	// With an automatic import saved from it, the next upload imports itself.
	p, _ := s.SaveStatementParser(statements.Parser{Name: "HDFC Credit Card Parser v1", Adapter: "hdfc-credit-card", BalanceTolerancePaise: 99})
	if _, err := s.AddStatementTrigger(p.ID, id, 1); err != nil {
		t.Fatal(err)
	}
	code, out = upload("Statement Nov.pdf", fixture.PDF(strings.ReplaceAll(fixture.Statement, "4242", "8080")))
	if code != 200 || out["state"] != "statement" || out["message"] != "Statement imported" {
		t.Fatal(code, out)
	}
	if rows, _ := s.Transactions(store.Filter{}); len(rows) != 3 {
		t.Fatal(len(rows))
	}
}

func TestWidgetAcceptsPDFsFromDrop(t *testing.T) {
	h := (&Server{Store: testStore(t), Token: "1234"}).Handler()
	r := httptest.NewRequest("GET", "/api/foyer/widget", nil)
	r.Header.Set("Authorization", "Bearer 1234")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	var got struct {
		Accepts struct {
			URL   string   `json:"url"`
			Types []string `json:"types"`
		} `json:"accepts"`
	}
	json.Unmarshal(w.Body.Bytes(), &got)
	if got.Accepts.URL != "/api/statements/upload" || len(got.Accepts.Types) == 0 || got.Accepts.Types[0] != ".pdf" {
		t.Fatal(w.Body.String())
	}
}

func TestParserChangesRereadTheirEmails(t *testing.T) {
	s := testStore(t)
	p, _ := s.SaveParser(fixture.AlertParser())
	s.Ingest(fixture.Mail("one", fixture.AlertBody))
	h := (&Server{Store: s, Token: "1234"}).Handler()
	call := func(method, path, body string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, path, strings.NewReader(body))
		r.Header.Set("Authorization", "Bearer 1234")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w
	}
	if w := call("GET", fmt.Sprintf("/api/parsers/%d/handled", p.ID), ""); !strings.Contains(w.Body.String(), `"emails":1`) {
		t.Fatal(w.Code, w.Body.String())
	}
	p.Direction = "credit"
	raw, _ := json.Marshal(p)
	if w := call("POST", "/api/parsers?reread=1", string(raw)); w.Code != 200 || !strings.Contains(w.Body.String(), `"reread":1`) || !strings.Contains(w.Body.String(), `"id":`) {
		t.Fatal(w.Code, w.Body.String())
	}
	if rows, _ := s.Transactions(store.Filter{}); len(rows) != 1 || rows[0].Direction != "credit" {
		t.Fatalf("%+v", rows)
	}
	if w := call("DELETE", fmt.Sprintf("/api/parsers/%d?reread=1", p.ID), ""); w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	if rows, _ := s.Transactions(store.Filter{}); len(rows) != 0 {
		t.Fatal("deleted parser's transaction kept", rows)
	}
	if w := call("GET", "/api/parsers/999/handled", ""); w.Code != 404 {
		t.Fatal(w.Code)
	}
}

func TestTransferRoutes(t *testing.T) {
	s := testStore(t)
	p, _ := s.SaveParser(fixture.AlertParser())
	_ = p
	s.Ingest(fixture.Mail("sweep", strings.Replace(fixture.AlertBody, "Example Shop", "SWEEP TFR DR", 1)))
	rows, _ := s.Transactions(store.Filter{})
	h := (&Server{Store: s, Token: "1234"}).Handler()
	call := func(method, path, body string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, path, strings.NewReader(body))
		r.Header.Set("Authorization", "Bearer 1234")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w
	}
	if w := call("POST", fmt.Sprintf("/api/transactions/%d/transfer-rule", rows[0].ID), ""); w.Code != 200 || !strings.Contains(w.Body.String(), "SWEEP") {
		t.Fatal(w.Code, w.Body.String())
	}
	if w := call("GET", "/api/transactions?kind=transfers", ""); !strings.Contains(w.Body.String(), `"transfer":"rule"`) {
		t.Fatal(w.Body.String())
	}
	if w := call("GET", "/api/transactions.csv", ""); !strings.Contains(w.Body.String(), ",rule") {
		t.Fatal(w.Body.String())
	}
	if w := call("DELETE", "/api/transfer-rules/1", ""); w.Code != 200 {
		t.Fatal(w.Code)
	}
	if w := call("POST", fmt.Sprintf("/api/transactions/%d/transfer", rows[0].ID), `{"transfer":true}`); w.Code != 200 {
		t.Fatal(w.Code)
	}
	if w := call("GET", "/api/transactions?kind=spending", ""); w.Body.String() != "[]\n" {
		t.Fatal(w.Body.String())
	}
}

func TestDismissRoutes(t *testing.T) {
	s := testStore(t)
	id, _ := s.Ingest(fixture.Mail("stray", fixture.AlertBody))
	h := (&Server{Store: s, Token: "1234"}).Handler()
	call := func(method, path, body string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, path, strings.NewReader(body))
		r.Header.Set("Authorization", "Bearer 1234")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w
	}
	path := fmt.Sprintf("/api/messages/%d/dismiss", id)
	if w := call("POST", path, `{"dismissed":true}`); w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	if w := call("GET", "/api/messages?kind=dismissed", ""); !strings.Contains(w.Body.String(), "Dismissed from the inbox") {
		t.Fatal(w.Body.String())
	}
	if w := call("POST", path, `{"dismissed":true}`); w.Code != 409 {
		t.Fatal(w.Code)
	}
	if w := call("POST", path, `{"dismissed":false}`); w.Code != 200 {
		t.Fatal(w.Code)
	}
	if w := call("GET", "/api/messages", ""); !strings.Contains(w.Body.String(), fmt.Sprintf(`"id":%d`, id)) {
		t.Fatal("not restored", w.Body.String())
	}
}

func TestWidgetListsCardDuesAndMarksThemPaid(t *testing.T) {
	s := testStore(t)
	due := time.Now().AddDate(0, 0, 2).Format("2006-01-02")
	st := statements.Statement{AccountID: ledger.AccountKey("Example Bank", "card", "4242"), AccountKind: "card",
		Issuer: "Example Bank", Account: "4242", Date: time.Now().AddDate(0, 0, -18).Format("2006-01-02"), DueDate: due, TotalDue: 1234500}
	raw, _ := json.Marshal(st)
	s.DB.Exec("INSERT INTO statements(account_key,date,fingerprint,snapshot) VALUES(?,?,?,?)", st.AccountID, st.Date, "x", string(raw))
	h := (&Server{Store: s, Token: "1234"}).Handler()
	call := func(method, path string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, path, strings.NewReader("{}"))
		r.Header.Set("Authorization", "Bearer 1234")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w
	}
	type widget struct {
		Stats []map[string]string `json:"stats"`
		Items []struct {
			Title, Subtitle, Caption string
			Action                   struct{ URL string }
		} `json:"items"`
	}
	var got widget
	json.Unmarshal(call("GET", "/api/foyer/widget").Body.Bytes(), &got)
	if len(got.Items) != 1 || got.Items[0].Title != "Example Bank card ••4242" || got.Items[0].Caption != "₹12,345.00" ||
		!strings.HasSuffix(got.Items[0].Subtitle, "in 2 days") || got.Stats[1]["label"] != "Card dues" || got.Stats[1]["tone"] != "warn" {
		t.Fatalf("%+v", got)
	}
	if w := call("POST", got.Items[0].Action.URL); w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	got = widget{}
	json.Unmarshal(call("GET", "/api/foyer/widget").Body.Bytes(), &got)
	if len(got.Items) != 0 || got.Stats[1]["caption"] != "All paid" {
		t.Fatalf("%+v", got)
	}
	if w := call("POST", "/api/reminders/test"); w.Code != 409 {
		t.Fatal("test reminder without LEDGER_NOTIFY_URL", w.Code)
	}
}
