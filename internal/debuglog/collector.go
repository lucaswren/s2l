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
	mu            sync.Mutex
	until         time.Time
	data          []byte
	dropped       int
	events        []Event
	droppedEvents int
	samples       []Sample
	previous      map[string]observation
	generation    uint64
	lastSample    time.Time
}
type State struct {
	Enabled bool      `json:"enabled"`
	Expires time.Time `json:"expires"`
	Bytes   int       `json:"bytes"`
	Dropped int       `json:"dropped_bytes"`
	Session uint64    `json:"session_id"`
}

func (c *Collector) active() bool { return time.Now().Before(c.until) }
func (c *Collector) Set(enabled bool) State {
	c.mu.Lock()
	defer c.mu.Unlock()
	if enabled {
		if !c.active() {
			c.generation++
			c.data = nil
			c.dropped = 0
			c.events = nil
			c.samples = nil
			c.previous = nil
			c.droppedEvents = 0
			c.lastSample = time.Time{}
			c.until = time.Now().Add(30 * time.Minute)
		}
	} else {
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
	return State{Enabled: c.active(), Expires: c.until, Bytes: len(c.data), Dropped: c.dropped, Session: c.generation}
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
	b := append([]byte(fmt.Sprintf("%s [%s] ", time.Now().UTC().Format(time.RFC3339Nano), source)), p...)
	if len(b) == 0 || b[len(b)-1] != '\n' {
		b = append(b, '\n')
	}
	if len(b) > Limit {
		c.dropped += len(b) - Limit
		b = b[len(b)-Limit:]
	}
	if n := len(c.data) + len(b) - Limit; n > 0 {
		c.dropped += n
		copy(c.data, c.data[n:])
		c.data = c.data[:len(c.data)-n]
	}
	c.data = append(c.data, b...)
}
func (c *Collector) Snapshot() []byte {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]byte(nil), c.data...)
}

type Capture struct {
	State    State
	Logs     []byte
	Analysis Analysis
}

func (c *Collector) Capture() Capture {
	c.mu.Lock()
	defer c.mu.Unlock()
	return Capture{State: c.stateLocked(), Logs: append([]byte(nil), c.data...), Analysis: c.analysisLocked()}
}
