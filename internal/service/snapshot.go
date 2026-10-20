package service

import (
	"github.com/lucaswren/s2l/internal/model"
	"time"
)

// Shared read-only runtime snapshot. Configuration remains live so lifecycle
// changes are visible immediately even while the last runtime sample is reused.
func (s *MappingService) CachedStatus() model.SystemStatus {
	s.snapshotMu.Lock()
	defer s.snapshotMu.Unlock()
	if s.snapshotAt.IsZero() || time.Since(s.snapshotAt) >= 5*time.Second {
		s.snapshot = s.Status()
		s.snapshotAt = time.Now()
	}
	result := s.snapshot
	result.Mappings = s.store.ListMappings()
	result.CollectedAt = s.snapshotAt
	return result
}
