package store

import (
	"errors"
	"path/filepath"
	"testing"

	"github.com/lucaswren/s2l/internal/model"
	"github.com/lucaswren/s2l/internal/validation"
)

func TestWANEgressPersistsAndCannotChangeWhileActive(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	s, err := New(path)
	if err != nil {
		t.Fatal(err)
	}
	n := model.SocksNode{ID: "wan", Egress: "wan", WANInterface: "eth0"}
	if err := s.UpsertNode(n); err != nil {
		t.Fatal(err)
	}
	m := model.TunnelMapping{ID: "m", NodeID: n.ID, TunName: "tun101", TableID: 101, Subnet: "10.0.101.0/24", L2TPUser: "u", L2TPPass: "p", Status: model.StatusRunning}
	if err := s.UpsertMapping(m); err != nil {
		t.Fatal(err)
	}
	for _, change := range []model.SocksNode{{ID: n.ID, Egress: "wan", WANInterface: "eth1"}, {ID: n.ID, Addr: "127.0.0.1:1080"}} {
		if err := s.UpsertNode(change); !errors.Is(err, ErrNodeInUse) {
			t.Fatalf("active exit changed: %v", err)
		}
	}
	restored, err := New(path)
	if err != nil {
		t.Fatal(err)
	}
	got, ok := restored.GetNode(n.ID)
	if !ok || got != n {
		t.Fatal("WAN binding lost on restore", got)
	}
	if err := validation.State(model.AppState{Nodes: []model.SocksNode{n}, Mappings: []model.TunnelMapping{m}}); err != nil {
		t.Fatal(err)
	}
}

func TestWANAndTUNNameConflictsRollBackBothCreationOrdersAndEdits(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	s, err := New(path)
	if err != nil {
		t.Fatal(err)
	}
	n := model.SocksNode{ID: "socks", Addr: "127.0.0.1:1080"}
	if err = s.UpsertNode(n); err != nil {
		t.Fatal(err)
	}
	m := model.TunnelMapping{ID: "m", NodeID: n.ID, TunName: "missingwan0", Status: model.StatusStopped}
	if err = s.UpsertMapping(m); err != nil {
		t.Fatal(err)
	}
	wan := model.SocksNode{ID: "wan", Egress: "wan", WANInterface: m.TunName}
	if s.UpsertNode(wan) == nil {
		t.Fatal("WAN creation took over a managed TUN name")
	}
	if _, ok := s.GetNode(wan.ID); ok {
		t.Fatal("failed WAN save changed memory")
	}
	if err = s.DeleteMapping(m.ID); err != nil {
		t.Fatal(err)
	}
	if err = s.UpsertNode(wan); err != nil {
		t.Fatal(err)
	}
	if s.UpsertMapping(m) == nil {
		t.Fatal("TUN creation took over an absent WAN name")
	}
	if _, ok := s.GetMapping(m.ID); ok {
		t.Fatal("failed TUN save changed memory")
	}
	m.TunName = "tun101"
	if err = s.UpsertMapping(m); err != nil {
		t.Fatal(err)
	}
	wan.WANInterface = m.TunName
	if s.UpsertNode(wan) == nil {
		t.Fatal("WAN edit took over an existing TUN")
	}
	restored, err := New(path)
	if err != nil {
		t.Fatal(err)
	}
	got, _ := restored.GetNode(wan.ID)
	if got.WANInterface != "missingwan0" {
		t.Fatal("failed edit corrupted persisted WAN binding", got)
	}
}
