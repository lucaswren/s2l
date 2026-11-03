//go:build linux

package network

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/lucaswren/s2l/internal/model"
)

func TestSessionRefreshDeadlineWithBusyController(t *testing.T) {
	controller := NewController()
	controller.mu.Lock()
	defer controller.mu.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- controller.SyncSessionGuard(ctx) }()
	select {
	case err := <-done:
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("session refresh did not propagate the deadline: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("session refresh blocked behind controller work")
	}
}

func TestSessionAdmissionUsesAssignedAddressAndKeepsExistingOwner(t *testing.T) {
	mappings := []model.TunnelMapping{
		{ID: "sg", Subnet: "10.0.101.0/24"},
		{ID: "us", Subnet: "10.0.102.0/24"},
	}
	original := model.PPPSession{MappingID: "sg", Interface: "ppp1", Index: 11, PeerIP: "10.0.101.2"}
	duplicate := model.PPPSession{MappingID: "sg", Interface: "ppp0", Index: 10, PeerIP: "10.0.101.2"}
	wrong := model.PPPSession{MappingID: "us", Interface: "ppp2", Index: 12, PeerIP: "10.0.102.3"}
	previous := map[string]model.PPPSession{"sg": original}
	admitted := selectSessionPeers(mappings, []model.PPPSession{duplicate, original, wrong}, previous)
	if len(admitted) != 1 || admitted["sg"].Index != original.Index {
		t.Fatalf("duplicate or unassigned peer displaced original: %+v", admitted)
	}
	admitted = selectSessionPeers(mappings, []model.PPPSession{duplicate}, previous)
	if len(admitted) != 1 || admitted["sg"].Index != duplicate.Index {
		t.Fatalf("remaining authenticated peer was not admitted: %+v", admitted)
	}
}
