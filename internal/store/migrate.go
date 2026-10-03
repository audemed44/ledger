package store

import (
	"encoding/json"
	"github.com/audemed44/ledger/internal/mail"
	"os"
	"path/filepath"
	"strings"
)

const schema = `PRAGMA journal_mode=WAL; PRAGMA busy_timeout=5000; PRAGMA foreign_keys=ON;
CREATE TABLE IF NOT EXISTS messages(
  id INTEGER PRIMARY KEY, message_key TEXT UNIQUE NOT NULL, sender TEXT NOT NULL,
  subject TEXT NOT NULL, date TEXT NOT NULL, body TEXT NOT NULL,
  state TEXT NOT NULL DEFAULT 'queued', reason TEXT NOT NULL DEFAULT '', archive TEXT NOT NULL);
CREATE TABLE IF NOT EXISTS parsers(id INTEGER PRIMARY KEY, definition TEXT NOT NULL);
CREATE TABLE IF NOT EXISTS statement_parsers(id INTEGER PRIMARY KEY, definition TEXT NOT NULL);
CREATE TABLE IF NOT EXISTS transactions(
  id INTEGER PRIMARY KEY, message_id INTEGER UNIQUE NOT NULL REFERENCES messages(id),
  merchant TEXT NOT NULL, account TEXT NOT NULL, amount INTEGER NOT NULL CHECK(amount>0),
  currency TEXT NOT NULL, direction TEXT NOT NULL, date TEXT NOT NULL, reference TEXT NOT NULL,
  status TEXT NOT NULL, issuer TEXT NOT NULL);
CREATE INDEX IF NOT EXISTS transactions_date ON transactions(date);
CREATE TABLE IF NOT EXISTS settings(key TEXT PRIMARY KEY, value TEXT NOT NULL);
CREATE TABLE IF NOT EXISTS statement_links(message_id INTEGER PRIMARY KEY REFERENCES messages(id), outcome TEXT NOT NULL);
CREATE TABLE IF NOT EXISTS transfer_rules(
  id INTEGER PRIMARY KEY, issuer TEXT NOT NULL, pattern TEXT NOT NULL, example TEXT NOT NULL);
CREATE TABLE IF NOT EXISTS card_links(
  issuer TEXT NOT NULL, card TEXT NOT NULL, account TEXT NOT NULL, PRIMARY KEY(issuer,card));`

// migrate creates the schema and runs each upgrade. Every step is
// idempotent, so it runs on every start.
func (s *Store) migrate() error {
	if _, err := s.DB.Exec(schema); err != nil {
		return err
	}
	if err := os.Chmod(filepath.Join(s.Dir, "ledger.db"), 0600); err != nil {
		return err
	}
	for _, step := range []func() error{
		s.migrateAccountKinds,
		s.migrateStatementImports,
		s.migrateAlertLinks,
		s.migratePlaceholders,
		s.migrateTransfers,
		s.mergeDuplicateStatementParsers,
		s.retireBeforeBackfill,
		s.recoverArchivedText,
		s.recoverSenders,
	} {
		if err := step(); err != nil {
			return err
		}
	}
	return nil
}

// migrateAccountKinds adds transactions.account_kind. Existing rows become
// "unknown": a migration mustn't guess card or bank.
func (s *Store) migrateAccountKinds() error {
	return s.addColumn("transactions", "account_kind", "TEXT NOT NULL DEFAULT 'unknown'")
}

// migrateAlertLinks adds transactions.alert_message_id: the alert email
// matched to a statement line after the statement was imported.
func (s *Store) migrateAlertLinks() error {
	return s.addColumn("transactions", "alert_message_id", "INTEGER REFERENCES messages(id)")
}

// migratePlaceholders adds transactions.placeholder: the merchant is a
// parser's description, to be replaced by a statement's.
func (s *Store) migratePlaceholders() error {
	return s.addColumn("transactions", "placeholder", "INTEGER NOT NULL DEFAULT 0")
}

// migrateTransfers adds how a transaction was found to be a transfer, its
// other side, and whether you said it isn't one.
func (s *Store) migrateTransfers() error {
	for _, c := range [][2]string{
		{"transfer", "TEXT NOT NULL DEFAULT ''"},
		{"transfer_of", "INTEGER"},
		{"no_transfer", "INTEGER NOT NULL DEFAULT 0"},
	} {
		if err := s.addColumn("transactions", c[0], c[1]); err != nil {
			return err
		}
	}
	_, err := s.DB.Exec("CREATE INDEX IF NOT EXISTS transactions_amount ON transactions(amount)")
	return err
}

// addColumn adds a column unless it exists. table and column are constants.
func (s *Store) addColumn(table, column, definition string) error {
	rows, err := s.DB.Query("PRAGMA table_info(" + table + ")")
	if err != nil {
		return err
	}
	exists := false
	for rows.Next() {
		var cid, notnull, pk int
		var name, kind string
		var value any
		if err = rows.Scan(&cid, &name, &kind, &notnull, &value, &pk); err != nil {
			rows.Close()
			return err
		}
		if name == column {
			exists = true
		}
	}
	err = rows.Err()
	rows.Close()
	if err != nil || exists {
		return err
	}
	_, err = s.DB.Exec("ALTER TABLE " + table + " ADD COLUMN " + column + " " + definition)
	return err
}

