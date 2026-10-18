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

func TestSessionStopMetadata(t *testing.T) {
	c := &Collector{}
	state := c.Set(true)
	if state.Started.IsZero() || state.DurationSeconds != 1800 || !state.Stopped.IsZero() {
		t.Fatal(state)
	}
	c.until = time.Now().Add(-time.Second)
	state = c.State()
	if state.Enabled || state.StopReason != "expired" || !state.Stopped.Equal(c.until) {
		t.Fatal(state)
	}
	c.Set(false)
	if c.State().StopReason != "expired" {
		t.Fatal(c.State())
	}
	c.Set(true)
	state = c.Set(false)
	if state.StopReason != "manual" || state.Stopped.IsZero() {
		t.Fatal(state)
	}
	c.Set(true)
	if c.State().StopReason != "" || !c.State().Stopped.IsZero() {
		t.Fatal(c.State())
	}
}

func TestCircularLogBufferOrderAndOverwriteCount(t *testing.T) {
	c := &Collector{}
	var expected []byte
	total := 0
	for i, n := range []int{0, 23, Limit / 2, Limit / 2, 7, Limit / 3, Limit, Limit + 41, 997} {
		p := bytes.Repeat([]byte{byte('a' + i)}, n)
		c.appendBytesLocked(p)
		expected = append(expected, p...)
		total += len(p)
		if len(expected) > Limit {
			expected = expected[len(expected)-Limit:]
		}
		if !bytes.Equal(c.Snapshot(), expected) {
			t.Fatalf("write %d reordered bytes", i)
		}
		if c.State().Dropped != total-len(expected) {
			t.Fatalf("write %d: dropped=%d", i, c.State().Dropped)
		}
	}
	snapshot := c.Snapshot()
	snapshot[0] ^= 1
	if bytes.Equal(snapshot, c.Snapshot()) {
		t.Fatal("snapshot aliases internal storage")
	}
	c.Set(true)
	if c.head != 0 || len(c.Snapshot()) != 0 {
		t.Fatal("new session retained circular buffer state")
	}
}
