package service

import (
	"context"
	"github.com/lucaswren/s2l/internal/model"
	"github.com/lucaswren/s2l/internal/speedtest"
	"sync"
	"time"
)

type probeKey struct {
	Node model.SocksNode
	Mode string
	UDP  bool
	DNS  model.DNSOptions
}
type probeEntry struct {
	done     chan struct{}
	report   speedtest.HealthReport
	at       time.Time
	complete bool
	valid    bool
}
type nodeProbeCache struct {
	mu      sync.Mutex
	entries map[probeKey]*probeEntry
}

func (c *nodeProbeCache) get(ctx context.Context, key probeKey, run func() speedtest.HealthReport) speedtest.HealthReport {
	c.mu.Lock()
	if c.entries == nil {
		c.entries = map[probeKey]*probeEntry{}
	}
	for k, e := range c.entries {
		if e.complete && time.Since(e.at) > 5*time.Second {
			delete(c.entries, k)
		}
	}
	if e := c.entries[key]; e != nil {
		c.mu.Unlock()
		select {
		case <-ctx.Done():
			return speedtest.HealthReport{}
		case <-e.done:
			if !e.valid && ctx.Err() == nil {
				return c.get(ctx, key, run)
			}
			return e.report
		}
	}
	e := &probeEntry{done: make(chan struct{})}
	c.entries[key] = e
	c.mu.Unlock()
	report := run()
	c.mu.Lock()
	e.report = report
	e.at = time.Now()
	e.complete = true
	e.valid = ctx.Err() == nil
	if ctx.Err() != nil {
		delete(c.entries, key)
	}
	close(e.done)
	c.mu.Unlock()
	return report
}

// Manual probes bypass the cache and update only records that still describe
// the same mapping, node credentials and process generation as before the probe.
func (s *MappingService) ProbeNode(ctx context.Context, node model.SocksNode) speedtest.HealthReport {
	type target struct {
		m          model.TunnelMapping
		h          *healthRecord
		generation uint64
	}
	targets := []target{}
	s.mu.Lock()
	for _, m := range s.store.ListMappings() {
		if m.NodeID == node.ID {
			if h := s.health[m.ID]; h != nil {
				targets = append(targets, target{m, h, h.generation})
			}
		}
	}
	s.mu.Unlock()
	report := speedtest.ProbeHealth(ctx, node)
	for _, t := range targets {
		measured := nodeReportForMapping(report, t.m)
		s.finishProbe(ctx, t.m, node, t.h, t.generation, measured)
	}
	return report
}

// A node-wide probe uses default DNS. Never overwrite a mapping's custom DNS
// health with results for a different resolver or transport policy.
func nodeReportForMapping(report speedtest.HealthReport, m model.TunnelMapping) speedtest.HealthReport {
	if m.EffectiveDNSOptions() != (model.DNSOptions{}).WithDefaults() {
		report.DNS = model.ProtocolHealth{}
	}
	return report
}
