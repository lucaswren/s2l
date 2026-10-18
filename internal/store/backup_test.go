package store

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/lucaswren/s2l/internal/model"
)

func TestBackupsBoundedAndStatusChangesDoNotCreateBackup(t *testing.T) {
	s, err := New(filepath.Join(t.TempDir(), "state.json"))
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 15; i++ {
		if err = s.UpsertNode(model.SocksNode{ID: "n", Name: fmt.Sprint(i)}); err != nil {
			t.Fatal(err)
		}
	}
	items, err := s.ListBackups()
	if err != nil || len(items) != backupLimit {
		t.Fatalf("retention: %d %v", len(items), err)
	}
	if err = s.UpsertMapping(model.TunnelMapping{ID: "m", Status: model.StatusStopped}); err != nil {
		t.Fatal(err)
	}
	before, _ := s.ListBackups()
	if err = s.UpdateMappingStatus("m", model.StatusRunning, ""); err != nil {
		t.Fatal(err)
	}
	after, _ := s.ListBackups()
	if len(before) != len(after) || before[0].ID != after[0].ID {
		t.Fatal("status update created backup")
	}
	if runtime.GOOS != "windows" {
		i, _ := os.Stat(filepath.Join(s.DataDir(), "backups", after[0].ID))
		if i.Mode().Perm() != 0600 {
			t.Fatal("backup permissions")
		}
	}
}

func TestRestoreChecksRevisionStoppedStateAndPreservesUndo(t *testing.T) {
	s, err := New(filepath.Join(t.TempDir(), "state.json"))
	if err != nil {
		t.Fatal(err)
	}
	n := model.SocksNode{ID: "n", Name: "before", Addr: "127.0.0.1:1080", Password: "secret"}
	if err = s.UpsertNode(n); err != nil {
		t.Fatal(err)
	}
	item, err := s.CreateBackup()
	if err != nil {
		t.Fatal(err)
	}
	b, err := s.ReadBackup(item.ID)
	if err != nil {
		t.Fatal(err)
	}
	n.Name = "after"
	if err = s.UpsertNode(n); err != nil {
		t.Fatal(err)
	}
	if err = s.Restore(b.State, "stale"); !errors.Is(err, ErrChanged) {
		t.Fatal("stale preview accepted", err)
	}
	if err = s.UpsertMapping(model.TunnelMapping{ID: "m", Status: model.StatusRunning}); err != nil {
		t.Fatal(err)
	}
	if err = s.Restore(b.State, Revision(s.Snapshot())); err == nil {
		t.Fatal("restored active configuration")
	}
	s.UpdateMappingStatus("m", model.StatusStopped, "")
	if err = s.Restore(b.State, Revision(s.Snapshot())); err != nil {
		t.Fatal(err)
	}
	got, _ := s.GetNode("n")
	if got.Name != "before" || got.Password != "secret" {
		t.Fatal("wrong restore", got.Name)
	}
	items, _ := s.ListBackups()
	undo, err := s.ReadBackup(items[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	if undo.State.Nodes[0].Name != "after" || undo.State.Mappings[0].Status != model.StatusStopped {
		t.Fatal("missing undo snapshot")
	}
	reloaded, err := New(s.path)
	if err != nil || reloaded.Snapshot().Nodes[0].Name != "before" {
		t.Fatal("restore was not durable", err)
	}
}

func TestBackupCorruptionAndUnsafePathRejected(t *testing.T) {
	s, _ := New(filepath.Join(t.TempDir(), "state.json"))
	item, err := s.CreateBackup()
	if err != nil {
		t.Fatal(err)
	}
	b, _ := s.ReadBackup(item.ID)
	b.State.Nodes = append(b.State.Nodes, model.SocksNode{ID: "tampered"})
	data, _ := json.Marshal(b)
	if err = os.WriteFile(filepath.Join(s.DataDir(), "backups", item.ID), data, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err = s.ReadBackup(item.ID); err == nil {
		t.Fatal("corrupt backup accepted")
	}
	if _, err = s.ReadBackup("../state.json"); err == nil {
		t.Fatal("path traversal accepted")
	}
}

func TestBackupFailurePreventsConfigurationMutation(t *testing.T) {
	s, _ := New(filepath.Join(t.TempDir(), "state.json"))
	if err := os.WriteFile(filepath.Join(s.DataDir(), "backups"), []byte("not a directory"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := s.UpsertNode(model.SocksNode{ID: "n"}); err == nil {
		t.Fatal("edit committed without backup")
	}
	if len(s.ListNodes()) != 0 {
		t.Fatal("failed backup changed memory")
	}
}

func TestOversizedConfigurationCannotDisableFutureBackups(t *testing.T) {
	s, _ := New(filepath.Join(t.TempDir(), "state.json"))
	if err := s.UpsertNode(model.SocksNode{ID: "too-large", Name: strings.Repeat("x", 4<<20)}); err == nil {
		t.Fatal("oversized state committed")
	}
	if len(s.ListNodes()) != 0 {
		t.Fatal("oversized edit changed memory")
	}
	if err := s.UpsertNode(model.SocksNode{ID: "small"}); err != nil {
		t.Fatal("failed growth blocked later edits", err)
	}
}

func TestCorruptPrimaryRecoversStoppedAndPreservesOriginal(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	s, err := New(path)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.UpsertNode(model.SocksNode{ID: "n", Name: "known-good", Addr: "127.0.0.1:1080"}); err != nil {
		t.Fatal(err)
	}
	if err = s.UpsertMapping(model.TunnelMapping{ID: "m", NodeID: "n", TunName: "tun101", TableID: 101, Subnet: "10.0.101.0/24", L2TPUser: "user", L2TPPass: "pass", Status: model.StatusRunning}); err != nil {
		t.Fatal(err)
	}
	if _, err = s.CreateBackup(); err != nil {
		t.Fatal(err)
	}
	original := []byte(`{"nodes": [broken`)
	if err = os.WriteFile(path, original, 0600); err != nil {
		t.Fatal(err)
	}
	recovered, err := New(path)
	if err != nil {
		t.Fatal(err)
	}
	if recovered.RecoveryNotice() == "" || len(recovered.ListMappings()) != 1 || recovered.ListMappings()[0].Status != model.StatusStopped {
		t.Fatal("unsafe recovery")
	}
	paths, _ := filepath.Glob(filepath.Join(s.DataDir(), "backups", "corrupt-*.json"))
	if len(paths) != 1 {
		t.Fatal("missing corrupt evidence")
	}
	data, _ := os.ReadFile(paths[0])
	if string(data) != string(original) {
		t.Fatal("corrupt original changed")
	}
	if _, err = New(path); err != nil {
		t.Fatal("recovered primary invalid", err)
	}
}

func TestCorruptPrimaryWithoutValidBackupFailsClosed(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	original := []byte("not-json")
	os.WriteFile(path, original, 0600)
	if _, err := New(path); err == nil {
		t.Fatal("silently lost configuration")
	}
	data, _ := os.ReadFile(path)
	if string(data) != string(original) {
		t.Fatal("destroyed invalid primary")
	}
}