// migrateStatementImports lets a statement add several rows per message,
// keeping IDs and alert provenance. All DDL runs in one transaction, so a
// failed upgrade leaves the old schema intact.
func (s *Store) migrateStatementImports() error {
	version, err := s.Setting("statement-import-schema")
	if err != nil || version == "1" {
		return err
	}
	tx, err := s.DB.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	_, err = tx.Exec(`
ALTER TABLE messages ADD COLUMN has_pdf INTEGER NOT NULL DEFAULT 0;
CREATE TABLE statements(
  id INTEGER PRIMARY KEY, account_key TEXT NOT NULL, date TEXT NOT NULL,
  fingerprint TEXT NOT NULL, snapshot TEXT NOT NULL, UNIQUE(account_key,date));
CREATE TABLE statement_sources(
  message_id INTEGER NOT NULL REFERENCES messages(id), part INTEGER NOT NULL,
  statement_id INTEGER NOT NULL REFERENCES statements(id), PRIMARY KEY(message_id,part));
CREATE TABLE transactions_new(
  id INTEGER PRIMARY KEY, message_id INTEGER NOT NULL REFERENCES messages(id),
  merchant TEXT NOT NULL, account TEXT NOT NULL, amount INTEGER NOT NULL CHECK(amount>0),
  currency TEXT NOT NULL, direction TEXT NOT NULL, date TEXT NOT NULL, reference TEXT NOT NULL,
  status TEXT NOT NULL, issuer TEXT NOT NULL, account_kind TEXT NOT NULL DEFAULT 'unknown',
  source_part INTEGER NOT NULL DEFAULT -1, row_index INTEGER NOT NULL DEFAULT 0,
  statement_id INTEGER REFERENCES statements(id),
  UNIQUE(message_id,source_part,row_index), UNIQUE(statement_id,row_index));
INSERT INTO transactions_new(id,message_id,merchant,account,amount,currency,direction,date,reference,status,issuer,account_kind)
  SELECT id,message_id,merchant,account,amount,currency,direction,date,reference,status,issuer,account_kind FROM transactions;
DROP TABLE transactions;
ALTER TABLE transactions_new RENAME TO transactions;
CREATE INDEX transactions_date ON transactions(date);
INSERT INTO settings(key,value) VALUES('statement-import-schema','1');`)
	if err != nil {
		return err
	}
	return tx.Commit()
}

// mergeDuplicateStatementParsers cleans up after older versions, which let
// identical presets be created repeatedly: the oldest of exact duplicates is
// kept, and distinct configurations are left alone.
func (s *Store) mergeDuplicateStatementParsers() error {
	parsers, err := s.StatementParsers()
	if err != nil {
		return err
	}
	seen := map[string]bool{}
	tx, err := s.DB.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, p := range parsers {
		if strings.EqualFold(strings.TrimSpace(p.Name), "HDFC Credit Card") {
			p.Name = "HDFC Credit Card Parser v1"
			raw, _ := json.Marshal(p)
			if _, err = tx.Exec("UPDATE statement_parsers SET definition=? WHERE id=?", string(raw), p.ID); err != nil {
				return err
			}
		}
		id := p.ID
		p.ID = 0
		p.Name = strings.ToLower(strings.TrimSpace(p.Name))
		raw, _ := json.Marshal(p)
		if seen[string(raw)] {
			if _, err = tx.Exec("DELETE FROM statement_parsers WHERE id=?", id); err != nil {
				return err
			}
		} else {
			seen[string(raw)] = true
		}
	}
	return tx.Commit()
}

// retireBeforeBackfill takes queued mail older than BackfillStart out of the
// inbox. Archives and imported transactions are kept.
func (s *Store) retireBeforeBackfill() error {
	_, err := s.DB.Exec(`UPDATE messages SET state='excluded',reason=?
WHERE state='queued' AND substr(date,1,10) < ? AND date GLOB '????-??-??T*' AND date NOT LIKE '0001-%'`,
		BeforeBackfillReason, BackfillStart)
	return err
}

// recoverArchivedText re-reads queued mail from the archive once, for
// deployments from before HTML extraction. Only queued bodies change: the
// cursor, IDs, transactions and archives stay intact, and a missing archive
// stays visible on its message rather than stopping startup.
func (s *Store) recoverArchivedText() error {
	const version = "html-text-pdf-v3"
	saved, err := s.Setting("mail-text-version")
	if err != nil || saved == version {
		return err
	}
	if err = s.recoverQueuedText(true); err != nil {
		return err
	}
	return s.SetSetting("mail-text-version", version)
}

// recoverSenders re-reads the sender of mail stored without one, once:
// older versions couldn't read a From header with an empty encoded name.
// A missing archive leaves the sender blank.
func (s *Store) recoverSenders() error {
	const version = "1"
	saved, err := s.Setting("sender-recovery")
	if err != nil || saved == version {
		return err
	}
	ids, err := s.queuedIDs("SELECT id FROM messages WHERE sender='' AND id>? ORDER BY id", 0)
	if err != nil {
		return err
	}
	for _, id := range ids {
		raw, e := s.ArchivedRaw(id)
		if e != nil {
			continue
		}
		if sender := mail.Decode(raw).Sender; sender != "" {
			if _, err = s.DB.Exec("UPDATE messages SET sender=? WHERE id=?", sender, id); err != nil {
				return err
			}
		}
	}
	return s.SetSetting("sender-recovery", version)
}
