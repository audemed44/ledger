package store

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/audemed44/ledger/internal/fixture"
	"github.com/audemed44/ledger/internal/ledger"
	"github.com/audemed44/ledger/internal/statements"
)

func testStore(t *testing.T) *Store {
	t.Helper()
	s, e := Open(t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func TestFiveCardsAndTwoBankAccountsStaySeparate(t *testing.T) {
	s := testStore(t)
	// Deliberately share suffixes across issuer and account type.
	inputs := []struct{ issuer, kind, last string }{{"HDFC", "card", "1001"}, {"HDFC", "card", "1002"}, {"ICICI", "card", "1001"}, {"SBI", "card", "1001"}, {"AXIS", "card", "1001"}, {"HDFC", "bank", "1001"}, {"ICICI", "bank", "1001"}}
	for i, a := range inputs {
		p := fixture.AlertParser()
		p.Name = fmt.Sprintf("Rule %d", i)
		p.Issuer = a.issuer
		p.AccountKind = a.kind
		p.Sender = fmt.Sprintf("bank%d@example.invalid", i)
		if _, e := s.SaveParser(p); e != nil {
			t.Fatal(e)
		}
		raw := strings.ReplaceAll(string(fixture.Mail(fmt.Sprint(i), strings.Replace(fixture.AlertBody, "4242", a.last, 1))), "alerts@example.invalid", p.Sender)
		if _, e := s.Ingest([]byte(raw)); e != nil {
			t.Fatal(e)
		}
	}
	accounts, e := s.Accounts()
	if e != nil || len(accounts) != 7 {
		t.Fatalf("%+v %v", accounts, e)
	}
	for _, a := range accounts {
		rows, e := s.Transactions(Filter{Account: a.ID})
		if e != nil || len(rows) != 1 || rows[0].AccountKind != a.Kind || rows[0].AccountID != a.ID {
			t.Fatalf("account isolation failed: %+v %v", rows, e)
		}
	}
	// Purchase and refund parser names must not create separate identities.
	p := fixture.AlertParser()
	p.Name = "HDFC refunds"
	p.Issuer = "hdfc"
	p.AccountKind = "card"
	p.Sender = "refunds@example.invalid"
	p.Direction = "credit"
	s.SaveParser(p)
	raw := strings.ReplaceAll(string(fixture.Mail("refund", strings.Replace(fixture.AlertBody, "4242", "1001", 1))), "alerts@example.invalid", p.Sender)
	if _, e = s.Ingest([]byte(raw)); e != nil {
		t.Fatal(e)
	}
	accounts, e = s.Accounts()
	if e != nil || len(accounts) != 7 {
		t.Fatal("parser variants split the account")
	}
	rows, e := s.Transactions(Filter{Account: ledger.AccountKey("HDFC", "card", "1001")})
	if e != nil || len(rows) != 2 {
		t.Fatalf("refund failed to join card: %+v %v", rows, e)
	}
}

func TestBackfillCutoffRetiresOldQueueAndPreservesArchives(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	oldID, err := s.Ingest(fixture.DatedMail("cutoff-old", "Wed, 31 Dec 2025 23:59:59 +0530"))
	if err != nil {
		t.Fatal(err)
	}
	old, _ := s.Message(oldID)
	if old.State != "excluded" {
		t.Fatalf("old mail is %s", old.State)
	}
	boundaryID, err := s.Ingest(fixture.DatedMail("cutoff-boundary", "Thu, 01 Jan 2026 00:00:00 +0530"))
	if err != nil {
		t.Fatal(err)
	}
	// Simulate the pending queue left by the previous version.
	if _, err = s.DB.Exec("UPDATE messages SET state='queued',reason='No matching alert parser' WHERE id=?", oldID); err != nil {
		t.Fatal(err)
	}
	s.SetSetting("cursor:test", "unchanged")
	var archive string
	s.DB.QueryRow("SELECT archive FROM messages WHERE id=?", oldID).Scan(&archive)
	s.DB.Close()
	s, err = Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer s.DB.Close()
	old, _ = s.Message(oldID)
	if old.State != "excluded" || old.Reason != BeforeBackfillReason {
		t.Fatalf("old mail not retired: %+v", old)
	}
	rows, err := s.Messages()
	if err != nil || len(rows) != 1 || rows[0].ID != boundaryID {
		t.Fatalf("boundary missing: %+v %v", rows, err)
	}
	if _, err = os.Stat(filepath.Join(dir, "archive", archive)); err != nil {
		t.Fatal("original archive lost", err)
	}
	if cursor, _ := s.Setting("cursor:test"); cursor != "unchanged" {
		t.Fatal("cursor changed")
	}
	if _, err = s.Reprocess(); err != nil {
		t.Fatal(err)
	}
	old, _ = s.Message(oldID)
	if old.State != "excluded" {
		t.Fatal("retry requeued older mail")
	}
}

func TestInboxDateOrderAndFiltersBeforeLimit(t *testing.T) {
	s := testStore(t)
	pdfID, err := s.Ingest(fixture.StatementMail("older-pdf", fixture.Statement))
	if err != nil {
		t.Fatal(err)
	}
	// Dates deliberately differ from insertion order. UTC ordering matters too.
	for i := 0; i < 205; i++ {
		if _, err = s.Ingest(append([]byte("Date: Sat, 03 Oct 2026 01:00:00 +0530\r\n"), fixture.Mail(fmt.Sprint(i), fixture.AlertBody)...)); err != nil {
			t.Fatal(err)
		}
	}
	latest, err := s.Ingest(append([]byte("Date: Fri, 02 Oct 2026 23:00:00 +0000\r\n"), fixture.Mail("latest", fixture.AlertBody)...))
	if err != nil {
		t.Fatal(err)
	}
	// Higher ID but older date must not go first.
	s.Ingest(append([]byte("Date: Thu, 01 Oct 2026 00:00:00 +0530\r\n"), fixture.Mail("last-insert", fixture.AlertBody)...))
	rows, err := s.Messages()
	if err != nil || len(rows) != 200 || rows[0].ID != latest {
		t.Fatal("wrong date order", err)
	}
	rows, err = s.FilteredMessages("pdf")
	if err != nil || len(rows) != 1 || rows[0].ID != pdfID || !rows[0].HasPDF {
		t.Fatal("filtered after limit", rows, err)
	}
	rows, err = s.FilteredMessages("text")
	if err != nil || len(rows) != 200 {
		t.Fatal(err, len(rows))
	}
	for _, row := range rows {
		if row.HasPDF {
			t.Fatal("PDF in text filter")
		}
	}
	if _, err = s.FilteredMessages("bad"); err == nil {
		t.Fatal("invalid filter accepted")
	}
}

func TestParserReuseRenameAndDuplicateUpgrade(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	p := statements.Parser{Name: "HDFC Credit Card", Adapter: "hdfc-credit-card", BalanceTolerancePaise: 99}
	first, err := s.SaveStatementParser(p)
	if err != nil {
		t.Fatal(err)
	}
	second, err := s.SaveStatementParser(p)
	if err != nil || first.ID != second.ID {
		t.Fatal("duplicate preset created", err)
	}
	p.PasswordSlot = 1
	if _, err = s.SaveStatementParser(p); err != ErrParserNameTaken {
		t.Fatal("same name hides different config", err)
	}
	// Simulate duplicates saved by the previous version.
	p.PasswordSlot = 0
	raw, _ := json.Marshal(p)
	s.DB.Exec("INSERT INTO statement_parsers(definition) VALUES(?)", string(raw))
	s.DB.Close()
	s, err = Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer s.DB.Close()
	rows, err := s.StatementParsers()
	if err != nil || len(rows) != 1 || rows[0].ID != first.ID || rows[0].Name != "HDFC Credit Card Parser v1" {
		t.Fatal("legacy duplicates not merged/renamed", rows, err)
	}
}

func TestStatementUpgradePreservesExistingTransactions(t *testing.T) {
	s := testStore(t)
	s.SaveParser(fixture.AlertParser())
	id, err := s.Ingest(fixture.Mail("existing-alert", fixture.AlertBody))
	if err != nil {
		t.Fatal(err)
	}
	before, _ := s.Transactions(Filter{})
	// Recreate the previous transactions schema while preserving an actual row.
	_, err = s.DB.Exec(`DROP TABLE statement_sources; DROP TABLE statements;
 CREATE TABLE old_transactions(id INTEGER PRIMARY KEY, message_id INTEGER UNIQUE NOT NULL REFERENCES messages(id), merchant TEXT NOT NULL, account TEXT NOT NULL, amount INTEGER NOT NULL CHECK(amount>0), currency TEXT NOT NULL, direction TEXT NOT NULL, date TEXT NOT NULL, reference TEXT NOT NULL, status TEXT NOT NULL, issuer TEXT NOT NULL, account_kind TEXT NOT NULL DEFAULT 'unknown');
 INSERT INTO old_transactions SELECT id,message_id,merchant,account,amount,currency,direction,date,reference,status,issuer,account_kind FROM transactions;
 DROP TABLE transactions; ALTER TABLE old_transactions RENAME TO transactions;
 ALTER TABLE messages DROP COLUMN has_pdf;
 DELETE FROM settings WHERE key='statement-import-schema';`)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.migrate(); err != nil {
		t.Fatal(err)
	}
	after, err := s.Transactions(Filter{})
	if err != nil || len(after) != 1 || after[0] != before[0] || after[0].MessageID != id {
		t.Fatal("upgrade altered alert", after, err)
	}
	if err = s.migrate(); err != nil {
		t.Fatal("migration not repeatable", err)
	}
	var foreignKeyError string
	if err = s.DB.QueryRow("PRAGMA foreign_key_check").Scan(&foreignKeyError); err == nil {
		t.Fatal("broken foreign keys")
	}
	// Existing row still prevents processing the same alert again.
	s.Ingest(fixture.Mail("existing-alert", fixture.AlertBody))
	after, _ = s.Transactions(Filter{})
	if len(after) != 1 {
		t.Fatal("existing alert duplicated")
	}
}

func TestSharedSuffixDifferentIssuersStaySeparateOnImport(t *testing.T) {
	s := testStore(t)
	p := fixture.AlertParser()
	p.Issuer = "OTHER BANK"
	p.AccountKind = "card"
	s.SaveParser(p)
	s.Ingest(fixture.Mail("other-bank", "INR 500.00 at EXAMPLE SHOP card 4242 on 2026-10-01"))
	id, err := s.Ingest(fixture.StatementMail("hdfc", fixture.Statement))
	if err != nil {
		t.Fatal(err)
	}
	st, err := statements.ParseHDFC(fixture.Statement)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.ImportStatement(id, 0, PDFPreview{Statement: &st}, statements.Fingerprint(st)); err != nil {
		t.Fatal("combined issuers", err)
	}
	rows, _ := s.Transactions(Filter{})
	if len(rows) != 4 {
		t.Fatal(len(rows))
	}
	// A missing row remains a validation failure even with a tiny closing discrepancy.
	st, err = statements.ParseHDFC(strings.Replace(fixture.Statement, "C 50.00", "C 49.85", 1))
	if err == nil || st.Balanced {
		t.Fatal("accepted incomplete rows")
	}
}

func TestArchiveQueueRetryAndDedup(t *testing.T) {
	s := testStore(t)
	raw := fixture.Mail("one", fixture.AlertBody)
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
	p, e := s.SaveParser(fixture.AlertParser())
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
	s.SaveParser(fixture.AlertParser())
	p := fixture.AlertParser()
	p.Name = "Other issuer"
	s.SaveParser(p)
	id, e := s.Ingest(fixture.Mail("ambiguous", fixture.AlertBody))
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
	s.SaveParser(fixture.AlertParser())
	raw := []byte("From: alerts@example.invalid\r\nSubject: Alert\r\nMessage-ID: <pdf@example.invalid>\r\nMIME-Version: 1.0\r\nContent-Type: multipart/mixed; boundary=x\r\n\r\n--x\r\nContent-Type: text/plain\r\n\r\n" + fixture.AlertBody + "\r\n--x\r\nContent-Type: application/pdf\r\nContent-Disposition: attachment; filename=statement.pdf\r\n\r\n%PDF-synthetic\r\n--x--\r\n")
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
		if _, e := s.Ingest(fixture.Mail(fmt.Sprint(i), fixture.AlertBody)); e != nil {
			t.Fatal(e)
		}
	}
	s.SaveParser(fixture.AlertParser())
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

func TestCurrencyTotalsStaySeparate(t *testing.T) {
	s := testStore(t)
	s.SaveParser(fixture.AlertParser())
	date := time.Now().Format("2006-01-02")
	for i, c := range []string{"INR", "USD"} {
		body := fmt.Sprintf("%s 10.00 at Example card 4242 on %s", c, date)
		s.Ingest(fixture.Mail(fmt.Sprint(i), body))
	}
	totals, e := s.MonthTotals(time.Now().Format("2006-01"))
	if e != nil || len(totals) != 2 {
		t.Fatalf("%+v %v", totals, e)
	}
	for _, total := range totals {
		if total.Debit != 1000 {
			t.Fatal(total)
		}
	}
}

func TestHTMLMailIsReadableAndParseable(t *testing.T) {
	s := testStore(t)
	body := `<html><head><title>Not the message</title><style>.a{color:red}</style></head><body><div hidden>Hidden decoy INR 99.00</div><div style="display: none">Hide this</div><script>fetch("https://evil.invalid")</script><p>INR&nbsp;<b>1,234.56</b> at Example &amp; Shop card 4242 on 2026-10-01</p><img src="https://tracker.invalid/pixel"><table><tr><td>Reference</td><td>FAKE-01</td></tr></table></body></html>`
	id, e := s.Ingest(fixture.HTMLMail("html", body))
	if e != nil {
		t.Fatal(e)
	}
	m, e := s.ReviewMessage(id)
	if e != nil {
		t.Fatal(e)
	}
	if !m.CanParse || m.BodyFormat != "html" || !strings.Contains(m.Body, "INR 1,234.56 at Example & Shop") || !strings.Contains(m.Body, "Reference FAKE-01") {
		t.Fatalf("unexpected review: %+v", m)
	}
	for _, bad := range []string{"Hidden", "Hide this", "fetch", "tracker.invalid", "<html>", "Not the message"} {
		if strings.Contains(m.Body, bad) {
			t.Errorf("included %s", bad)
		}
	}
	if _, e = s.SaveParser(fixture.AlertParser()); e != nil {
		t.Fatal(e)
	}
	if _, e = s.Reprocess(); e != nil {
		t.Fatal(e)
	}
	rows, e := s.Transactions(Filter{})
	if e != nil || len(rows) != 1 || rows[0].Merchant != "Example & Shop" {
		t.Fatalf("%+v %v", rows, e)
	}
}

func TestInlinePDFIsNotAnAlert(t *testing.T) {
	s := testStore(t)
	raw := []byte("From: alerts@example.invalid\r\nSubject: Alert\r\nMIME-Version: 1.0\r\nContent-Type: multipart/mixed; boundary=x\r\n\r\n--x\r\nContent-Type: text/html\r\n\r\n<p>" + fixture.AlertBody + "</p>\r\n--x\r\nContent-Type: application/pdf\r\nContent-Disposition: inline\r\n\r\n%PDF-synthetic\r\n--x--\r\n")
	id, e := s.Ingest(raw)
	if e != nil {
		t.Fatal(e)
	}
	m, e := s.ReviewMessage(id)
	if e != nil || !m.HasPDF || m.CanParse || len(m.Attachments) != 1 || m.Attachments[0].Part != 1 || m.Body == "" {
		t.Fatalf("%+v %v", m, e)
	}
	s.SaveParser(fixture.AlertParser())
	s.Reprocess()
	rows, _ := s.Transactions(Filter{})
	if len(rows) > 0 {
		t.Fatal("inline PDF imported as alert")
	}
}

func TestRecoverLegacyHTMLArchivesOnOpen(t *testing.T) {
	dir := t.TempDir()
	s, e := Open(dir)
	if e != nil {
		t.Fatal(e)
	}
	ids := []int64{}
	for i := range 205 {
		id, e := s.Ingest(fixture.HTMLMail(fmt.Sprint(i), "<p>"+fixture.AlertBody+"</p>"))
		if e != nil {
			t.Fatal(e)
		}
		ids = append(ids, id)
	}
	// Simulate the old release: messages archived with no extracted body.
	s.DB.Exec("UPDATE messages SET body='',reason='MIME: no plain-text body; original archived'")
	s.DB.Exec("DELETE FROM settings WHERE key='mail-text-version'")
	s.SetSetting("cursor:test", "unchanged")
	s.DB.Close()
	s, e = Open(dir)
	if e != nil {
		t.Fatal(e)
	}
	defer s.DB.Close()
	var recovered int
	s.DB.QueryRow("SELECT count(*) FROM messages WHERE body<>'' AND reason='No matching alert parser'").Scan(&recovered)
	if recovered != 205 {
		t.Fatalf("recovered %d", recovered)
	}
	v, _ := s.Setting("cursor:test")
	if v != "unchanged" {
		t.Fatal("cursor changed")
	}
	if _, e = s.SaveParser(fixture.AlertParser()); e != nil {
		t.Fatal(e)
	}
	if _, e = s.Reprocess(); e != nil {
		t.Fatal(e)
	}
	var count int
	s.DB.QueryRow("SELECT count(*) FROM transactions").Scan(&count)
	if count != 205 {
		t.Fatal(count)
	}
	for _, id := range ids {
		m, e := s.Message(id)
		if e != nil || m.State != "parsed" {
			t.Fatal("message ID/state lost")
		}
	}
	if _, e = s.Reprocess(); e != nil {
		t.Fatal(e)
	}
	s.DB.QueryRow("SELECT count(*) FROM transactions").Scan(&count)
	if count != 205 {
		t.Fatal("duplicates")
	}
}

func TestMissingArchiveHasVisibleErrorAndCannotEscape(t *testing.T) {
	s := testStore(t)
	id, e := s.Ingest(fixture.HTMLMail("missing", "<p>hello</p>"))
	if e != nil {
		t.Fatal(e)
	}
	var name string
	s.DB.QueryRow("SELECT archive FROM messages WHERE id=?", id).Scan(&name)
	os.Remove(filepath.Join(s.Dir, "archive", name))
	m, e := s.ReviewMessage(id)
	if e != nil || m.ContentError == "" || m.CanParse {
		t.Fatalf("%+v %v", m, e)
	}
	s.DB.Exec("UPDATE messages SET archive='../ledger.db' WHERE id=?", id)
	if _, e = s.ArchivedRaw(id); e == nil {
		t.Fatal("archive path escaped")
	}
}

func TestStatementImportValidationOverlapAndMultipleAttachments(t *testing.T) {
	s := testStore(t)
	valid, err := statements.ParseHDFC(fixture.Statement)
	if err != nil {
		t.Fatal(err)
	}
	preview := PDFPreview{Statement: &valid, Fingerprint: statements.Fingerprint(valid)}
	second, err := statements.ParseHDFC(strings.ReplaceAll(fixture.Statement, "4242", "8080"))
	if err != nil {
		t.Fatal(err)
	}
	id, err := s.Ingest(fixture.StatementMail("two", fixture.Statement, strings.ReplaceAll(fixture.Statement, "4242", "8080")))
	if err != nil {
		t.Fatal(err)
	}
	failed := preview
	failed.ParseError = "missing row"
	if _, err = s.ImportStatement(id, 0, failed, preview.Fingerprint); err == nil {
		t.Fatal("invalid statement imported")
	}
	if _, err = s.ImportStatement(id, 0, preview, preview.Fingerprint); err != nil {
		t.Fatal(err)
	}
	m, _ := s.Message(id)
	if m.State != "queued" || !m.HasPDF {
		t.Fatal("other attachment hidden", m)
	}
	if err = s.Process(id); err != nil {
		t.Fatal(err)
	}
	preview2 := PDFPreview{Statement: &second, Fingerprint: statements.Fingerprint(second)}
	if _, err = s.ImportStatement(id, 1, preview2, preview2.Fingerprint); err != nil {
		t.Fatal(err)
	}
	rows, _ := s.Transactions(Filter{})
	if len(rows) != 6 {
		t.Fatal("multiple attachments missing", len(rows))
	}
	// A late alert confirms its statement line instead of adding another.
	p := fixture.AlertParser()
	p.Issuer = "HDFC"
	p.AccountKind = "card"
	s.SaveParser(p)
	alert, err := s.Ingest(fixture.Mail("late-alert", "INR 500.00 at Example Shop card 4242 on 2026-10-02"))
	if err != nil {
		t.Fatal(err)
	}
	m, _ = s.Message(alert)
	rows, _ = s.Transactions(Filter{Search: "EXAMPLE SHOP", Account: ledger.AccountKey("HDFC", "card", "4242")})
	if m.State != "parsed" || len(rows) != 1 || !rows[0].Matched || rows[0].Status != "confirmed" {
		t.Fatal("late alert not matched", m, rows)
	}
	if all, _ := s.Transactions(Filter{}); len(all) != 6 {
		t.Fatal("late alert double counted", len(all))
	}
	// A second alert for the same purchase has no line left to confirm.
	s.Ingest(fixture.Mail("late-again", "INR 500.00 at Example Shop card 4242 on 2026-10-02"))
	if all, _ := s.Transactions(Filter{}); len(all) != 7 {
		t.Fatal("second alert lost", len(all))
	}
	// Different content for the same account/date cannot overwrite the import.
	revision := valid
	revision.TotalDue++
	if _, err = s.ImportStatement(id, 0, PDFPreview{Statement: &revision}, statements.Fingerprint(revision)); err == nil {
		t.Fatal("revision silently replaced")
	}
}

func TestStatementImportReconcilesAlerts(t *testing.T) {
	s := testStore(t)
	p := fixture.AlertParser()
	p.Issuer = "HDFC"
	p.AccountKind = "card"
	s.SaveParser(p)
	alert := func(key, amount, date string) {
		t.Helper()
		if _, err := s.Ingest(fixture.Mail(key, "INR "+amount+" at Alert Name card 4242 on "+date)); err != nil {
			t.Fatal(err)
		}
	}
	alert("on-statement", "50.00", "2026-09-29")  // the grocer line, posted two days later
	alert("missing", "77.00", "2026-09-20")       // in the period, not on the statement
	alert("still-posting", "99.00", "2026-10-01") // too recent to be missing
	alert("older", "88.00", "2026-08-01")         // before the period
	other := fixture.AlertParser()
	other.Name, other.Issuer, other.AccountKind, other.Sender = "Other", "ICICI", "card", "icici@example.invalid"
	s.SaveParser(other)
	raw := strings.ReplaceAll(string(fixture.Mail("other-bank", "INR 500.00 at Shop card 4242 on 2026-10-01")), "alerts@example.invalid", other.Sender)
	s.Ingest([]byte(raw)) // same suffix and amount, different issuer

	id, err := s.Ingest(fixture.StatementMail("statement", fixture.Statement))
	if err != nil {
		t.Fatal(err)
	}
	st, _ := statements.ParseHDFC(fixture.Statement)
	out, err := s.ImportStatement(id, 0, PDFPreview{Statement: &st}, statements.Fingerprint(st))
	if err != nil || out.Matched != 1 || out.Flagged != 1 || out.Count != 3 {
		t.Fatalf("%+v %v", out, err)
	}
	status := map[string]string{}
	rows, _ := s.Transactions(Filter{})
	for _, r := range rows {
		status[r.Issuer+" "+ledger.Decimal(r.Amount)+" "+r.Merchant] = r.Status
	}
	want := map[string]string{
		"HDFC 50.00 Alert Name":                         "confirmed", // keeps the alert's name
		"HDFC 77.00 Alert Name":                         "flagged",
		"HDFC 99.00 Alert Name":                         "provisional",
		"HDFC 88.00 Alert Name":                         "provisional",
		"HDFC 500.00 EXAMPLE SHOP":                      "confirmed",
		"HDFC 600.00 CREDIT CARD PAYMENT (Ref# 123456)": "confirmed",
		"ICICI 500.00 Shop":                             "provisional",
	}
	if len(rows) != len(want) {
		t.Fatal(status)
	}
	for k, v := range want {
		if status[k] != v {
			t.Errorf("%s: %s, want %s", k, status[k], v)
		}
	}

	// A flagged alert can be dismissed out of the totals, and restored.
	flagged, _ := s.Transactions(Filter{Status: "flagged"})
	totals, _ := s.MonthTotals("2026-09")
	if err = s.Dismiss(flagged[0].ID, true); err != nil {
		t.Fatal(err)
	}
	after, _ := s.MonthTotals("2026-09")
	if after[0].Debit != totals[0].Debit-7700 {
		t.Fatal("dismissed transaction still counted", totals, after)
	}
	if s.Dismiss(rows[0].ID, true) == nil && rows[0].Status != "flagged" {
		t.Fatal("dismissed a transaction that wasn't flagged")
	}
	if err = s.Dismiss(flagged[0].ID, false); err != nil {
		t.Fatal(err)
	}

	// The next statement confirms the alert that was still posting, and
	// importing the first again changes nothing.
	next := strings.NewReplacer("02 Oct, 2026", "02 Nov, 2026", "22 Oct, 2026", "22 Nov, 2026",
		"C 50.00", "C 99.00", "C950.00", "C999.00", "+ C550.00", "+ C599.00").Replace(fixture.Statement)
	st2, err := statements.ParseHDFC(next)
	if err != nil || !st2.Balanced {
		t.Fatal(err, st2.Discrepancy)
	}
	id2, _ := s.Ingest(fixture.StatementMail("next", next))
	if out, err = s.ImportStatement(id2, 0, PDFPreview{Statement: &st2}, statements.Fingerprint(st2)); err != nil || out.Matched != 1 {
		t.Fatalf("%+v %v", out, err)
	}
	if out, err = s.ImportStatement(id, 0, PDFPreview{Statement: &st}, statements.Fingerprint(st)); err != nil || !out.AlreadyImported {
		t.Fatal("re-import changed something", err)
	}
	if all, _ := s.Transactions(Filter{}); len(all) != 9 {
		t.Fatal(len(all))
	}
	if left, _ := s.Transactions(Filter{Status: "provisional"}); len(left) != 2 {
		t.Fatal("late-posting alert not confirmed", left)
	}
	if left, _ := s.Transactions(Filter{Status: "flagged"}); len(left) != 1 {
		t.Fatal("missing alert no longer flagged", left)
	}
}

func TestAutomaticStatementImport(t *testing.T) {
	fixture.RequirePDFTools(t)
	s := testStore(t)
	p, err := s.SaveStatementParser(statements.Parser{Name: "HDFC Credit Card Parser v1", Adapter: "hdfc-credit-card", BalanceTolerancePaise: 99})
	if err != nil {
		t.Fatal(err)
	}
	other, _ := s.SaveStatementParser(statements.Parser{Name: "Second", Adapter: "hdfc-credit-card"})
	// No automatic imports yet: statements wait for review.
	first, _ := s.Ingest(fixture.StatementMail("first", fixture.Statement))
	if m, _ := s.Message(first); m.State != "queued" {
		t.Fatal(m.State)
	}
	if _, err = s.AddStatementTrigger(p.ID, first, 0); err != nil {
		t.Fatal(err)
	}
	if _, err = s.AddStatementTrigger(p.ID, first, 0); err != nil {
		t.Fatal("adding the same trigger again", err)
	}
	if _, err = s.AddStatementTrigger(other.ID, first, 0); err == nil {
		t.Fatal("two parsers would import the same statement")
	}
	saved, _ := s.StatementParsers()
	if len(saved[0].Triggers) != 1 || saved[0].Triggers[0].Sender != "statements@example.invalid" {
		t.Fatalf("%+v", saved[0])
	}

	// Retrying the backlog imports the first; new mail imports itself.
	if _, err = s.Reprocess(); err != nil {
		t.Fatal(err)
	}
	if m, _ := s.Message(first); m.State != "statement" {
		t.Fatal("backlog not imported", m.Reason)
	}
	next := strings.NewReplacer("4242", "8080").Replace(fixture.Statement)
	id, _ := s.Ingest(fixture.StatementMail("next", next))
	if m, _ := s.Message(id); m.State != "statement" {
		t.Fatal("new statement not imported", m.Reason)
	}
	if rows, _ := s.Transactions(Filter{}); len(rows) != 6 {
		t.Fatal(len(rows))
	}

	// Unbalanced, and from another sender: both wait with a reason.
	broken, _ := s.Ingest(fixture.StatementMail("broken", strings.Replace(next, "C 50.00", "C 51.00", 1)))
	if m, _ := s.Message(broken); m.State != "queued" || !strings.Contains(m.Reason, "automatic import with HDFC Credit Card Parser v1 failed") {
		t.Fatal(m.State, m.Reason)
	}
	raw := strings.Replace(string(fixture.StatementMail("stranger", next)), "statements@example.invalid", "other@example.invalid", 1)
	stranger, _ := s.Ingest([]byte(raw))
	if m, _ := s.Message(stranger); m.State != "queued" || !strings.Contains(m.Reason, "no statement parser imports this automatically") {
		t.Fatal(m.State, m.Reason)
	}
}

func TestPDFPreviewKeepsPartialRows(t *testing.T) {
	text := strings.Replace(fixture.Statement, "C 500.00", "? 500.00", 1)
	fixture.RequirePDFTools(t)
	s := testStore(t)
	id, err := s.Ingest(fixture.StatementMail("partial", text))
	if err != nil {
		t.Fatal(err)
	}
	p := statements.Parser{Adapter: "hdfc-credit-card", BalanceTolerancePaise: 99}
	v, err := s.PreviewPDF(t.Context(), id, 0, &p, nil)
	if err != nil || v.ParseError == "" || v.Statement == nil || len(v.Statement.Transactions) != 2 || v.Fingerprint != "" {
		t.Fatal("failed preview lost diagnosis", err)
	}
}

func TestPDFPreviewWithPasswordSlots(t *testing.T) {
	fixture.RequirePDFTools(t)
	dir := t.TempDir()
	plain := filepath.Join(dir, "plain.pdf")
	encrypted := filepath.Join(dir, "locked.pdf")
	os.WriteFile(plain, fixture.PDF(fixture.Statement), 0600)
	out, e := exec.Command("qpdf", "--encrypt", "fixture-two", "fixture-owner", "256", "--", plain, encrypted).CombinedOutput()
	if e != nil {
		t.Fatalf("encrypt fixture: %v %s", e, out)
	}
	raw, _ := os.ReadFile(encrypted)
	mime := []byte("From: statements@example.invalid\r\nSubject: Statement\r\nMIME-Version: 1.0\r\nContent-Type: multipart/mixed; boundary=x\r\n\r\n--x\r\nContent-Type: text/plain\r\n\r\nAttached.\r\n--x\r\nContent-Type: application/pdf\r\nContent-Disposition: attachment; filename=statement.pdf\r\nContent-Transfer-Encoding: base64\r\n\r\n" + base64.StdEncoding.EncodeToString(raw) + "\r\n--x--\r\n")
	s := testStore(t)
	id, e := s.Ingest(mime)
	if e != nil {
		t.Fatal(e)
	}
	p := statements.Parser{Name: "HDFC Credit Card", Adapter: "hdfc-credit-card"}
	result, e := s.PreviewPDF(t.Context(), id, 1, &p, []string{"fixture-one", "fixture-two"})
	if e != nil || result.ParseError != "" || result.Statement == nil || len(result.Statement.Transactions) != 3 || !result.Statement.Balanced || result.ParserName != p.Name {
		t.Fatalf("PDF preview: %v %s", e, result.ParseError)
	}
	rows, _ := s.Transactions(Filter{})
	if len(rows) != 0 {
		t.Fatal("preview imported transactions")
	}
	if _, e = s.PreviewPDF(t.Context(), id, 0, &p, []string{"fixture-two"}); e == nil {
		t.Fatal("accepted non-PDF MIME part")
	}
}

func TestSenderRecoveredOnOpen(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	s.SaveParser(fixture.AlertParser())
	raw := strings.Replace(string(fixture.Mail("blank-name", fixture.AlertBody)), "From: Example <alerts@example.invalid>", "From: =?UTF-8?B??= <alerts@example.invalid>", 1)
	id, _ := s.Ingest([]byte(raw))
	// As an older version stored it: no sender, so no parser matched.
	s.DB.Exec("UPDATE messages SET sender='' WHERE id=?; DELETE FROM transactions; UPDATE messages SET state='queued'; DELETE FROM settings WHERE key='sender-recovery'", id)
	s.Close()
	if s, err = Open(dir); err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if m, _ := s.Message(id); m.Sender != "alerts@example.invalid" {
		t.Fatalf("%q", m.Sender)
	}
	s.Reprocess()
	if rows, _ := s.Transactions(Filter{}); len(rows) != 1 {
		t.Fatal("recovered alert not parsed")
	}
}
