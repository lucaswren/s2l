package service

import (
	"errors"
	"github.com/lucaswren/s2l/internal/model"
	"time"
)

// A running mapping records the operator's desired state. Startup failures are
// represented by local health, and retain that intent for bounded retries.
func (s *MappingService) scheduleStartupRetry(m model.TunnelMapping, cause error) error {
	if err := s.store.UpdateMappingStatus(m.ID, model.StatusRunning, "启动恢复失败，等待自动重试"); err != nil {
		return err
	}
	now := time.Now()
	h := &healthRecord{UpstreamHealth: model.UpstreamHealth{TunName: m.TunName, State: "recovering", LocalState: "unavailable", LocalCheckedAt: now, RecoveryError: "启动恢复失败，等待自动重试"}, startupPending: true}
	h.recovery.allow(now)
	h.finishRecovery(cause, now)
	s.mu.Lock()
	s.health[m.ID] = h
	s.mu.Unlock()
	return nil
}

// Caller owns the per-mapping lifecycle lock, so a user stop cannot race a retry.
func (s *MappingService) retryStartup(m model.TunnelMapping, h *healthRecord) {
	now := time.Now()
	s.mu.Lock()
	if now.Before(h.localNext) || !h.recovery.allow(now) {
		if !h.recovery.openUntil.IsZero() {
			h.LocalState = "circuit-open"
			h.State = "circuit-open"
			h.localNext = h.recovery.openUntil
		}
		s.mu.Unlock()
		return
	}
	h.LocalState = "recovering"
	h.State = "recovering"
	s.mu.Unlock()
	err := s.startLocked(m.ID)
	if err != nil {
		err = errors.Join(err, s.store.UpdateMappingStatus(m.ID, model.StatusRunning, "启动恢复失败，等待自动重试"))
		s.mu.Lock()
		h.finishRecovery(err, time.Now())
		h.startupPending = true
		s.health[m.ID] = h
		s.mu.Unlock()
		return
	}
	s.mu.Lock()
	if fresh := s.health[m.ID]; fresh != nil {
		fresh.recovery = h.recovery
		fresh.recovery.healthy(time.Now())
	}
	s.mu.Unlock()
}

func (s *MappingService) SetStartupComplete() { s.mu.Lock(); s.startupComplete = true; s.mu.Unlock() }
func (s *MappingService) StartupComplete() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.startupComplete
}
