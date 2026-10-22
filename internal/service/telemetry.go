package service

import (
	"context"
	"sync"
	"time"

	"github.com/lucaswren/s2l/internal/debuglog"
	"github.com/lucaswren/s2l/internal/history"
	"github.com/lucaswren/s2l/internal/operations"
)

func (s *MappingService) runTelemetry(ctx context.Context) {
	var delivery sync.WaitGroup
	wake := make(chan struct{}, 1)
	delivery.Add(1)
	go func() {
		defer delivery.Done()
		for {
			select {
			case <-ctx.Done():
				return
			case <-wake:
				s.operations.Deliver(ctx)
			}
		}
	}()
	defer delivery.Wait()
	tick := time.NewTicker(5 * time.Second)
	defer tick.Stop()
	defer s.operations.Flush()
	if s.history != nil {
		defer s.history.Flush()
	}
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		}
		status := s.CachedStatus()
		now := time.Now()
		s.operations.Observe(status, now)
		_ = s.operations.Flush()
		select {
		case wake <- struct{}{}:
		default:
		}
		if s.history != nil {
			s.history.Record(status, now)
		}
		if session := debuglog.Default.Session(); session != 0 {
			debuglog.Default.RecordSession(session, status, debuglog.SampleResources(s.store.DataDir()), now)
		}
	}
}
func (s *MappingService) History(id string) ([]history.Bucket, string) {
	if s.history == nil {
		return []history.Bucket{}, ""
	}
	return s.history.Snapshot(id)
}

func (s *MappingService) Operations() *operations.Store { return s.operations }
