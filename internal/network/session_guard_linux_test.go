//go:build linux

package network

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/lucaswren/s2l/internal/model"
)

func TestSlowPeerCannotConsumeOtherAdmissionAndCommitBudget(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 600*time.Millisecond)
	defer cancel()
	mappings := []model.TunnelMapping{{ID: "slow"}, {ID: "healthy"}, {ID: "existing"}}
	previous := map[string]model.PPPSession{"existing": {Index: 10}}
	next := map[string]model.PPPSession{"slow": {Index: 11}, "healthy": {Index: 12}, "existing": {Index: 10}}
	var healthyChecked bool
	err := pinSessionReturns(ctx, mappings, next, previous, func(check context.Context, m model.TunnelMapping, _ model.PPPSession) error {
		if m.ID == "slow" {
			<-check.Done()
			return check.Err()
		}
		if m.ID == "existing" {
			t.Fatal("existing owner was unnecessarily repinned")
		}
		healthyChecked = true
		return check.Err()
	})
	if !errors.Is(err, context.DeadlineExceeded) || !healthyChecked {
		t.Fatal("fault injection did not exercise the slow peer", err)
	}
	if _, ok := next["slow"]; ok {
		t.Fatal("failed peer was admitted")
	}
	if next["healthy"].Index != 12 || next["existing"].Index != 10 || ctx.Err() != nil {
		t.Fatal("unrelated admission or final commit lost its budget", next, ctx.Err())
	}
}

func TestManyNewPeersMakeProgressWithoutUsingCommitBudget(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	var mappings []model.TunnelMapping
	next := map[string]model.PPPSession{}
	for i := 0; i < 40; i++ {
		id := fmt.Sprint(i)
		mappings = append(mappings, model.TunnelMapping{ID: id})
		next[id] = model.PPPSession{Index: i + 1}
	}
	_ = pinSessionReturns(ctx, mappings, next, nil, func(check context.Context, _ model.TunnelMapping, _ model.PPPSession) error {
		select {
		case <-time.After(8 * time.Millisecond):
			return nil
		case <-check.Done():
			return check.Err()
		}
	})
	if len(next) == 0 || len(next) == len(mappings) || ctx.Err() != nil {
		t.Fatal("large reconnect batch made no progress or starved the final commit", len(next), ctx.Err())
	}
}

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
