package service

import (
	"context"
	"errors"
	"fmt"
	"math/rand"
	"sync"
	"time"

	"github.com/lucaswren/s2l/internal/debuglog"
	"github.com/lucaswren/s2l/internal/model"
	"github.com/lucaswren/s2l/internal/network"
	"github.com/lucaswren/s2l/internal/speedtest"
)

type healthRecord struct {
	model.UpstreamHealth
	failures, successes int
	next                time.Time
	localNext           time.Time
	forwardingNext      time.Time
	startupPending      bool
	generation          uint64
	recoveryFailures    int
	recovery            recoveryBudget
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
		h.next = now.Add(time.Duration(9500+rand.Intn(1001)) * time.Millisecond)
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
	wg.Add(1)
	go func() { defer wg.Done(); s.runTelemetry(ctx) }()
	wg.Add(1)
	go func() { defer wg.Done(); s.listenPPP(ctx) }()
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
		h := s.health[m.ID]
		if !local && time.Now().Before(h.next) {
			s.mu.Unlock()
			return
		}
		slots := probeSlots
		if local {
			slots = localSlots
		}
		select {
		case slots <- struct{}{}:
		default:
			s.mu.Unlock()
			return
		}
		busy[m.ID] = true
		s.mu.Unlock()
		wg.Add(1)
		go func() {
			defer wg.Done()
			defer func() { <-slots; s.mu.Lock(); delete(busy, m.ID); s.mu.Unlock() }()
			if ctx.Err() != nil {
				return
			}
			if local {
				s.checkLocalMapping(ctx, m)
			} else {
				s.checkMapping(ctx, m)
			}
		}()
	}
	cursor := 0
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
		mappings := s.store.ListMappings()
		for i := range mappings {
			m := mappings[(i+cursor)%len(mappings)]
			if m.Status == model.StatusRunning {
				launch(m, true)
				launch(m, false)
			}
		}
		if len(mappings) > 0 {
			cursor = (cursor + 4) % len(mappings)
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
	if h.startupPending {
		s.retryStartup(m, h)
		return
	}
	if alive {
		peers, _, peerErr := s.peerSnapshot()
		var err error
		if peerErr != "" {
			err = fmt.Errorf("PPP snapshot unavailable")
		} else {
			err = network.TunePeerSnapshot(m, peers)
		}
		verifyDue := !now.Before(h.forwardingNext)
		repairAttempted, repairSucceeded := false, false
		if verifyDue {
			ruleErr := s.net.VerifyContext(ctx, m)
			if ruleErr != nil && ctx.Err() == nil {
				repairAttempted = true
				debuglog.Default.Printf("mapping %s forwarding rules missing; repairing: %v", m.ID, ruleErr)
				ruleErr = s.net.Setup(m)
				if ruleErr == nil {
					ruleErr = s.net.VerifyContext(ctx, m)
					repairSucceeded = ruleErr == nil
				}
			}
			err = errors.Join(err, ruleErr)
		}
		s.mu.Lock()
		if repairAttempted {
			h.RepairAttempts++
		}
		if repairSucceeded {
			h.RepairSuccesses++
		}
		h.LocalCheckedAt = now
		if err != nil {
			h.recovery.healthySince = time.Time{}
			h.LocalState = "unstable"
			h.RecoveryError = "本地转发或 PPP 参数检查失败，请开启 Debug 查看详情"
			debuglog.Default.Printf("mapping %s PPP tune failed: %v", m.ID, err)
		} else {
			h.LocalState = "healthy"
			if verifyDue {
				h.forwardingNext = now.Add(30 * time.Second)
			}
			h.RecoveryError = ""
			if h.recovery.healthy(now) {
				h.recoveryFailures = 0
				h.localNext = time.Time{}
			}
		}
		s.mu.Unlock()
		if repairAttempted {
			detail := "规则修复失败"
			if repairSucceeded {
				detail = "规则已修复，保留原进程与出口"
			}
			s.operations.Audit("rule-repair", m.ID, detail)
		}
		return
	}
	s.mu.Lock()
	h.LocalCheckedAt = now
	h.recovery.healthySince = time.Time{}
	if now.Before(h.localNext) {
		s.mu.Unlock()
		return
	}
	if !h.recovery.allow(now) {
		h.LocalState = "circuit-open"
		h.State = "circuit-open"
		h.RecoveryError = "自动恢复已熔断，冷却 5 分钟后试探；也可手动重试"
		h.Error = h.RecoveryError
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
			if err == nil {
				err = s.net.Verify(m)
			}
		}
	}
	if err != nil {
		_ = s.singbox.Stop(m.TunName)
	}
	s.mu.Lock()
	h.finishRecovery(err, time.Now())
	s.mu.Unlock()
	detail := "进程恢复失败"
	if err == nil {
		detail = "进程已恢复，保留固定节点"
	}
	s.operations.Audit("process-recovery", m.ID, detail)
	if err != nil {
		debuglog.Default.Printf("mapping %s recovery failed: %v", m.ID, err)
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
	probeUDP := h.udpProbeDue(time.Now())
	s.mu.Unlock()
	report := speedtest.HealthReport{}
	var legs sync.WaitGroup
	legs.Add(2)
	go func() {
		defer legs.Done()
		report.TCP = s.probes.get(ctx, probeKey{node, "tcp", false}, func() speedtest.HealthReport { return speedtest.HealthReport{TCP: speedtest.ProbeTCP(ctx, node)} }).TCP
	}()
	go func() {
		defer legs.Done()
		report.DNS = s.probes.get(ctx, probeKey{node, "dns:" + m.EffectiveDNSMode(), false}, func() speedtest.HealthReport {
			return speedtest.HealthReport{DNS: speedtest.ProbeDNS(ctx, node, m.EffectiveDNSMode())}
		}).DNS
	}()
	if probeUDP {
		legs.Add(1)
		go func() {
			defer legs.Done()
			report.UDP = s.probes.get(ctx, probeKey{node, "udp", true}, func() speedtest.HealthReport { return speedtest.HealthReport{UDP: speedtest.ProbeUDP(ctx, node)} }).UDP
		}()
	}
	legs.Wait()
	s.finishProbe(ctx, m, node, h, generation, report)
}

