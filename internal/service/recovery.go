package service

import (
	"fmt"
	"math/rand"
	"time"

	"github.com/lucaswren/s2l/internal/model"
)

const recoveryWindow = 10 * time.Minute
const recoveryCooldown = 5 * time.Minute
const recoveryStable = 2 * time.Minute
const recoveryBudgetLimit = 3

// Count all restart attempts, including briefly successful restarts. A flapping
// process must not reset its budget merely by creating a TUN once.
type recoveryBudget struct {
	attempts     []time.Time
	openUntil    time.Time
	halfOpen     bool
	healthySince time.Time
	lastHealthy  time.Time
}

func (b *recoveryBudget) allow(now time.Time) bool {
	b.healthySince = time.Time{}
	b.lastHealthy = time.Time{}
	i := 0
	for i < len(b.attempts) && !b.attempts[i].After(now.Add(-recoveryWindow)) {
		i++
	}
	b.attempts = b.attempts[i:]
	if !b.openUntil.IsZero() {
		if now.Before(b.openUntil) {
			return false
		}
		if b.halfOpen {
			b.trip(now)
			return false
		}
		b.halfOpen = true
		b.attempts = append(b.attempts, now)
		return true
	}
	if len(b.attempts) >= recoveryBudgetLimit {
		b.trip(now)
		return false
	}
	b.attempts = append(b.attempts, now)
	return true
}
func (b *recoveryBudget) trip(now time.Time) {
	b.openUntil = now.Add(recoveryCooldown)
	b.halfOpen = false
	b.healthySince = time.Time{}
	b.lastHealthy = time.Time{}
}
func (b *recoveryBudget) failed(now time.Time) {
	if b.halfOpen || len(b.attempts) >= recoveryBudgetLimit {
		b.trip(now)
	}
}
func (b *recoveryBudget) healthy(now time.Time) bool {
	if b.healthySince.IsZero() || b.lastHealthy.IsZero() || now.Sub(b.lastHealthy) > 15*time.Second || now.Before(b.lastHealthy) {
		b.healthySince = now
	}
	b.lastHealthy = now
	if now.Sub(b.healthySince) >= recoveryStable {
		*b = recoveryBudget{healthySince: b.healthySince, lastHealthy: now}
		return true
	}
	return false
}
func (b *recoveryBudget) status(next time.Time, failures int) model.RecoveryStatus {
	state := "closed"
	if !b.openUntil.IsZero() {
		state = "open"
		next = b.openUntil
	}
	if b.halfOpen {
		state = "half-open"
		next = time.Time{}
	}
	return model.RecoveryStatus{Circuit: state, Attempts: len(b.attempts), Failures: failures, NextRetry: next, HealthySince: b.healthySince}
}

func recoveryDelay(n int) time.Duration {
	d := retryDelay(n)
	// Bounded jitter avoids synchronized retries across mappings after an outage.
	d = d * time.Duration(80+rand.Intn(41)) / 100
	if d > 60*time.Second {
		d = 60 * time.Second
	}
	return d
}

// RetryRecovery schedules one new recovery budget without disrupting a live
// process. The monitor executes it using the existing per-mapping lifecycle lock.
func (s *MappingService) RetryRecovery(id string) error {
	s.stateGate.RLock()
	defer s.stateGate.RUnlock()
	unlock := s.lockMapping(id)
	defer unlock()
	m, ok := s.store.GetMapping(id)
	s.mu.Lock()
	defer s.mu.Unlock()
	h := s.health[id]
	if !ok || m.Status != model.StatusRunning || h == nil || s.shuttingDown {
		return fmt.Errorf("mapping is not monitored; start it first")
	}
	h.recovery = recoveryBudget{}
	h.recoveryFailures = 0
	h.localNext = time.Time{}
	h.invalidateProtocols()
	h.successes = 0
	h.next = time.Time{}
	h.baseState = "checking"
	h.State = "checking"
	h.Error = ""
	h.LocalState = "checking"
	h.RecoveryError = ""
	return nil
}
