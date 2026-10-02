package ledger

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

func TestInboxDateOrderAndFiltersBeforeLimit(t *testing.T) {
	s := testStore(t)
	pdfID, err := s.Ingest(statementMail("older-pdf", syntheticStatement))
	if err != nil {
		t.Fatal(err)
	}
	// Dates deliberately differ from insertion order. UTC ordering matters too.
	for i := 0; i < 205; i++ {
		if _, err = s.Ingest(append([]byte("Date: Sat, 03 Oct 2026 01:00:00 +0530\r\n"), testMail(fmt.Sprint(i), testBody)...)); err != nil {
			t.Fatal(err)
		}
	}
	latest, err := s.Ingest(append([]byte("Date: Fri, 02 Oct 2026 23:00:00 +0000\r\n"), testMail("latest", testBody)...))
	if err != nil {
		t.Fatal(err)
	}
	// Higher ID but older date must not go first.
	s.Ingest(append([]byte("Date: Thu, 01 Oct 2026 00:00:00 +0530\r\n"), testMail("last-insert", testBody)...))
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
	p := StatementParser{Name: "HDFC Credit Card", Adapter: "hdfc-credit-card", BalanceTolerancePaise: 99}
	first, err := s.SaveStatementParser(p)
	if err != nil {
		t.Fatal(err)
	}
	second, err := s.SaveStatementParser(p)
	if err != nil || first.ID != second.ID {
		t.Fatal("duplicate preset created", err)
	}
	p.PasswordSlot = 1
	if _, err = s.SaveStatementParser(p); err != errParserNameTaken {
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
	s.SaveParser(testParser())
	id, err := s.Ingest(testMail("existing-alert", testBody))
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
	if err = s.migrateStatementImports(); err != nil {
		t.Fatal(err)
	}
	after, err := s.Transactions(Filter{})
	if err != nil || len(after) != 1 || after[0] != before[0] || after[0].MessageID != id {
		t.Fatal("upgrade altered alert", after, err)
	}
	if err = s.migrateStatementImports(); err != nil {
		t.Fatal("migration not repeatable", err)
	}
	var foreignKeyError string
	if err = s.DB.QueryRow("PRAGMA foreign_key_check").Scan(&foreignKeyError); err == nil {
		t.Fatal("broken foreign keys")
	}
	// Existing row still prevents processing the same alert again.
	s.Ingest(testMail("existing-alert", testBody))
	after, _ = s.Transactions(Filter{})
	if len(after) != 1 {
		t.Fatal("existing alert duplicated")
	}
}

func TestSharedSuffixDifferentIssuersStaySeparateOnImport(t *testing.T) {
	s := testStore(t)
	p := testParser()
	p.Issuer = "OTHER BANK"
	p.AccountKind = "card"
	s.SaveParser(p)
	s.Ingest(testMail("other-bank", "INR 500.00 at EXAMPLE SHOP card 4242 on 2026-10-01"))
	id, err := s.Ingest(statementMail("hdfc", syntheticStatement))
	if err != nil {
		t.Fatal(err)
	}
	st, err := ParseHDFCStatement(syntheticStatement)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.importStatement(id, 0, PDFPreview{Statement: &st}, statementFingerprint(st)); err != nil {
		t.Fatal("combined issuers", err)
	}
	rows, _ := s.Transactions(Filter{})
	if len(rows) != 4 {
		t.Fatal(len(rows))
	}
	// A missing row remains a validation failure even with a tiny closing discrepancy.
	st, err = ParseHDFCStatement(strings.Replace(syntheticStatement, "C 50.00", "C 49.85", 1))
	if err == nil || st.Balanced {
		t.Fatal("accepted incomplete rows")
	}
}
