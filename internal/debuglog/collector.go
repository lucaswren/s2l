// Package debuglog collects a bounded, temporary support session in memory.
package debuglog

import (
	"fmt"
	"sync"
	"time"
)

const Limit = 4 * 1024 * 1024

var Default = &Collector{}

type Collector struct {
	mu                  sync.Mutex
	until               time.Time
	started             time.Time
	stopped             time.Time
	stopReason          string
	data                []byte
	head                int
	dropped             int
	events              []Event
	droppedEvents       int
	samples             []Sample
	previous            map[string]observation
	generation          uint64
	lastSample          time.Time
	connections         map[string]Connection
	connectionFragments map[string]string
	droppedConnections  int
}
type State struct {
	Started         time.Time `json:"started_at"`
	Stopped         time.Time `json:"stopped_at"`
	StopReason      string    `json:"stop_reason,omitempty"`
	DurationSeconds int       `json:"duration_seconds"`
	Enabled         bool      `json:"enabled"`
	Expires         time.Time `json:"expires"`
	Bytes           int       `json:"bytes"`
	Dropped         int       `json:"dropped_bytes"`
	Session         uint64    `json:"session_id"`
}

func (c *Collector) active() bool { return time.Now().Before(c.until) }
func (c *Collector) Set(enabled bool) State {
	c.mu.Lock()
	defer c.mu.Unlock()
	if enabled {
		if !c.active() {
			c.generation++
			c.data = nil
			c.head = 0
			c.dropped = 0
			c.events = nil
			c.samples = nil
			c.previous = nil
			c.droppedEvents = 0
			c.lastSample = time.Time{}
			c.connections = nil
			c.connectionFragments = nil
			c.droppedConnections = 0
			c.started = time.Now()
			c.stopped = time.Time{}
			c.stopReason = ""
			c.until = c.started.Add(30 * time.Minute)
		}
	} else {
		if c.generation > 0 && c.stopped.IsZero() {
			c.stopped = time.Now()
			c.stopReason = "manual"
			if !c.active() {
				c.stopped = c.until
				c.stopReason = "expired"
			}
		}
		c.until = time.Time{}
	}
	return c.stateLocked()
}
func (c *Collector) State() State {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.stateLocked()
}
func (c *Collector) stateLocked() State {
	enabled := c.active()
	stopped, reason := c.stopped, c.stopReason
	if !enabled && stopped.IsZero() && c.generation > 0 {
		stopped = c.until
		reason = "expired"
	}
	return State{Started: c.started, Stopped: stopped, StopReason: reason, DurationSeconds: 1800, Enabled: enabled, Expires: c.until, Bytes: len(c.data), Dropped: c.dropped, Session: c.generation}
}

// Capture the session before beginning asynchronous sampling; stale producers
// must not inject pre-enable observations into a later debug session.
func (c *Collector) Session() uint64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.active() {
		return 0
	}
	return c.generation
}
func (c *Collector) Write(p []byte) (int, error) { c.Add("service", p); return len(p), nil }
func (c *Collector) Printf(format string, args ...any) {
	if session := c.Session(); session != 0 {
		p := []byte(fmt.Sprintf(format, args...))
		c.mu.Lock()
		defer c.mu.Unlock()
		if c.active() && session == c.generation {
			c.addLocked("service", p)
		}
	}
}
func (c *Collector) Add(source string, p []byte) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.active() {
		return
	}
	c.addLocked(source, p)
}
func (c *Collector) addLocked(source string, p []byte) {
	c.observeConnectionsLocked(source, p)
	b := append([]byte(fmt.Sprintf("%s [%s] ", time.Now().UTC().Format(time.RFC3339Nano), source)), p...)
	if len(b) == 0 || b[len(b)-1] != '\n' {
		b = append(b, '\n')
	}
	c.appendBytesLocked(b)
}

// Keep a circular buffer: a full collector only copies the incoming bytes,
// rather than shifting up to 4 MiB for every sing-box write.
func (c *Collector) appendBytesLocked(b []byte) {
	if len(b) > Limit {
		c.dropped += len(b) - Limit
		b = b[len(b)-Limit:]
	}
	if len(c.data) < Limit {
		n := min(len(b), Limit-len(c.data))
		c.data = append(c.data, b[:n]...)
		b = b[n:]
	}
	if len(b) == 0 {
		return
	}
	c.dropped += len(b)
	n := copy(c.data[c.head:], b)
	copy(c.data, b[n:])
	c.head = (c.head + len(b)) % Limit
}

func (c *Collector) snapshotLocked() []byte {
	if len(c.data) == 0 {
		return nil
	}
	b := make([]byte, len(c.data))
	n := copy(b, c.data[c.head:])
	copy(b[n:], c.data[:c.head])
	return b
}

func (c *Collector) Snapshot() []byte {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.snapshotLocked()
}

type Capture struct {
	State    State
	Logs     []byte
	Analysis Analysis
}

func (c *Collector) Capture() Capture {
	c.mu.Lock()
	defer c.mu.Unlock()
	return Capture{State: c.stateLocked(), Logs: c.snapshotLocked(), Analysis: c.analysisLocked()}
}
