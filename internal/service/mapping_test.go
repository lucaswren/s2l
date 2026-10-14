package service

import (
	"path/filepath"
	"testing"

	"github.com/lucaswren/s2l/internal/model"
	"github.com/lucaswren/s2l/internal/store"
)

func TestRestoreSkipsStoppedDeletedAndShutdownMappings(t *testing.T) {
	st, err := store.New(filepath.Join(t.TempDir(), "state.json"))
	if err != nil {
		t.Fatal(err)
	}
	m := model.TunnelMapping{ID: "m", Status: model.StatusRunning}
	if err := st.UpsertMapping(m); err != nil {
		t.Fatal(err)
	}
	s := NewMappingService(st, nil, nil, nil)
	// The restore snapshot can predate a user stop/delete. No runtime managers
	// are provided: attempting to start this stale mapping must never happen.
	if err := st.UpdateMappingStatus("m", model.StatusStopped, ""); err != nil {
		t.Fatal(err)
	}
	if restored, err := s.restoreMapping("m"); restored || err != nil {
		t.Fatalf("restored stopped mapping: %v, %v", restored, err)
	}
	if err := st.DeleteMapping("m"); err != nil {
		t.Fatal(err)
	}
	if restored, err := s.restoreMapping("m"); restored || err != nil {
		t.Fatalf("restored deleted mapping: %v, %v", restored, err)
	}
	if err := st.UpsertMapping(m); err != nil {
		t.Fatal(err)
	}
	s.shuttingDown = true
	if restored, err := s.restoreMapping("m"); restored || err != nil {
		t.Fatalf("restored during shutdown: %v, %v", restored, err)
	}
}

func TestInvalidPersistedMappingCannotRemainRunning(t *testing.T) {
	st, err := store.New(filepath.Join(t.TempDir(), "state.json"))
	if err != nil {
		t.Fatal(err)
	}
	m := model.TunnelMapping{ID: "invalid", Status: model.StatusRunning, TunName: "tun101", TableID: 254, Subnet: "10.0.101.0/24"}
	if err := st.UpsertMapping(m); err != nil {
		t.Fatal(err)
	}
	s := NewMappingService(st, nil, nil, nil)
	restored, err := s.restoreMapping(m.ID)
	if !restored || err == nil {
		t.Fatal("invalid persisted mapping was restored")
	}
	saved, _ := st.GetMapping(m.ID)
	if saved.Status != model.StatusError || saved.ErrorMsg == "" {
		t.Fatal("failed restore still reported running", saved)
	}
}

func TestRestoreDoesNotRestartAnAlreadyTrackedMapping(t *testing.T) {
	st, err := store.New(filepath.Join(t.TempDir(), "state.json"))
	if err != nil {
		t.Fatal(err)
	}
	m := model.TunnelMapping{ID: "m", Status: model.StatusRunning}
	if err := st.UpsertMapping(m); err != nil {
		t.Fatal(err)
	}
	s := NewMappingService(st, nil, nil, nil)
	s.health[m.ID] = &healthRecord{}
	if restored, err := s.restoreMapping(m.ID); restored || err != nil {
		t.Fatal("restore restarted a mapping already started by the API", restored, err)
	}
}
