package service

import (
	"context"
	"fmt"
	"log"
	"sync"
	"time"

	"github.com/lucaswren/s2l/internal/model"
	"github.com/lucaswren/s2l/internal/network"
	"github.com/lucaswren/s2l/internal/speedtest"
)

type healthRecord struct {
	model.UpstreamHealth
	failures, successes int
	next                time.Time
	localNext           time.Time
	generation          uint64
	recoveryFailures    int
	baseState           string
	tcp, udp, dns       protocolRecord
}

func (h *healthRecord) observe(err error, now time.Time) {
	if h.baseState != "" {
		h.State = h.baseState
	}
	h.CheckedAt = now
	if err != nil {
		h.successes = 0
		h.failures++
		h.Error = err.Error()
		if h.OutageSince.IsZero() {
			h.OutageSince = now
		}
		if h.failures >= 3 {
			h.State = "unavailable"
		} else {
			h.State = "unstable"
		}
		h.next = now.Add(retryDelay(h.failures))
	} else {
		h.failures = 0
		h.successes++
		if h.successes >= 2 {
			localHealthy := h.LocalState == "" || h.LocalState == "healthy"
			if h.State != "healthy" && localHealthy {
				h.RecoveredAt = now
			}
			h.State = "healthy"
			h.Error = ""
			if localHealthy {
				h.OutageSince = time.Time{}
			}
		}
		h.next = now.Add(10 * time.Second)
	}
	h.baseState = h.State
}

func retryDelay(n int) time.Duration {
	if n < 1 {
		n = 1
	}
	if n > 5 {
		n = 5
	}
	d := 5 * time.Second * time.Duration(1<<uint(n-1))
	if d > 60*time.Second {
		d = 60 * time.Second
	}
	return d
}

// Local liveness and upstream probes have independent worker pools and clocks.
func (s *MappingService) Monitor(ctx context.Context) {
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()
	var wg sync.WaitGroup
	defer wg.Wait()
	localSlots, probeSlots := make(chan struct{}, 4), make(chan struct{}, 4)
	localBusy := make(map[string]bool)
	launch := func(m model.TunnelMapping, local bool) {
		s.mu.Lock()
		busy := s.inflight
		if local {
			busy = localBusy
		}
		if busy[m.ID] || s.health[m.ID] == nil {
			s.mu.Unlock()
			return
		}
		busy[m.ID] = true
		s.mu.Unlock()
		wg.Add(1)
		go func() {
			defer wg.Done()
			defer func() { s.mu.Lock(); delete(busy, m.ID); s.mu.Unlock() }()
			slots := probeSlots
			if local {
				slots = localSlots
			}
			select {
			case slots <- struct{}{}:
			case <-ctx.Done():
				return
			}
			defer func() { <-slots }()
			if local {
				s.checkLocalMapping(ctx, m)
			} else {
				s.checkMapping(ctx, m)
			}
		}()
	}
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
		for _, m := range s.store.ListMappings() {
			if m.Status == model.StatusRunning {
				launch(m, true)
				launch(m, false)
			}
		}
	}
}

