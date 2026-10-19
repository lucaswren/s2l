package service

import (
	"context"
	"github.com/lucaswren/s2l/internal/model"
	"github.com/lucaswren/s2l/internal/speedtest"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestNodeProbesShareConcurrentWorkButSeparateCredentialsAndMode(t *testing.T) {
	var cache nodeProbeCache
	var calls atomic.Int32
	ready := make(chan struct{})
	var wg sync.WaitGroup
	key := probeKey{Node: model.SocksNode{ID: "n", Password: "one"}, Mode: "auto", UDP: true}
	run := func() speedtest.HealthReport {
		calls.Add(1)
		<-ready
		return speedtest.HealthReport{TCP: model.ProtocolHealth{State: "healthy"}}
	}
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			r := cache.get(context.Background(), key, run)
			if r.TCP.State != "healthy" {
				t.Error(r)
			}
		}()
	}
	time.Sleep(20 * time.Millisecond)
	close(ready)
	wg.Wait()
	if calls.Load() != 1 {
		t.Fatal("duplicate node probes", calls.Load())
	}
	key.Mode = "tcp"
	cache.get(context.Background(), key, run)
	key.Node.Password = "two"
	cache.get(context.Background(), key, run)
	if calls.Load() != 3 {
		t.Fatal("shared incompatible probe results")
	}
}
