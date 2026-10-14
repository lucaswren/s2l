package service

import (
	"errors"
	"testing"
	"time"

	"github.com/lucaswren/s2l/internal/model"
	"github.com/lucaswren/s2l/internal/speedtest"
)

func TestHealthHysteresisAndRecovery(t *testing.T) {
	h := healthRecord{UpstreamHealth: model.UpstreamHealth{State: "checking"}}
	now := time.Now()
	h.observe(nil, now)
	if h.State == "healthy" {
		t.Fatal("one success must not establish health")
	}
	h.observe(nil, now.Add(time.Second))
	if h.State != "healthy" {
		t.Fatal(h.State)
	}
	for i := 1; i <= 3; i++ {
		h.observe(errors.New("offline"), now.Add(time.Duration(i+1)*time.Second))
		if i < 3 && h.State != "unstable" {
			t.Fatal(h.State)
		}
	}
	if h.State != "unavailable" || h.OutageSince.IsZero() {
		t.Fatal(h)
	}
	h.observe(nil, now.Add(8*time.Second))
	if h.State != "unavailable" {
		t.Fatal("premature recovery")
	}
	h.observe(nil, now.Add(9*time.Second))
	if h.State != "healthy" || !h.OutageSince.IsZero() || h.Error != "" {
		t.Fatal(h)
	}
	recovered := h.RecoveredAt
	h.observe(nil, now.Add(10*time.Second))
	if h.RecoveredAt != recovered {
		t.Fatal("recovery timestamp changed on healthy probe")
	}
}
func TestRetryDelayBounded(t *testing.T) {
	for i, want := range []time.Duration{5, 10, 20, 40, 60, 60} {
		if got := retryDelay(i + 1); got != want*time.Second {
			t.Fatalf("attempt %d: %v", i+1, got)
		}
	}
}

func TestSeparateProtocolsDoNotDisableHealthyTCP(t *testing.T) {
	h := healthRecord{UpstreamHealth: model.UpstreamHealth{State: "checking"}}
	report := speedtest.HealthReport{TCP: model.ProtocolHealth{State: "healthy"}, DNS: model.ProtocolHealth{State: "healthy", Method: "doh"}, UDP: model.ProtocolHealth{State: "unsupported", Error: "UDP unsupported"}}
	now := time.Now()
	for i := 0; i < 4; i++ {
		h.applyReport(report, now.Add(time.Duration(i)*time.Second))
	}
	if h.State != "degraded" || h.TCP.State != "healthy" || h.UDP.State != "unsupported" || h.DNS.Method != "doh" {
		t.Fatal(h)
	}
	recovered := h.RecoveredAt
	h.applyReport(report, now.Add(5*time.Second))
	if h.RecoveredAt != recovered {
		t.Fatal("partial capability updates changed TCP recovery time")
	}
	report.UDP = model.ProtocolHealth{State: "healthy"}
	h.applyReport(report, now.Add(6*time.Second))
	if h.UDP.State == "healthy" {
		t.Fatal("UDP recovery needs two successes")
	}
	h.applyReport(report, now.Add(7*time.Second))
	if h.State != "healthy" {
		t.Fatal(h)
	}
	report.DNS = model.ProtocolHealth{State: "unavailable", Error: "DNS failed"}
	for i := 0; i < 3; i++ {
		h.applyReport(report, now.Add(time.Duration(8+i)*time.Second))
	}
	if h.State != "degraded" || h.TCP.State != "healthy" || h.DNS.State != "unavailable" {
		t.Fatal(h)
	}
	h.invalidateProtocols()
	if h.TCP.State != "checking" || h.UDP.State != "checking" || h.DNS.State != "checking" {
		t.Fatal("recovery left stale protocol results")
	}
}

func TestHTTPSFailureWithWorkingDNSIsPartialAvailability(t *testing.T) {
	h := healthRecord{UpstreamHealth: model.UpstreamHealth{State: "checking"}}
	report := speedtest.HealthReport{TCP: model.ProtocolHealth{State: "unavailable"}, DNS: model.ProtocolHealth{State: "healthy"}, UDP: model.ProtocolHealth{State: "unsupported"}}
	for i := 0; i < 3; i++ {
		h.applyReport(report, time.Now())
	}
	if h.State != "degraded" || h.TCP.State != "unavailable" {
		t.Fatal(h)
	}
}

func TestDNSBackupDoesNotClaimPrimaryRecovery(t *testing.T) {
	p := protocolRecord{}
	fallback := model.ProtocolHealth{State: "degraded", Method: "tcp-fallback", Error: "primary DNS failed"}
	for i := 0; i < 3; i++ {
		p.observe(fallback, time.Now())
	}
	if p.State != "degraded" {
		t.Fatal(p)
	}
	p.observe(model.ProtocolHealth{State: "healthy", Method: "doh"}, time.Now())
	if p.State == "healthy" {
		t.Fatal("one primary success must not clear fallback warning")
	}
	p.observe(model.ProtocolHealth{State: "healthy", Method: "doh"}, time.Now())
	if p.State != "healthy" || p.Error != "" {
		t.Fatal(p)
	}
}

func TestUpstreamSuccessCannotHideLocalRecoveryFailure(t *testing.T) {
	outage := time.Now().Add(-time.Minute)
	h := healthRecord{UpstreamHealth: model.UpstreamHealth{LocalState: "unavailable", RecoveryError: "recovery failed", OutageSince: outage}}
	report := speedtest.HealthReport{TCP: model.ProtocolHealth{State: "healthy"}, UDP: model.ProtocolHealth{State: "healthy"}, DNS: model.ProtocolHealth{State: "healthy"}}
	for i := 0; i < 3; i++ {
		h.applyReport(report, time.Now())
	}
	if h.State != "unavailable" || h.Error != "recovery failed" {
		t.Fatal("upstream probes masked local failure", h)
	}
	if h.OutageSince != outage || !h.RecoveredAt.IsZero() {
		t.Fatal("healthy upstream cleared an ongoing local outage")
	}
	before := h.generation
	h.invalidateProtocols()
	if h.generation != before+1 {
		t.Fatal("stale probes were not invalidated")
	}
}
