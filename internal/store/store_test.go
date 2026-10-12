package store

import (
	"github.com/lucaswren/s2l/internal/model"
	"path/filepath"
	"testing"
)

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
