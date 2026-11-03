package service

import (
	"context"
	"errors"
	"github.com/lucaswren/s2l/internal/model"
	"github.com/lucaswren/s2l/internal/speedtest"
	"github.com/lucaswren/s2l/internal/store"
	"path/filepath"
	"testing"
	"time"
)

func TestRecoveryBudgetIncludesBriefSuccessAndHalfOpen(t *testing.T) {
	var b recoveryBudget
	now := time.Now()
	for i := 0; i < 3; i++ {
		at := now.Add(time.Duration(i) * 20 * time.Second)
		if !b.allow(at) {
			t.Fatal("budget denied early")
		}
		if b.healthy(at.Add(time.Second)) {
			t.Fatal("brief success reset budget")
		}
	}
	if b.allow(now.Add(time.Minute)) {
		t.Fatal("flapping exhausted budget was accepted")
	}
	until := b.openUntil
	if b.allow(until.Add(-time.Nanosecond)) {
		t.Fatal("cooldown ignored")
	}
	if !b.allow(until) || !b.halfOpen {
		t.Fatal("no half-open attempt")
	}
	b.failed(until.Add(time.Second))
	if b.halfOpen || b.openUntil.Before(until.Add(recoveryCooldown)) {
		t.Fatal("failed trial didn't reopen")
	}
	if !b.allow(b.openUntil) {
		t.Fatal("second cooldown never retries")
	}
	at := b.openUntil
	b.healthy(at)
	for i := time.Second * 5; i < recoveryStable; i += 5 * time.Second {
		b.healthy(at.Add(i))
	}
	if !b.healthy(at.Add(recoveryStable)) || len(b.attempts) != 0 || !b.openUntil.IsZero() {
		t.Fatal("stable process didn't reset circuit")
	}
}

func TestObservationGapDoesNotClearRecoveryBudget(t *testing.T) {
	now := time.Now()
	b := recoveryBudget{attempts: []time.Time{now}}
	b.healthy(now)
	if b.healthy(now.Add(3*time.Minute)) || len(b.attempts) == 0 {
		t.Fatal("unobserved time counted as continuous stability")
	}
}

func TestProbeFinishingAfterManualRetryIsDiscarded(t *testing.T) {
	st, err := store.New(filepath.Join(t.TempDir(), "state.json"))
	if err != nil {
		t.Fatal(err)
	}
	node := model.SocksNode{ID: "n", Addr: "127.0.0.1:1080"}
	st.UpsertNode(node)
	m := model.TunnelMapping{ID: "m", NodeID: "n", Status: model.StatusRunning}
	st.UpsertMapping(m)
	s := NewMappingService(st, nil, nil, nil)
	h := &healthRecord{UpstreamHealth: model.UpstreamHealth{LocalState: "circuit-open"}}
	s.health[m.ID] = h
	generation := h.generation
	release, done := make(chan struct{}), make(chan struct{})
	go func() {
		defer close(done)
		<-release
		s.finishProbe(context.Background(), m, node, h, generation, speedtest.HealthReport{TCP: model.ProtocolHealth{State: "healthy"}})
	}()
	if err = s.RetryRecovery(m.ID); err != nil {
		close(release)
		<-done
		t.Fatal(err)
	}
	close(release)
	<-done
	if !h.CheckedAt.IsZero() || h.TCP.State != "checking" || h.successes != 0 {
		t.Fatal("stale probe crossed manual retry", h)
	}
}

func TestRecoveryCompletionInvalidatesMidRecoveryProbes(t *testing.T) {
	h := healthRecord{}
	now := time.Now()
	h.recovery.allow(now)
	generation := h.generation
	h.finishRecovery(nil, now)
	if h.generation == generation || h.TCP.State != "checking" || len(h.recovery.attempts) != 1 {
		t.Fatal("successful recovery accepted old probes or reset restart budget")
	}
	for i := 0; i < 2; i++ {
		at := now.Add(time.Duration(i+1) * time.Second)
		h.recovery.allow(at)
		h.finishRecovery(errors.New("restart failed"), at)
	}
	if h.LocalState != "circuit-open" || h.recovery.openUntil.IsZero() {
		t.Fatal("third failure didn't open circuit")
	}
}

func TestRecoveryBudgetWindowAndJitterBounded(t *testing.T) {
	now := time.Now()
	b := recoveryBudget{attempts: []time.Time{now.Add(-recoveryWindow), now.Add(-recoveryWindow)}}
	if !b.allow(now) || len(b.attempts) != 1 {
		t.Fatal("expired attempts consumed budget")
	}
	for n := 1; n < 10; n++ {
		for i := 0; i < 100; i++ {
			d := recoveryDelay(n)
			if d < retryDelay(n)*80/100 || d > 60*time.Second || d > retryDelay(n)*120/100 {
				t.Fatal("invalid jitter", d)
			}
		}
	}
}

func TestUpstreamSuccessCannotClearOpenCircuit(t *testing.T) {
	h := healthRecord{UpstreamHealth: model.UpstreamHealth{LocalState: "circuit-open", RecoveryError: "cooldown"}}
	h.observe(nil, time.Now())
	h.observe(nil, time.Now().Add(time.Second))
	if h.RecoveredAt.IsZero() == false || h.OutageSince.IsZero() == false {
		t.Fatal("unexpected upstream recovery timestamps")
	}
	if h.LocalState != "circuit-open" {
		t.Fatal("upstream reset breaker")
	}
}
