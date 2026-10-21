package service

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/lucaswren/s2l/internal/model"
	"github.com/lucaswren/s2l/internal/store"
)

func TestGoogleMappingDiagnosticCannotStartOrSwitchMapping(t *testing.T) {
	st, err := store.New(filepath.Join(t.TempDir(), "state.json"))
	if err != nil {
		t.Fatal(err)
	}
	m := model.TunnelMapping{ID: "m", NodeID: "fixed", Status: model.StatusStopped, UDPPolicy: "reject", DNSMode: "tcp"}
	if err := st.UpsertMapping(m); err != nil {
		t.Fatal(err)
	}
	s := NewMappingService(st, nil, nil, nil)
	for _, nodeID := range []string{"fixed", "other"} {
		if _, err := s.ProbeGoogleMapping(context.Background(), "m", nodeID); err == nil {
			t.Fatal("stopped or wrong-node mapping reported available")
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := s.ProbeGoogleMapping(ctx, "m", "fixed"); err == nil {
		t.Fatal("cancelled check succeeded")
	}
	got, _ := st.GetMapping("m")
	if got != m {
		t.Fatal("diagnostic changed mapping", got)
	}
}
