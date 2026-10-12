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
			if h.State != "healthy" {
				h.RecoveredAt = now
			}
			h.State = "healthy"
			h.Error = ""
			h.OutageSince = time.Time{}
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

// Monitor is stopped and joined before Shutdown. Probes do not hold the service
// mutex; a replaced/stopped mapping cannot receive stale probe results.
func (s *MappingService) Monitor(ctx context.Context) {
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
		var wg sync.WaitGroup
		slots := make(chan struct{}, 4)
		for _, m := range s.store.ListMappings() {
			if m.Status != model.StatusRunning {
				continue
			}
			select {
			case slots <- struct{}{}:
			case <-ctx.Done():
				wg.Wait()
				return
			}
			wg.Add(1)
			go func(m model.TunnelMapping) { defer wg.Done(); defer func() { <-slots }(); s.checkMapping(ctx, m) }(m)
		}
		wg.Wait()
	}
}

func (s *MappingService) checkMapping(ctx context.Context, m model.TunnelMapping) {
	s.mu.Lock()
	current, ok := s.store.GetMapping(m.ID)
	if !ok || current != m || ctx.Err() != nil {
		s.mu.Unlock()
		return
	}
	h := s.health[m.ID]
	if h == nil {
		// Persisted running status alone must not override auto_restore=false.
		s.mu.Unlock()
		return
	}
	if time.Now().Before(h.next) {
		s.mu.Unlock()
		return
	}
	node, ok := s.store.GetNode(m.NodeID)
	if !ok {
		h.observe(fmt.Errorf("upstream node missing"), time.Now())
		s.mu.Unlock()
		return
	}
	if !s.singbox.IsRunning(m.TunName) || network.WaitInterface(m.TunName, 100*time.Millisecond) != nil {
		h.invalidateProtocols()
		h.State = "recovering"
		h.baseState = "recovering"
		h.successes = 0
		if h.OutageSince.IsZero() {
			h.OutageSince = time.Now()
		}
		err := s.singbox.Restart(node, m)
		if err == nil {
			err = network.WaitInterface(m.TunName, 15*time.Second)
		}
		if err == nil {
			err = s.net.Setup(m)
		}
		if err != nil {
			// Never tear down the guard while repairing an active PPP session.
			_ = s.singbox.Stop(m.TunName)
			h.recoveryFailures++
			h.observe(fmt.Errorf("mapping process or TUN recovery failed; inspect service logs"), time.Now())
			h.next = time.Now().Add(retryDelay(h.recoveryFailures))
			log.Printf("mapping %s recovery failed: %v", m.ID, err)
			s.mu.Unlock()
			return
		}
		h.recoveryFailures = 0
		h.failures = 0
	}
	s.mu.Unlock()
	report := speedtest.ProbeHealth(ctx, node)
	s.mu.Lock()
	defer s.mu.Unlock()
	current, ok = s.store.GetMapping(m.ID)
	latestNode, nodeOK := s.store.GetNode(m.NodeID)
	if !ok || current != m || !nodeOK || latestNode != node || s.health[m.ID] != h || ctx.Err() != nil {
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
}

func (h *healthRecord) invalidateProtocols() {
	h.tcp = protocolRecord{ProtocolHealth: model.ProtocolHealth{State: "checking"}}
	h.udp = protocolRecord{ProtocolHealth: model.ProtocolHealth{State: "checking"}}
	h.dns = protocolRecord{ProtocolHealth: model.ProtocolHealth{State: "checking"}}
	h.TCP = h.tcp.ProtocolHealth
	h.UDP = h.udp.ProtocolHealth
	h.DNS = h.dns.ProtocolHealth
}
