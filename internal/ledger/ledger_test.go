package ledger

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func testStore(t *testing.T) *Store {
	t.Helper()
	s, e := Open(t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { s.DB.Close() })
	return s
}
func testParser() Parser {
	return Parser{Name: "Example Bank", Sender: "alerts@example.invalid", Subject: "^Alert$", Pattern: `(?P<currency>[A-Z]{3}) (?P<amount>[\d,.]+) at (?P<merchant>.+) card (?P<account>\d{4}) on (?P<date>\d{4}-\d{2}-\d{2})`, DateLayout: "2006-01-02", Timezone: "Asia/Kolkata", Currency: "INR", Direction: "debit", Enabled: true}
}
func testMail(id, body string) []byte {
	return []byte("From: Example <alerts@example.invalid>\r\nSubject: Alert\r\nMessage-ID: <" + id + "@example.invalid>\r\nContent-Type: text/plain; charset=utf-8\r\n\r\n" + body)
}

const testBody = "INR 1,234.56 at Example Shop card 4242 on 2026-10-01"

func TestMinorUnits(t *testing.T) {
	for value, want := range map[string]int64{"1.01": 101, "12,34,567.89": 123456789, "1,234,567.89": 123456789, "0.01": 1, "20": 2000} {
		got, e := MinorUnits(value)
		if e != nil || got != want {
			t.Errorf("%s: %d %v", value, got, e)
		}
	}
	for _, v := range []string{"0", "-2.00", "1.234", "NaN", "1,2.00", "1e3", "99999999999999999999999"} {
		if _, e := MinorUnits(v); e == nil {
			t.Errorf("accepted %q", v)
		}
	}
}
func TestParserValidationAndMatching(t *testing.T) {
	p := testParser()
	r, e := p.Parse(p.Sender, "Alert", testBody)
	if e != nil || r.Transaction == nil || r.Transaction.Amount != 123456 || r.Transaction.Date != "2026-10-01T00:00:00+05:30" {
		t.Fatalf("%+v %v", r, e)
	}
	r, e = p.Parse("impostor@example.invalid", "Alert", testBody)
	if e != nil || r.Matched {
		t.Fatal("sender not scoped")
	}
	if _, e = p.Parse(p.Sender, "Alert", testBody+"\n"+testBody); e == nil {
		t.Fatal("ambiguous body accepted")
	}
	if _, e = p.Parse(p.Sender, "Alert", strings.Replace(testBody, "2026-10-01", "2026-99-01", 1)); e == nil {
		t.Fatal("bad date accepted")
	}
	p.Direction = "ignore"
	p.Pattern = "declined"
	r, e = p.Parse(p.Sender, "Alert", "declined")
	if e != nil || !r.Ignored || r.Transaction != nil {
		t.Fatal("declined handling failed")
	}
}
func TestArchiveQueueRetryAndDedup(t *testing.T) {
	s := testStore(t)
	raw := testMail("one", testBody)
	id, e := s.Ingest(raw)
	if e != nil {
		t.Fatal(e)
	}
	m, e := s.Message(id)
	if e != nil || m.State != "queued" {
		t.Fatalf("%+v %v", m, e)
	}
	files, _ := filepath.Glob(filepath.Join(s.Dir, "archive", "*.eml"))
	if len(files) != 1 {
		t.Fatal("archive missing")
	}
	b, _ := os.ReadFile(files[0])
	if string(b) != string(raw) {
		t.Fatal("archive changed")
	}
	p, e := s.SaveParser(testParser())
	if e != nil {
		t.Fatal(e)
	}
	if _, e = s.Reprocess(); e != nil {
		t.Fatal(e)
	}
	var wg sync.WaitGroup
	for range 5 {
		wg.Go(func() {
			if _, err := s.Ingest(raw); err != nil {
				t.Error(err)
			}
		})
	}
	wg.Wait()
	rows, e := s.Transactions(Filter{})
	if e != nil || len(rows) != 1 || rows[0].Amount != 123456 {
		t.Fatalf("%+v %v", rows, e)
	}
	p.Direction = "credit"
	s.SaveParser(p)
	s.Reprocess()
	rows, _ = s.Transactions(Filter{})
	if rows[0].Direction != "debit" {
		t.Fatal("edited parser rewrote imported transaction")
	}
}
func TestAmbiguousParsersStayQueued(t *testing.T) {
	s := testStore(t)
	s.SaveParser(testParser())
	p := testParser()
	p.Name = "Other issuer"
	s.SaveParser(p)
	id, e := s.Ingest(testMail("ambiguous", testBody))
	if e != nil {
		t.Fatal(e)
	}
	m, _ := s.Message(id)
	if m.State != "queued" || !strings.Contains(m.Reason, "Multiple") {
		t.Fatalf("%+v", m)
	}
}
func TestAttachmentAndMalformedMIMEAreRetained(t *testing.T) {
	s := testStore(t)
	s.SaveParser(testParser())
	raw := []byte("From: alerts@example.invalid\r\nSubject: Alert\r\nMessage-ID: <pdf@example.invalid>\r\nMIME-Version: 1.0\r\nContent-Type: multipart/mixed; boundary=x\r\n\r\n--x\r\nContent-Type: text/plain\r\n\r\n" + testBody + "\r\n--x\r\nContent-Type: application/pdf\r\nContent-Disposition: attachment; filename=statement.pdf\r\n\r\n%PDF-synthetic\r\n--x--\r\n")
	id, e := s.Ingest(raw)
	if e != nil {
		t.Fatal(e)
	}
	m, _ := s.Message(id)
	if m.State != "queued" || !m.HasPDF {
		t.Fatalf("%+v", m)
	}
	s.Reprocess()
	rows, _ := s.Transactions(Filter{})
	if len(rows) != 0 {
		t.Fatal("statement treated as alert")
	}
	id, e = s.Ingest([]byte("malformed mime\n\nbody"))
	if e != nil {
		t.Fatal(e)
	}
	m, _ = s.Message(id)
	if !strings.HasPrefix(m.Reason, "MIME:") {
		t.Fatal(m.Reason)
	}
}
func TestBacklogBeyondQueuePage(t *testing.T) {
	s := testStore(t)
	for i := range 205 {
		if _, e := s.Ingest(testMail(fmt.Sprint(i), testBody)); e != nil {
			t.Fatal(e)
		}
	}
	s.SaveParser(testParser())
	count, e := s.Reprocess()
	if e != nil || count != 205 {
		t.Fatalf("%d %v", count, e)
	}
	var n int
	s.DB.QueryRow("SELECT count(*) FROM transactions").Scan(&n)
	if n != 205 {
		t.Fatal(n)
	}
}
func TestAPIAuthOriginAndCSV(t *testing.T) {
	s := testStore(t)
	s.SaveParser(testParser())
	s.Ingest(testMail("csv", strings.Replace(testBody, "Example Shop", "=HYPERLINK(bad)", 1)))
	server := &Server{Store: s, Poller: &Poller{Store: s, Config: MailConfig{Interval: time.Minute}}, Token: "synthetic-test-token-123456", SecureCookies: true}
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
func TestCurrencyTotalsStaySeparate(t *testing.T) {
	s := testStore(t)
	s.SaveParser(testParser())
	date := time.Now().Format("2006-01-02")
	for i, c := range []string{"INR", "USD"} {
		body := fmt.Sprintf("%s 10.00 at Example card 4242 on %s", c, date)
		s.Ingest(testMail(fmt.Sprint(i), body))
	}
	server := Server{Store: s}
	summary, e := server.getSummary()
	if e != nil || len(summary.Totals) != 2 {
		t.Fatalf("%+v %v", summary, e)
	}
	for _, total := range summary.Totals {
		if total.Debit != 1000 {
			t.Fatal(total)
		}
	}
}
