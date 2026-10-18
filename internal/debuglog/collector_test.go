package debuglog

import (
	"bytes"
	"fmt"
	"github.com/lucaswren/s2l/internal/model"
	"sync"
	"testing"
	"time"
)

func TestCollectionLifecycle(t *testing.T) {
	c := &Collector{}
	c.Write([]byte("off"))
	if len(c.Snapshot()) != 0 {
		t.Fatal("collected while disabled")
	}
	c.Set(true)
	c.Write([]byte("on"))
	if !bytes.Contains(c.Snapshot(), []byte("on")) {
		t.Fatal("missing log")
	}
	c.Set(false)
	old := c.Snapshot()
	c.Write([]byte("ignored"))
	if !bytes.Equal(old, c.Snapshot()) {
		t.Fatal("collected after stop")
	}
	c.Set(true)
	if len(c.Snapshot()) != 0 {
		t.Fatal("new session retained old data")
	}
	c.Write(bytes.Repeat([]byte("x"), Limit+100))
	if len(c.Snapshot()) > Limit || c.State().Dropped == 0 {
		t.Fatal("unbounded buffer")
	}
	c.until = time.Now().Add(-time.Second)
	old = c.Snapshot()
	c.Write([]byte("expired"))
	if c.State().Enabled || !bytes.Equal(old, c.Snapshot()) {
		t.Fatal("expiry failed")
	}
}

func TestStaleSessionAndOutOfOrderSamplesAreRejected(t *testing.T) {
	c := &Collector{}
	first := c.Set(true).Session
	old := model.SystemStatus{UpstreamHealth: map[string]model.UpstreamHealth{"old": {State: "unavailable"}}}
	c.Set(false)
	second := c.Set(true).Session
	c.RecordSession(first, old, Resources{}, time.Now())
	if snapshot := c.Capture(); len(snapshot.Logs) != 0 || len(snapshot.Analysis.Events) != 0 {
		t.Fatal("old sampling work crossed session boundary")
	}
	now := time.Now()
	fresh := model.SystemStatus{UpstreamHealth: map[string]model.UpstreamHealth{"new": {State: "healthy"}}}
	c.RecordSession(second, fresh, Resources{}, now)
	c.RecordSession(second, old, Resources{}, now.Add(-time.Second))
	for _, e := range c.Analysis().Events {
		if e.MappingID == "old" {
			t.Fatal("out-of-order health rewound timeline")
		}
	}
	c.Set(false)
	before := c.Capture()
	c.RecordSession(second, old, Resources{}, now.Add(time.Second))
	c.Printf("off")
	c.Write([]byte("off"))
	after := c.Capture()
	if !bytes.Equal(before.Logs, after.Logs) || len(before.Analysis.Events) != len(after.Analysis.Events) || len(before.Analysis.Resources) != len(after.Analysis.Resources) {
		t.Fatal("disabled collection changed")
	}
}

func TestConcurrentSessionCaptureIsCoherent(t *testing.T) {
	c := &Collector{}
	var wg sync.WaitGroup
	for worker := 0; worker < 4; worker++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 100; i++ {
				session := c.Session()
				status := model.SystemStatus{UpstreamHealth: map[string]model.UpstreamHealth{fmt.Sprint(session): {State: "healthy"}}}
				c.RecordSession(session, status, Resources{}, time.Now())
				capture := c.Capture()
				if capture.State.Bytes != len(capture.Logs) {
					t.Error("capture mixed buffer and metadata")
				}
				for _, e := range capture.Analysis.Events {
					if e.MappingID != fmt.Sprint(capture.State.Session) {
						t.Error("capture mixed sessions")
					}
				}
			}
		}()
	}
	for i := 0; i < 100; i++ {
		c.Set(true)
		c.Set(false)
	}
	wg.Wait()
}

type delayedLogValue struct {
	started chan struct{}
	release chan struct{}
}

func (v delayedLogValue) String() string { close(v.started); <-v.release; return "old-session-log" }
func TestSlowFormattingCannotCrossDebugSessions(t *testing.T) {
	c := &Collector{}
	c.Set(true)
	value := delayedLogValue{make(chan struct{}), make(chan struct{})}
	done := make(chan struct{})
	go func() { defer close(done); c.Printf("%s", value) }()
	<-value.started
	c.Set(false)
	c.Set(true)
	close(value.release)
	<-done
	if len(c.Snapshot()) != 0 {
		t.Fatal("old formatted message entered new session")
	}
}
