package service

import (
	"github.com/lucaswren/s2l/internal/model"
	"github.com/lucaswren/s2l/internal/singbox"
	"github.com/lucaswren/s2l/internal/store"
	"path/filepath"
	"testing"
	"time"
)

func TestStartReadsStateAfterSharedLifecycleTransaction(t *testing.T) {
	st, err := store.New(filepath.Join(t.TempDir(), "state.json"))
	if err != nil {
		t.Fatal(err)
	}
	s := NewMappingService(st, nil, nil, nil)
	s.lifecycleMu.Lock()
	locked := true
	defer func() {
		if locked {
			s.lifecycleMu.Unlock()
		}
	}()
	done := make(chan error, 1)
	go func() { done <- s.Start("late-mapping") }()
	select {
	case err := <-done:
		t.Fatalf("start bypassed shared transaction: %v", err)
	case <-time.After(50 * time.Millisecond):
	}
	// Simulate the preceding transaction committing the active state. Start
	// must observe this mapping, rather than the pre-commit 'not found' state.
	if err := st.UpsertMapping(model.TunnelMapping{ID: "late-mapping", Status: model.StatusRunning, TableID: 254}); err != nil {
		t.Fatal(err)
	}
	s.lifecycleMu.Unlock()
	locked = false
	select {
	case err := <-done:
		m, _ := st.GetMapping("late-mapping")
		if err == nil || m.Status != model.StatusError {
			t.Fatal("start used stale state", err, m)
		}
	case <-time.After(time.Second):
		t.Fatal("start did not resume after transaction")
	}
}

func TestIndependentMappingLockAndStatusRemainResponsive(t *testing.T) {
	st, err := store.New(filepath.Join(t.TempDir(), "state.json"))
	if err != nil {
		t.Fatal(err)
	}
	s := NewMappingService(st, singbox.NewManager(""), nil, nil)
	s.stateGate.RLock()
	defer s.stateGate.RUnlock()
	unlock := s.lockMapping("slow-mapping")
	defer unlock()
	done := make(chan struct{})
	go func() { unlock := s.lockMapping("other-mapping"); unlock(); _ = s.Status(); close(done) }()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("one mapping's operation blocked unrelated locks or status")
	}
}
