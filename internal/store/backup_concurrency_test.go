package store

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/lucaswren/s2l/internal/model"
)

func TestClockRollbackKeepsNewestBackupAndUndo(t *testing.T) {
	s, _ := New(filepath.Join(t.TempDir(), "state.json"))
	future := time.Now().Add(time.Hour)
	s.now = func() time.Time { return future }
	for i := 0; i < 12; i++ {
		if err := s.UpsertNode(model.SocksNode{ID: "n", Name: fmt.Sprint(i), Addr: "127.0.0.1:1080"}); err != nil {
			t.Fatal(err)
		}
	}
	items, _ := s.ListBackups()
	target, err := s.ReadBackup(items[3].ID)
	if err != nil {
		t.Fatal(err)
	}
	s.now = func() time.Time { return future.Add(-24 * time.Hour) }
	newest, err := s.CreateBackup()
	if err != nil {
		t.Fatal(err)
	}
	items, _ = s.ListBackups()
	if items[0].ID != newest.ID {
		t.Fatal("clock rollback lost newest backup")
	}
	if _, err = s.ReadBackup(newest.ID); err != nil {
		t.Fatal("fresh backup pruned", err)
	}
	if err = s.Restore(target.State, Revision(s.Snapshot())); err != nil {
		t.Fatal(err)
	}
	items, _ = s.ListBackups()
	undo, err := s.ReadBackup(items[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	if undo.State.Nodes[0].Name != "11" {
		t.Fatal("undo snapshot was pruned or ordered incorrectly")
	}
}

func TestConcurrentBackupReadCreateAndPrune(t *testing.T) {
	s, _ := New(filepath.Join(t.TempDir(), "state.json"))
	var wg sync.WaitGroup
	for worker := 0; worker < 4; worker++ {
		wg.Add(1)
		go func(worker int) {
			defer wg.Done()
			for i := 0; i < 20; i++ {
				if worker < 2 {
					if _, err := s.CreateBackup(); err != nil {
						t.Error(err)
					}
					continue
				}
				items, err := s.ListBackups()
				if err != nil {
					t.Error(err)
					continue
				}
				for _, item := range items {
					if _, err := s.ReadBackup(item.ID); err != nil && !os.IsNotExist(err) {
						t.Error("concurrent reader saw incomplete/corrupt backup", err)
					}
				}
			}
		}(worker)
	}
	wg.Wait()
	items, err := s.ListBackups()
	if err != nil || len(items) != backupLimit {
		t.Fatal("retention failed", len(items), err)
	}
}

func TestCompetingRestoresRejectStaleConfiguration(t *testing.T) {
	s, _ := New(filepath.Join(t.TempDir(), "state.json"))
	if err := s.UpsertNode(model.SocksNode{ID: "n", Addr: "127.0.0.1:1080"}); err != nil {
		t.Fatal(err)
	}
	revision := Revision(s.Snapshot())
	results := make(chan error, 2)
	start := make(chan struct{})
	for _, name := range []string{"A", "B"} {
		go func(name string) {
			<-start
			results <- s.Restore(model.AppState{Nodes: []model.SocksNode{{ID: "n", Name: name, Addr: "127.0.0.1:1080"}}}, revision)
		}(name)
	}
	close(start)
	success, stale := 0, 0
	for i := 0; i < 2; i++ {
		err := <-results
		if err == nil {
			success++
		} else if errors.Is(err, ErrChanged) {
			stale++
		} else {
			t.Fatal(err)
		}
	}
	if success != 1 || stale != 1 {
		t.Fatal("competing restores both committed", success, stale)
	}
	reloaded, err := New(s.path)
	if err != nil || Revision(reloaded.Snapshot()) != Revision(s.Snapshot()) {
		t.Fatal("disk and memory diverged", err)
	}
}

func TestDirectorySyncFailureAfterCommitKeepsMemoryConsistent(t *testing.T) {
	s, _ := New(filepath.Join(t.TempDir(), "state.json"))
	n := model.SocksNode{ID: "n", Name: "old", Addr: "127.0.0.1:1080"}
	if err := s.UpsertNode(n); err != nil {
		t.Fatal(err)
	}
	s.syncDir = func(path string) error {
		if path == s.DataDir() {
			return errors.New("injected fsync failure")
		}
		return nil
	}
	n.Name = "committed"
	err := s.UpsertNode(n)
	var committed *committedError
	if !errors.As(err, &committed) {
		t.Fatal("durability failure hidden", err)
	}
	reloaded, err := New(s.path)
	if err != nil {
		t.Fatal(err)
	}
	if Revision(reloaded.Snapshot()) != Revision(s.Snapshot()) {
		t.Fatal("post-rename error rolled back memory only")
	}
}

func TestCorruptPrimarySkipsChecksumValidButUnusableBackup(t *testing.T) {
	s, _ := New(filepath.Join(t.TempDir(), "state.json"))
	if err := s.UpsertNode(model.SocksNode{ID: "n", Name: "good", Addr: "127.0.0.1:1080"}); err != nil {
		t.Fatal(err)
	}
	s.CreateBackup()
	// Simulate a legacy snapshot whose checksum is valid but whose references
	// cannot be safely restored. Recovery must continue to an older usable one.
	if err := s.UpsertMapping(model.TunnelMapping{ID: "broken", NodeID: "missing"}); err != nil {
		t.Fatal(err)
	}
	s.CreateBackup()
	os.WriteFile(s.path, []byte("invalid-json"), 0600)
	recovered, err := New(s.path)
	if err != nil {
		t.Fatal(err)
	}
	if len(recovered.ListMappings()) != 0 || len(recovered.ListNodes()) != 1 || recovered.ListNodes()[0].Name != "good" {
		t.Fatal("selected unusable newest backup")
	}
}

func TestCanceledRestoreDoesNotCommitAfterWaitingOrBackup(t *testing.T) {
	for _, cancelDuringBackup := range []bool{false, true} {
		t.Run(fmt.Sprint(cancelDuringBackup), func(t *testing.T) {
			s, _ := New(filepath.Join(t.TempDir(), "state.json"))
			if err := s.UpsertNode(model.SocksNode{ID: "n", Name: "original", Addr: "127.0.0.1:1080"}); err != nil {
				t.Fatal(err)
			}
			before := s.Snapshot()
			target := cloneState(before)
			target.Nodes[0].Name = "replacement"
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if cancelDuringBackup {
				s.syncDir = func(path string) error {
					if filepath.Base(path) == "backups" {
						cancel()
					}
					return nil
				}
			} else {
				cancel()
			}
			if err := s.RestoreContext(ctx, target, Revision(before)); !errors.Is(err, context.Canceled) {
				t.Fatal("canceled restore accepted", err)
			}
			reloaded, err := New(s.path)
			if err != nil || Revision(reloaded.Snapshot()) != Revision(before) || Revision(s.Snapshot()) != Revision(before) {
				t.Fatal("canceled rollback mutated primary", err)
			}
		})
	}
}

func TestBackupSyncFailureDoesNotPublishOrMutate(t *testing.T) {
	s, _ := New(filepath.Join(t.TempDir(), "state.json"))
	if err := s.UpsertNode(model.SocksNode{ID: "n", Addr: "127.0.0.1:1080"}); err != nil {
		t.Fatal(err)
	}
	before := s.Snapshot()
	items, _ := s.ListBackups()
	s.syncDir = func(string) error { return errors.New("injected backup sync failure") }
	if err := s.UpsertNode(model.SocksNode{ID: "n", Name: "must-not-commit", Addr: "127.0.0.1:1080"}); err == nil {
		t.Fatal("mutation bypassed backup failure")
	}
	after, _ := s.ListBackups()
	if Revision(before) != Revision(s.Snapshot()) || len(items) != len(after) {
		t.Fatal("failed backup published or mutated configuration")
	}
	paths, _ := filepath.Glob(filepath.Join(s.DataDir(), "backups", ".backup-*"))
	if len(paths) != 0 {
		t.Fatal("failed backup leaked temporary files")
	}
}
