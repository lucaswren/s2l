// Package history keeps bounded operational metrics without raw destinations,
// credentials, or Debug logging. Samples are actual probes, not polling repeats.
package history

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"

	"github.com/lucaswren/s2l/internal/model"
)

const bucketSeconds = int64(300)
const maxBuckets = 288

type Counts struct {
	Pass        int `json:"pass"`
	Fail        int `json:"fail"`
	Restricted  int `json:"restricted"`
	Unsupported int `json:"unsupported"`
	Fallback    int `json:"fallback"`
}
type Bucket struct {
	At             int64  `json:"at"`
	TCP            Counts `json:"tcp"`
	DNS            Counts `json:"dns"`
	UDP            Counts `json:"udp"`
	LatencyTotalMS int64  `json:"latency_total_ms"`
	LatencySamples int    `json:"latency_samples"`
	Repairs        int    `json:"repairs"`
	Restarts       int    `json:"restarts"`
	SessionChanges int    `json:"session_changes"`
	ExitChanges    int    `json:"exit_changes"`
	ForwardingPass int    `json:"forwarding_pass"`
	ForwardingFail int    `json:"forwarding_fail"`
	RxPeakBPS      uint64 `json:"rx_peak_bps"`
	TxPeakBPS      uint64 `json:"tx_peak_bps"`
}
type baseline struct {
	tcp, dns, udp time.Time
	pid           int
	local         string
	sessions      string
	traffic       model.TunTraffic
	at            time.Time
	exitIP        string
	repairs       uint64
	exits         uint64
}

func (s *Store) RecordDiagnostic(id string, result model.MappingDiagnostic) {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now()
	rows := trim(s.rows[id], now)
	stamp := now.Unix() / bucketSeconds * bucketSeconds
	if len(rows) == 0 || rows[len(rows)-1].At != stamp {
		rows = append(rows, Bucket{At: stamp})
	}
	b := &rows[len(rows)-1]
	if result.Forwarding.State == "healthy" {
		b.ForwardingPass++
	} else if result.Forwarding.State == "unavailable" {
		b.ForwardingFail++
	}
	previous := s.previous[id]
	if ip := result.Forwarding.ExitIP; ip != "" {
		if previous.exitIP != "" && previous.exitIP != ip {
			b.ExitChanges++
		}
		previous.exitIP = ip
	}
	s.previous[id] = previous
	if len(rows) > maxBuckets {
		rows = rows[len(rows)-maxBuckets:]
	}
	s.rows[id] = rows
}

type Store struct {
	mu         sync.Mutex
	rows       map[string][]Bucket
	previous   map[string]baseline
	path       string
	lastFlush  time.Time
	warning    string
	loadNotice string
}

