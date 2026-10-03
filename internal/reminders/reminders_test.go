package reminders

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/audemed44/ledger/internal/ledger"
	"github.com/audemed44/ledger/internal/statements"
	"github.com/audemed44/ledger/internal/store"
)

func TestSettingsAreCheckedAndKept(t *testing.T) {
	s, _ := store.Open(t.TempDir())
	defer s.Close()
	if got, _ := LoadSettings(s); !slices.Equal(got.Days, []int{0, 1, 5}) || got.OnStatement {
		t.Fatal("defaults", got)
	}
	saved, err := SaveSettings(s, Settings{Days: []int{7, 0, 3, 7}, OnStatement: true})
	if err != nil || !slices.Equal(saved.Days, []int{0, 3, 7}) {
		t.Fatal(saved, err)
	}
	if got, _ := LoadSettings(s); !slices.Equal(got.Days, []int{0, 3, 7}) || !got.OnStatement {
		t.Fatal(got)
	}
	if saved, err = SaveSettings(s, Settings{Days: []int{}}); err != nil || len(saved.Days) != 0 {
		t.Fatal("no day reminders", saved, err)
	}
	for _, bad := range [][]int{{-1}, {31}, {0, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10}} {
		if _, err := SaveSettings(s, Settings{Days: bad}); err == nil {
			t.Error("accepted", bad)
		}
	}
}

func TestRupeesUseIndianGrouping(t *testing.T) {
	for v, want := range map[int64]string{5: "₹0.05", 99900: "₹999.00", 123456: "₹1,234.56", 1234567890: "₹1,23,45,678.90"} {
		if got := Rupees(v); got != want {
			t.Errorf("%d: %s", v, got)
		}
	}
}

func TestEachStepIsSentOnceWhileUnpaid(t *testing.T) {
	s, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	st := statements.Statement{AccountID: ledger.AccountKey("Example Bank", "card", "4242"), AccountKind: "card",
		Issuer: "Example Bank", Account: "4242", Date: "2026-09-20", DueDate: "2026-10-08", TotalDue: 1234500, MinimumDue: 61700}
	raw, _ := json.Marshal(st)
	s.DB.Exec("INSERT INTO statements(account_key,date,fingerprint,snapshot) VALUES(?,?,?,?)", st.AccountID, st.Date, "x", string(raw))

	var titles []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]string
		json.NewDecoder(r.Body).Decode(&body)
		titles = append(titles, body["title"]+" | "+body["body"])
	}))
	defer srv.Close()
	n := &Notifier{Store: s, URL: srv.URL}
	at := func(day, hour int) time.Time { return time.Date(2026, 10, day, hour, 0, 0, 0, time.Local) }
	for _, now := range []time.Time{
		at(1, 10),           // 7 days out: nothing yet
		at(3, 8),            // 5 days out, but before 9:00
		at(3, 9), at(3, 15), // the 5-day reminder, once
		at(7, 10),             // tomorrow
		at(8, 10),             // today
		at(9, 10), at(10, 10), // overdue, once
		at(20, 10), // long overdue: quiet
	} {
		if err := n.Check(context.Background(), now); err != nil {
			t.Fatal(err)
		}
	}
	if len(titles) != 4 || !strings.HasPrefix(titles[0], "Example Bank card ••4242: ₹12,345.00 due in 5 days | Due Thu 8 Oct. Minimum ₹617.00.") ||
		!strings.Contains(titles[1], "due tomorrow") || !strings.Contains(titles[2], "due today") || !strings.Contains(titles[3], "overdue") {
		t.Fatalf("%q", titles)
	}

	// Once marked paid, nothing more is sent.
	s.SettleDue(st.AccountID, st.DueDate, true)
	s.DB.Exec("DELETE FROM reminders")
	n.Check(context.Background(), at(8, 10))
	if len(titles) != 4 {
		t.Fatalf("reminded about a paid card: %q", titles)
	}
}

func TestFailedSendsAreRetried(t *testing.T) {
	s, _ := store.Open(t.TempDir())
	defer s.Close()
	st := statements.Statement{AccountID: ledger.AccountKey("Example Bank", "card", "4242"), AccountKind: "card",
		Issuer: "Example Bank", Account: "4242", Date: "2026-09-20", DueDate: "2026-10-08", TotalDue: 100}
	raw, _ := json.Marshal(st)
	s.DB.Exec("INSERT INTO statements(account_key,date,fingerprint,snapshot) VALUES(?,?,?,?)", st.AccountID, st.Date, "x", string(raw))
	status := http.StatusNoContent
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(status) }))
	defer srv.Close()
	SaveSettings(s, Settings{Days: []int{1}})
	n := &Notifier{Store: s, URL: srv.URL}
	now := time.Date(2026, 10, 7, 10, 0, 0, 0, time.Local)
	if err := n.Check(context.Background(), now); err == nil {
		t.Fatal("204 treated as sent")
	}
	status = http.StatusOK
	if err := n.Check(context.Background(), now); err != nil {
		t.Fatal(err)
	}
	if sent, _ := s.ReminderSent(st.AccountID, st.DueDate, 1); !sent {
		t.Fatal("not recorded")
	}
}

func TestNewStatementNoticeIsOptional(t *testing.T) {
	s, _ := store.Open(t.TempDir())
	defer s.Close()
	st := statements.Statement{AccountID: ledger.AccountKey("Example Bank", "card", "4242"), AccountKind: "card",
		Issuer: "Example Bank", Account: "4242", Date: "2026-09-20", DueDate: "2026-10-08", TotalDue: 1234500, MinimumDue: 61700}
	raw, _ := json.Marshal(st)
	s.DB.Exec("INSERT INTO statements(account_key,date,fingerprint,snapshot) VALUES(?,?,?,?)", st.AccountID, st.Date, "x", string(raw))
	var titles []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]string
		json.NewDecoder(r.Body).Decode(&body)
		titles = append(titles, body["title"]+" | "+body["body"])
	}))
	defer srv.Close()
	at := func(day, hour int) time.Time { return time.Date(2026, 10, day, hour, 0, 0, 0, time.Local) }

	n := &Notifier{Store: s, URL: srv.URL}
	n.Check(context.Background(), at(1, 10))
	if len(titles) != 0 {
		t.Fatalf("notice without the option: %q", titles)
	}

	SaveSettings(s, Settings{Days: []int{0, 1, 5}, OnStatement: true})
	for _, now := range []time.Time{at(1, 8), at(1, 10), at(1, 15), at(7, 10)} {
		if err := n.Check(context.Background(), now); err != nil {
			t.Fatal(err)
		}
	}
	if len(titles) != 2 || titles[0] != "Example Bank card ••4242: new statement, ₹12,345.00 due in 7 days | Statement total ₹12,345.00, due Thu 8 Oct. Minimum ₹617.00." ||
		!strings.Contains(titles[1], "due tomorrow") {
		t.Fatalf("%q", titles)
	}

	// A statement arriving inside a day step sends only the notice.
	s.DB.Exec("DELETE FROM reminders")
	titles = nil
	n.Check(context.Background(), at(4, 10))
	n.Check(context.Background(), at(4, 11))
	if len(titles) != 1 || !strings.Contains(titles[0], "new statement") {
		t.Fatalf("%q", titles)
	}

	// Past its due date, a statement gets no notice.
	s.DB.Exec("DELETE FROM reminders")
	titles = nil
	n.Check(context.Background(), at(9, 10))
	if len(titles) != 1 || strings.Contains(titles[0], "new statement") {
		t.Fatalf("%q", titles)
	}
}