func (s *MappingService) checkLocalMapping(ctx context.Context, m model.TunnelMapping) {
	s.stateGate.RLock()
	defer s.stateGate.RUnlock()
	unlock := s.lockMapping(m.ID)
	defer unlock()
	current, ok := s.store.GetMapping(m.ID)
	if !ok || current != m || ctx.Err() != nil {
		return
	}
	s.mu.Lock()
	h := s.health[m.ID]
	if h == nil || s.shuttingDown {
		s.mu.Unlock()
		return
	}
	s.mu.Unlock()
	alive := s.singbox.IsRunning(m.TunName) && network.WaitInterface(m.TunName, 100*time.Millisecond) == nil
	now := time.Now()
	if alive {
		err := network.TunePeers(m)
		s.mu.Lock()
		h.LocalCheckedAt = now
		if err != nil {
			h.LocalState = "unstable"
			h.RecoveryError = "PPP interface tuning failed; inspect service logs"
			log.Printf("mapping %s PPP tune failed: %v", m.ID, err)
		} else {
			h.LocalState = "healthy"
			h.RecoveryError = ""
		}
		s.mu.Unlock()
		return
	}
	s.mu.Lock()
	h.LocalCheckedAt = now
	if now.Before(h.localNext) {
		s.mu.Unlock()
		return
	}
	h.LocalState = "recovering"
	h.State = "recovering"
	h.baseState = "recovering"
	h.successes = 0
	h.invalidateProtocols()
	if h.OutageSince.IsZero() {
		h.OutageSince = now
	}
	s.mu.Unlock()
	node, ok := s.store.GetNode(m.NodeID)
	var err error
	if !ok {
		err = fmt.Errorf("upstream node missing")
	} else {
		err = s.singbox.Restart(node, m)
		if err == nil {
			err = network.WaitInterface(m.TunName, 15*time.Second)
		}
		if err == nil {
			err = s.net.Setup(m)
		}
	}
	if err != nil {
		_ = s.singbox.Stop(m.TunName)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err != nil {
		h.recoveryFailures++
		h.localNext = time.Now().Add(retryDelay(h.recoveryFailures))
		h.LocalState = "unavailable"
		h.RecoveryError = "mapping recovery failed; inspect service logs"
		h.State = "unavailable"
		h.Error = h.RecoveryError
		log.Printf("mapping %s recovery failed: %v", m.ID, err)
	} else {
		h.recoveryFailures = 0
		h.localNext = time.Time{}
		h.LocalState = "healthy"
		h.RecoveryError = ""
		h.failures = 0
		h.successes = 0
		h.baseState = "checking"
		h.State = "checking"
		h.Error = ""
		h.next = time.Time{}
	}
}

func (s *MappingService) checkMapping(ctx context.Context, m model.TunnelMapping) {
	s.mu.Lock()
	current, ok := s.store.GetMapping(m.ID)
	h := s.health[m.ID]
	if !ok || current != m || ctx.Err() != nil || h == nil || s.shuttingDown || time.Now().Before(h.next) {
		s.mu.Unlock()
		return
	}
	node, ok := s.store.GetNode(m.NodeID)
	if !ok {
		h.observe(fmt.Errorf("upstream node missing"), time.Now())
		s.mu.Unlock()
		return
	}
	generation := h.generation
	s.mu.Unlock()
	report := speedtest.ProbeHealth(ctx, node, m.EffectiveDNSMode())
	s.mu.Lock()
	defer s.mu.Unlock()
	current, ok = s.store.GetMapping(m.ID)
	latestNode, nodeOK := s.store.GetNode(m.NodeID)
	if !ok || current != m || !nodeOK || latestNode != node || s.health[m.ID] != h || h.generation != generation || ctx.Err() != nil {
		return
	}
	old := h.State
	h.applyReport(report, time.Now())
	if old != h.State {
		log.Printf("mapping %s upstream health: %s", m.ID, h.State)
	}
}

type protocolRecord struct {
	model.ProtocolHealth
	failures, successes int
}

func (p *protocolRecord) observe(result model.ProtocolHealth, now time.Time) {
	p.CheckedAt = now
	p.Method = result.Method
	switch result.State {
	case "healthy":
		p.failures = 0
		p.successes++
		if p.successes >= 2 {
			p.State = "healthy"
			p.Error = ""
		} else if p.State == "" || p.State == "unsupported" {
			p.State = "checking"
			p.Error = ""
		}
	case "degraded":
		p.State = "degraded"
		p.Error = result.Error
		p.failures = 0
		p.successes = 0
	case "unsupported":
		p.State = "unsupported"
		p.Error = result.Error
		p.failures = 0
		p.successes = 0
	default:
		p.successes = 0
		p.failures++
		p.Error = result.Error
		if p.failures >= 3 {
			p.State = "unavailable"
		} else {
			p.State = "unstable"
		}
	}
}

func (h *healthRecord) applyReport(report speedtest.HealthReport, now time.Time) {
	var tcpError error
	if report.TCP.State != "healthy" {
		tcpError = fmt.Errorf("TCP HTTPS forwarding check failed")
	}
	h.observe(tcpError, now)
	h.tcp.observe(report.TCP, now)
	h.udp.observe(report.UDP, now)
	h.dns.observe(report.DNS, now)
	h.TCP = h.tcp.ProtocolHealth
	h.UDP = h.udp.ProtocolHealth
	h.DNS = h.dns.ProtocolHealth
	if h.State == "healthy" && (h.UDP.State != "healthy" || h.DNS.State != "healthy") {
		h.State = "degraded"
		h.Error = "TCP is available; inspect the separate UDP and DNS results"
	} else if h.State == "unavailable" && (h.UDP.State == "healthy" || (h.DNS.State == "healthy" || h.DNS.State == "degraded")) {
		h.State = "degraded"
		h.Error = "Some upstream checks succeed; inspect the TCP result"
		h.next = now.Add(10 * time.Second)
	}
	if h.LocalState != "" && h.LocalState != "healthy" {
		h.State = h.LocalState
		h.Error = h.RecoveryError
	}
}

func (h *healthRecord) invalidateProtocols() {
	h.generation++
	h.tcp = protocolRecord{ProtocolHealth: model.ProtocolHealth{State: "checking"}}
	h.udp = protocolRecord{ProtocolHealth: model.ProtocolHealth{State: "checking"}}
	h.dns = protocolRecord{ProtocolHealth: model.ProtocolHealth{State: "checking"}}
	h.TCP = h.tcp.ProtocolHealth
	h.UDP = h.udp.ProtocolHealth
	h.DNS = h.dns.ProtocolHealth
}