func New(path string) *Store {
	s := &Store{rows: map[string][]Bucket{}, previous: map[string]baseline{}, path: path}
	if path == "" {
		return s
	}
	f, err := os.Open(path)
	if os.IsNotExist(err) {
		return s
	}
	if err != nil {
		s.loadNotice = "历史记录读取失败"
		return s
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, 32<<20+1))
	if err != nil || len(data) > 32<<20 {
		s.loadNotice = "历史记录文件无效，已重新开始采样"
		return s
	}
	if json.Unmarshal(data, &s.rows) != nil || s.rows == nil || len(s.rows) > 252 {
		s.rows = map[string][]Bucket{}
		s.loadNotice = "历史记录文件无效，已重新开始采样"
		return s
	}
	for id, rows := range s.rows {
		if len(rows) > maxBuckets {
			rows = rows[len(rows)-maxBuckets:]
		}
		s.rows[id] = trim(rows, time.Now())
	}
	return s
}
func trim(rows []Bucket, now time.Time) []Bucket {
	cutoff := now.Unix() - 24*3600
	out := make([]Bucket, 0, len(rows))
	for _, b := range rows {
		if b.At > cutoff && b.At <= now.Unix() {
			out = append(out, b)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].At < out[j].At })
	if len(out) > maxBuckets {
		out = out[len(out)-maxBuckets:]
	}
	return out
}
func count(c *Counts, p model.ProtocolHealth, last *time.Time) {
	if p.CheckedAt.IsZero() || !p.CheckedAt.After(*last) {
		return
	}
	*last = p.CheckedAt
	state := p.MeasuredState
	if state == "" {
		state = p.State
	}
	switch state {
	case "healthy":
		c.Pass++
	case "degraded":
		c.Pass++
		c.Fallback++
	case "restricted":
		c.Restricted++
	case "unsupported":
		c.Unsupported++
	case "unavailable", "unstable":
		c.Fail++
	}
}
func (s *Store) Record(status model.SystemStatus, now time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	active := map[string]bool{}
	traffic := map[string]model.TunTraffic{}
	for _, t := range status.TunTraffic {
		traffic[t.TunName] = t
	}
	for _, m := range status.Mappings {
		active[m.ID] = true
		// Retention also applies to stopped mappings, not only new samples.
		if rows, exists := s.rows[m.ID]; exists {
			s.rows[m.ID] = trim(rows, now)
		}
		h, ok := status.UpstreamHealth[m.ID]
		if !ok || m.Status != model.StatusRunning {
			delete(s.previous, m.ID)
			continue
		}
		rows := trim(s.rows[m.ID], now)
		stamp := now.Unix() / bucketSeconds * bucketSeconds
		if len(rows) == 0 || rows[len(rows)-1].At != stamp {
			rows = append(rows, Bucket{At: stamp})
		}
		b := &rows[len(rows)-1]
		old := s.previous[m.ID]
		fresh := !h.TCP.CheckedAt.IsZero() && h.TCP.CheckedAt.After(old.tcp)
		count(&b.TCP, h.TCP, &old.tcp)
		count(&b.DNS, h.DNS, &old.dns)
		count(&b.UDP, h.UDP, &old.udp)
		if fresh && (h.TCP.MeasuredState == "healthy" || (h.TCP.MeasuredState == "" && h.TCP.State == "healthy")) {
			b.LatencyTotalMS += h.TCP.DurationMS
			b.LatencySamples++
		}
		pid := status.SingboxPIDs[m.TunName]
		if !old.at.IsZero() {
			if h.Process.Exits > old.exits {
				b.Restarts += int(h.Process.Exits - old.exits)
			} else if old.pid != 0 && pid != 0 && pid != old.pid {
				b.Restarts++
			}
			if h.RepairSuccesses > old.repairs {
				b.Repairs += int(h.RepairSuccesses - old.repairs)
			}
		}
		if status.SessionsError == "" {
			keys := []string{}
			for _, p := range status.Sessions {
				if p.MappingID == m.ID {
					keys = append(keys, fmt.Sprintf("%s/%d/%s", p.Interface, p.Index, p.PeerIP))
				}
			}
			sort.Strings(keys)
			encoded, _ := json.Marshal(keys)
			current := string(encoded)
			if old.sessions != "" && current != old.sessions {
				b.SessionChanges++
			}
			old.sessions = current
		}
		t := traffic[m.TunName]
		elapsed := now.Sub(old.at).Seconds()
		if !old.at.IsZero() && elapsed > 0 && elapsed <= 30 && t.Error == "" && old.traffic.Error == "" && old.pid == pid && pid != 0 {
			if t.RxBytes >= old.traffic.RxBytes {
				v := uint64(float64(t.RxBytes-old.traffic.RxBytes) / elapsed)
				if v > b.RxPeakBPS {
					b.RxPeakBPS = v
				}
			}
			if t.TxBytes >= old.traffic.TxBytes {
				v := uint64(float64(t.TxBytes-old.traffic.TxBytes) / elapsed)
				if v > b.TxPeakBPS {
					b.TxPeakBPS = v
				}
			}
		}
		old.pid = pid
		old.local = h.LocalState
		old.repairs = h.RepairSuccesses
		old.exits = h.Process.Exits
		old.traffic = t
		old.at = now
		s.previous[m.ID] = old
		if len(rows) > maxBuckets {
			rows = rows[len(rows)-maxBuckets:]
		}
		s.rows[m.ID] = rows
	}
	for id := range s.rows {
		if !active[id] {
			delete(s.rows, id)
			delete(s.previous, id)
		}
	}
	if now.Sub(s.lastFlush) >= time.Minute {
		s.flushLocked(now)
	}
}
func (s *Store) Snapshot(id string) ([]Bucket, string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	warning := s.warning
	if warning == "" {
		warning = s.loadNotice
	}
	return append([]Bucket{}, trim(s.rows[id], time.Now())...), warning
}
func (s *Store) Flush() { s.mu.Lock(); defer s.mu.Unlock(); s.flushLocked(time.Now()) }
func (s *Store) flushLocked(now time.Time) {
	s.lastFlush = now
	if s.path == "" {
		return
	}
	data, err := json.Marshal(s.rows)
	if err == nil {
		err = os.MkdirAll(filepath.Dir(s.path), 0700)
	}
	if err == nil {
		err = os.WriteFile(s.path+".tmp", data, 0600)
	}
	if err == nil {
		err = os.Rename(s.path+".tmp", s.path)
	}
	if err != nil {
		s.warning = "历史记录保存失败"
	} else {
		s.warning = ""
	}
}
