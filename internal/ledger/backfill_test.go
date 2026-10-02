package ledger

import (
	"os"
	"path/filepath"
	"testing"
)

func TestBackfillCutoffRetiresOldQueueAndPreservesArchives(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	oldID, err := s.Ingest(datedMail("cutoff-old", "Wed, 31 Dec 2025 23:59:59 +0530"))
	if err != nil {
		t.Fatal(err)
	}
	old, _ := s.Message(oldID)
	if old.State != "excluded" {
		t.Fatalf("old mail is %s", old.State)
	}
	boundaryID, err := s.Ingest(datedMail("cutoff-boundary", "Thu, 01 Jan 2026 00:00:00 +0530"))
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
	if old.State != "excluded" || old.Reason != beforeBackfillReason {
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
