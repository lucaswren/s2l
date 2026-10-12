package service

import (
	"errors"
	"github.com/lucaswren/s2l/internal/model"
	"testing"
	"time"
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
