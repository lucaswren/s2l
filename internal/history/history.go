// Package history keeps bounded operational metrics without raw destinations,
// credentials, or Debug logging. Samples are actual probes, not polling repeats.
package history

import (
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strconv"
	"sync"
	"time"

	"github.com/lucaswren/s2l/internal/model"
)

const bucketSeconds = int64(300)
const maxBuckets = 288

// Allow room for fallback details across a full day of samples for every mapping.
const maxHistoryBytes = 64 << 20

type Counts struct {
	Pass        int `json:"pass"`
	Fail        int `json:"fail"`
	Restricted  int `json:"restricted"`
	Unsupported int `json:"unsupported"`
	Fallback    int `json:"fallback"`
}
type DNSFallback struct {
	CheckedAt    time.Time `json:"checked_at"`
	PrimaryStage string    `json:"primary_stage"`
	SOCKSReply   byte      `json:"socks_reply,omitempty"`
	PrimaryMS    int64     `json:"primary_ms"`
	BackupMS     int64     `json:"backup_ms"`
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

	LastDNSFallback *DNSFallback `json:"last_dns_fallback,omitempty"`
}
type baseline struct {
	tcp, dns, udp time.Time
	pid           int
	sessions      []string
	sessionsKnown bool
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
	data, err := io.ReadAll(io.LimitReader(f, maxHistoryBytes+1))
	if err != nil || len(data) > maxHistoryBytes {
		s.loadNotice = "历史记录文件无效，已重新开始采样"
		return s
	}
	if json.Unmarshal(data, &s.rows) != nil || s.rows == nil || len(s.rows) > 252 {
		s.rows = map[string][]Bucket{}
		s.loadNotice = "历史记录文件无效，已重新开始采样"
		return s
	}
	now := time.Now()
	for id, rows := range s.rows {
		sort.Slice(rows, func(i, j int) bool { return rows[i].At < rows[j].At })
		s.rows[id] = trim(rows, now)
	}
	return s
}

// Rows stay sorted by time. Readers that need an independent copy make it
// under the store lock before returning.
func trim(rows []Bucket, now time.Time) []Bucket {
	unix := now.Unix()
	end := sort.Search(len(rows), func(i int) bool { return rows[i].At > unix })
	start := sort.Search(end, func(i int) bool { return rows[i].At > unix-24*3600 })
	if end-start > maxBuckets {
		start = end - maxBuckets
	}
	return rows[start:end]
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
	sessions := make(map[string][]string, len(status.Sessions))
	if status.SessionsError == "" {
		for _, peer := range status.Sessions {
			key := peer.Interface + "/" + strconv.Itoa(peer.Index) + "/" + peer.PeerIP
			sessions[peer.MappingID] = append(sessions[peer.MappingID], key)
		}
	}
	for _, m := range status.Mappings {
		active[m.ID] = true
		rows := trim(s.rows[m.ID], now)
		h, ok := status.UpstreamHealth[m.ID]
		if !ok || m.Status != model.StatusRunning {
			if _, exists := s.rows[m.ID]; exists {
				s.rows[m.ID] = rows
			}
			delete(s.previous, m.ID)
			continue
		}
		stamp := now.Unix() / bucketSeconds * bucketSeconds
		if len(rows) == 0 || rows[len(rows)-1].At != stamp {
			rows = append(rows, Bucket{At: stamp})
		}
		b := &rows[len(rows)-1]
		old := s.previous[m.ID]
		fresh := !h.TCP.CheckedAt.IsZero() && h.TCP.CheckedAt.After(old.tcp)
		count(&b.TCP, h.TCP, &old.tcp)
		fallbacks := b.DNS.Fallback
		count(&b.DNS, h.DNS, &old.dns)
		if b.DNS.Fallback > fallbacks && h.DNS.Method == "doh-fallback" && len(h.DNS.Attempts) == 2 {
			primary, backup := h.DNS.Attempts[0], h.DNS.Attempts[1]
			b.LastDNSFallback = &DNSFallback{
				CheckedAt:    h.DNS.CheckedAt,
				PrimaryStage: primary.Stage,
				SOCKSReply:   primary.SOCKSReply,
				PrimaryMS:    primary.DurationMS,
				BackupMS:     backup.DurationMS,
			}
		}
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
			keys := sessions[m.ID]
			sort.Strings(keys)
			if old.sessionsKnown && !slices.Equal(old.sessions, keys) {
				b.SessionChanges++
			}
			old.sessions = keys
			old.sessionsKnown = true
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
	rows := append([]Bucket{}, trim(s.rows[id], time.Now())...)
	for i := range rows {
		if fallback := rows[i].LastDNSFallback; fallback != nil {
			detail := *fallback
			rows[i].LastDNSFallback = &detail
		}
	}
	return rows, warning
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
