package store

import (
	"os"
	"path/filepath"
	"testing"
)

func TestBackupCleanupIsBoundedAndDoesNotTouchCurrentState(t *testing.T) {
	s, err := New(filepath.Join(t.TempDir(), "state.json"))
	if err != nil {
		t.Fatal(err)
	}
	older, err := s.CreateBackup()
	if err != nil {
		t.Fatal(err)
	}
	latest, err := s.CreateBackup()
	if err != nil {
		t.Fatal(err)
	}
	before := Revision(s.Snapshot())
	for _, ids := range [][]string{{older.ID, "../state.json"}, {older.ID, older.ID}, {older.ID, "backup-0-0.json"}, nil} {
		if err := s.DeleteBackups(ids); err == nil {
			t.Fatal("invalid selection accepted", ids)
		}
		if _, err := s.ReadBackup(older.ID); err != nil {
			t.Fatal("invalid batch partially deleted", err)
		}
	}
	if err := s.DeleteBackups([]string{older.ID}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ReadBackup(older.ID); !os.IsNotExist(err) {
		t.Fatal("selected backup still present", err)
	}
	if _, err := s.ReadBackup(latest.ID); err != nil {
		t.Fatal("latest backup lost", err)
	}
	if Revision(s.Snapshot()) != before {
		t.Fatal("cleanup changed live configuration")
	}
}
