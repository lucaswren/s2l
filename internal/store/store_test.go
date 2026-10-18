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
	if err := s.UpsertNode(model.SocksNode{ID: "n", Name: "original", Addr: "127.0.0.1:1080"}); err != nil {
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

func TestFailedCleanupProtectsMappingAndNodeUntilStopped(t *testing.T) {
	s, err := New(filepath.Join(t.TempDir(), "state.json"))
	if err != nil {
		t.Fatal(err)
	}
	node := model.SocksNode{ID: "n", Addr: "127.0.0.1:1080", Password: "original"}
	if err = s.UpsertNode(node); err != nil {
		t.Fatal(err)
	}
	m := model.TunnelMapping{ID: "m", NodeID: "n", TunName: "tun101", TableID: 101, Subnet: "10.0.101.0/24", L2TPUser: "u", L2TPPass: "p", Status: model.StatusError, ErrorMsg: "cleanup failed"}
	if err = s.UpsertMapping(m); err != nil {
		t.Fatal(err)
	}
	changed := m
	changed.TunName = "tun102"
	if err = s.UpsertMapping(changed); err == nil {
		t.Fatal("lost old TUN identity after failed cleanup")
	}
	changed = m
	changed.Status = model.StatusStopped
	if err = s.UpsertMapping(changed); err == nil {
		t.Fatal("upsert bypassed lifecycle cleanup")
	}
	node.Password = "changed"
	if err = s.UpsertNode(node); err == nil {
		t.Fatal("changed credentials still owned by failed mapping")
	}
	saved, _ := s.GetMapping("m")
	if saved != m {
		t.Fatal("rejected mutation changed state")
	}
	// Explicit defaults don't change the effective runtime configuration.
	equivalent := m
	equivalent.MTU = m.EffectiveMTU()
	equivalent.DNSMode = m.EffectiveDNSMode()
	if err = s.UpsertMapping(equivalent); err != nil {
		t.Fatal(err)
	}
	if err = s.UpdateMappingStatus("m", model.StatusStopped, ""); err != nil {
		t.Fatal(err)
	}
	changed.Status = model.StatusStopped
	changed.TunName = "tun102"
	if err = s.UpsertMapping(changed); err != nil {
		t.Fatal("stopped mapping cannot be edited", err)
	}
	if err = s.UpsertNode(node); err != nil {
		t.Fatal("stopped node cannot be edited", err)
	}
}