func (s *MappingService) finishProbe(ctx context.Context, m model.TunnelMapping, node model.SocksNode, h *healthRecord, generation uint64, report speedtest.HealthReport) {
	s.mu.Lock()
	defer s.mu.Unlock()
	current, ok := s.store.GetMapping(m.ID)
	latestNode, nodeOK := s.store.GetNode(m.NodeID)
	if !ok || current != m || !nodeOK || latestNode != node || s.health[m.ID] != h || h.generation != generation || ctx.Err() != nil || s.shuttingDown {
		return
	}
	old := h.State
	h.UDPPolicy = m.EffectiveUDPPolicy()
	h.applyReport(report, time.Now())
	if old != h.State {
		debuglog.Default.Printf("mapping %s upstream health: %s", m.ID, h.State)
	}
}

type protocolRecord struct {
	model.ProtocolHealth
	failures, successes int
}

func (p *protocolRecord) observe(result model.ProtocolHealth, now time.Time) {
	if !result.CheckedAt.IsZero() {
		if !p.CheckedAt.IsZero() && !result.CheckedAt.After(p.CheckedAt) {
			return
		}
		p.CheckedAt = result.CheckedAt
	} else {
		p.CheckedAt = now
	}
	p.Method = result.Method
	p.MeasuredState = result.State
	p.Stage, p.StatusCode, p.DurationMS = result.Stage, result.StatusCode, result.DurationMS
	p.Attempts = append([]model.ProbeAttempt(nil), result.Attempts...)
	switch result.State {
	case "healthy":
		p.failures = 0
		p.successes++
		if p.successes >= 2 {
			p.State = "healthy"
			p.Error = ""
		} else if p.State != "healthy" {
			p.State = "checking"
			p.Error = ""
		}
	case "degraded", "restricted":
		p.State = result.State
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
	if report.TCP.State != "healthy" && report.TCP.State != "restricted" {
		tcpError = fmt.Errorf("TCP HTTPS forwarding check failed")
	}
	if report.TCP.CheckedAt.IsZero() || h.TCP.CheckedAt.IsZero() || report.TCP.CheckedAt.After(h.TCP.CheckedAt) {
		h.observe(tcpError, now)
		h.tcp.observe(report.TCP, now)
	} else if h.baseState != "" {
		h.State = h.baseState
		if h.State == "healthy" {
			h.Error = ""
		}
	}
	if report.UDP.State != "" {
		h.udp.observe(report.UDP, now)
	}
	if report.DNS.State != "" {
		h.dns.observe(report.DNS, now)
	}
	h.TCP = h.tcp.ProtocolHealth
	h.UDP = h.udp.ProtocolHealth
	h.DNS = h.dns.ProtocolHealth
	if h.State == "healthy" && ((h.UDPPolicy != "reject" && h.UDPPolicy != "" && h.UDP.State != "healthy") || h.DNS.State != "healthy" || h.TCP.State == "restricted") {
		h.State = "degraded"
		switch {
		case h.TCP.State == "restricted":
			h.Error = "代理已连通；检测目标限制访问或服务异常"
		case h.DNS.State != "healthy":
			h.Error = "TCP 正常；DNS 异常或检测中"
		case h.UDP.State == "unsupported":
			h.Error = "TCP 正常；UDP 不支持"
		default:
			h.Error = "TCP 正常；UDP 不可用或检测中"
		}
	} else if h.State == "unavailable" && (h.UDP.State == "healthy" || (h.DNS.State == "healthy" || h.DNS.State == "degraded")) {
		h.State = "degraded"
		h.Error = "Some upstream checks succeed; inspect the TCP result"
		h.next = now.Add(time.Duration(9500+rand.Intn(1001)) * time.Millisecond)
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

// Called with the service mutex after recovery I/O completes.
func (h *healthRecord) finishRecovery(err error, now time.Time) {
	// Probes may have started while Restart/Setup was in progress. Reject them
	// as well as probes from before recovery; only post-recovery results count.
	h.invalidateProtocols()
	if err != nil {
		h.recoveryFailures++
		h.localNext = now.Add(recoveryDelay(h.recoveryFailures))
		h.recovery.failed(now)
		h.LocalState = "unavailable"
		if !h.recovery.openUntil.IsZero() {
			h.LocalState = "circuit-open"
			h.localNext = h.recovery.openUntil
		}
		h.RecoveryError = "mapping recovery failed; enable Debug, reproduce and export logs"
		h.State = h.LocalState
		h.Error = h.RecoveryError
	} else {
		h.localNext = time.Time{}
		h.recovery.healthy(now)
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

// UDP capability changes slowly. Retain its real timestamp between probes;
// explicit node diagnostics always probe all protocols immediately.
func (h *healthRecord) udpProbeDue(now time.Time) bool {
	if h.UDP.CheckedAt.IsZero() {
		return true
	}
	interval := time.Minute
	switch h.UDP.State {
	case "unsupported", "unavailable":
		interval = 10 * time.Minute
	case "unstable":
		interval = time.Minute
	case "checking":
		interval = 10 * time.Second
	}
	return !now.Before(h.UDP.CheckedAt.Add(interval))
}
