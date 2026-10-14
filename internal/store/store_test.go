package store

import (
	"github.com/lucaswren/s2l/internal/model"
	"os"
	"path/filepath"
	"testing"
)

func TestFailedReplaceRestoresMemoryAndCleansTemporaryFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "state.json")
	s, err := New(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.UpsertNode(model.SocksNode{ID: "n", Name: "original"}); err != nil {
		t.Fatal(err)
	}
	reloaded, err := New(path)
	if err != nil || len(reloaded.ListNodes()) != 1 {
		t.Fatalf("saved state is unreadable: %v", err)
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(path, 0700); err != nil {
		t.Fatal(err)
	}
	if err := s.UpsertNode(model.SocksNode{ID: "n", Name: "changed"}); err == nil {
		t.Fatal("replace of directory succeeded")
	}
	n, _ := s.GetNode("n")
	if n.Name != "original" {
		t.Fatal("failed persistence changed memory")
	}
	files, err := filepath.Glob(filepath.Join(dir, ".s2l-state-*"))
	if err != nil || len(files) != 0 {
		t.Fatalf("temporary state leaked: %v %v", files, err)
	}
}

func TestActiveNodeConnectionSettingsCannotChange(t *testing.T) {
	s, err := New(filepath.Join(t.TempDir(), "state.json"))
	if err != nil {
		t.Fatal(err)
	}
	n := model.SocksNode{ID: "n", Addr: "127.0.0.1:1080"}
	if err := s.UpsertNode(n); err != nil {
		t.Fatal(err)
	}
	if err := s.UpsertMapping(model.TunnelMapping{ID: "m", NodeID: "n", Status: model.StatusRunning}); err != nil {
		t.Fatal(err)
	}
	n.Password = "changed"
	if err := s.UpsertNode(n); err == nil {
		t.Fatal("changed active credentials")
	}
	n.Password = ""
	n.Name = "renamed"
	if err := s.UpsertNode(n); err != nil {
		t.Fatal(err)
	}
	if err := s.UpdateMappingStatus("m", model.StatusStopped, ""); err != nil {
		t.Fatal(err)
	}
	n.Password = "changed"
	if err := s.UpsertNode(n); err != nil {
		t.Fatal(err)
	}
}
