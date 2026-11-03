// Package operations stores bounded incidents and credential-free audit events.
package operations

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/lucaswren/s2l/internal/model"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

type Event struct {
	ID       int64     `json:"id"`
	At       time.Time `json:"at"`
	Kind     string    `json:"kind"`
	Resource string    `json:"resource"`
	Detail   string    `json:"detail"`
}
type Incident struct {
	Key      string    `json:"key"`
	Scope    string    `json:"scope"`
	State    string    `json:"state"`
	Affected []string  `json:"affected"`
	Started  time.Time `json:"started"`
	Updated  time.Time `json:"updated"`
	Resolved time.Time `json:"resolved,omitempty"`
}
type Settings struct {
	Revision         string    `json:"revision,omitempty"`
	Enabled          bool      `json:"enabled"`
	Webhook          string    `json:"webhook,omitempty"`
	Token            string    `json:"token,omitempty"`
	MaintenanceUntil time.Time `json:"maintenance_until"`
}
type disk struct {
	Events    []Event    `json:"events"`
	Incidents []Incident `json:"incidents"`
	Settings  Settings   `json:"settings"`
}
type View struct {
	Revision         string     `json:"revision"`
	Events           []Event    `json:"events"`
	Incidents        []Incident `json:"incidents"`
	Enabled          bool       `json:"enabled"`
	Configured       bool       `json:"configured"`
	MaintenanceUntil time.Time  `json:"maintenance_until"`
	Warning          string     `json:"warning,omitempty"`
	Delivery         string     `json:"delivery"`
}
type Store struct {
	generation       uint64
	sendCancel       context.CancelFunc
	retryBatch       []Event
	client           *http.Client
	deliveryAttempts int
	mu               sync.Mutex
	path             string
	data             disk
	loadNotice       string
	warning          string
	delivery         string
	dirty            bool
	next             int64
	pending          []Event
	lastFlush        time.Time
	lastDelivery     time.Time
}

func New(path string) *Store {
	s := &Store{path: path}
	f, err := os.Open(path)
	var b []byte
	if err == nil {
		b, err = io.ReadAll(io.LimitReader(f, 4<<20+1))
		f.Close()
	}
	if err == nil {
		if len(b) > 4<<20 || json.Unmarshal(b, &s.data) != nil {
			s.data = disk{}
			s.warning = "运行事件文件无效，已重新采集"
		}
	} else if !os.IsNotExist(err) {
		s.warning = "运行事件读取失败"
	}
	if len(s.data.Events) > 1000 {
		s.data.Events = s.data.Events[len(s.data.Events)-1000:]
	}
	s.trimIncidents()
	for _, e := range s.data.Events {
		if e.ID > s.next {
			s.next = e.ID
		}
	}
	s.loadNotice = s.warning
	s.warning = ""
	return s
}
func (s *Store) add(now time.Time, kind, resource, detail string) {
	s.next++
	e := Event{s.next, now, kind, resource, detail}
	s.data.Events = append(s.data.Events, e)
	if len(s.data.Events) > 1000 {
		s.data.Events = s.data.Events[len(s.data.Events)-1000:]
	}
	s.dirty = true
	if kind == "incident-open" || kind == "incident-recovered" {
		if s.data.Settings.Enabled && now.After(s.data.Settings.MaintenanceUntil) {
			s.pending = append(s.pending, e)
			if len(s.pending) > 64 {
				s.pending = s.pending[len(s.pending)-64:]
			}
		}
	}
}

// Audit accepts only caller-generated category names, never request bodies.
func (s *Store) Audit(kind, resource, detail string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.add(time.Now(), kind, resource, detail)
}
func (s *Store) Observe(status model.SystemStatus, now time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	desired := map[string]Incident{}
	mappingByID := map[string]model.TunnelMapping{}
	for _, m := range status.Mappings {
		mappingByID[m.ID] = m
	}
	groups := map[string][]string{}
	for _, m := range status.Mappings {
		if m.Status != model.StatusRunning {
			continue
		}
		h, ok := status.UpstreamHealth[m.ID]
		if !ok {
			h = model.UpstreamHealth{}
		}
		state := ""
		scope := "mapping"
		if status.SessionsError == "" {
			count := 0
			for _, p := range status.Sessions {
				if p.MappingID == m.ID {
					count++
				}
			}
			if count > 1 {
				key := m.ID + ":duplicate-session"
				desired[key] = Incident{Key: key, Scope: "mapping", State: "duplicate-session", Affected: []string{m.ID}}
			}
		}
		if h.LocalState != "" && h.LocalState != "healthy" {
			state = "local-" + h.LocalState
		} else if h.TCP.CheckedAt.IsZero() || now.Sub(h.TCP.CheckedAt) > 3*time.Minute {
			if h.TCP.CheckedAt.IsZero() && status.Uptime < time.Minute {
				continue
			}
			state = "unknown"
		} else if h.TCP.State == "unavailable" || h.TCP.State == "unstable" {
			state = "upstream"
			scope = "node"
		} else if h.DNS.State == "unavailable" || h.DNS.State == "unstable" {
			state = "dns"
		}
		if state == "" {
			continue
		}
		key := m.ID + ":" + state
		if scope == "node" {
			key = "node:" + m.NodeID
			groups[key] = append(groups[key], m.ID)
		}
		desired[key] = Incident{Key: key, Scope: scope, State: state, Affected: []string{m.ID}}
	}
	for k, ids := range groups {
		sort.Strings(ids)
		v := desired[k]
		v.Affected = ids
		desired[k] = v
	}
	if len(groups) >= 2 {
		ids := []string{}
		for k, rows := range groups {
			ids = append(ids, rows...)
			delete(desired, k)
		}
		sort.Strings(ids)
		desired["shared:upstream"] = Incident{Key: "shared:upstream", Scope: "server-suspected", State: "shared-upstream", Affected: ids}
	}
	for i := range s.data.Incidents {
		v := &s.data.Incidents[i]
		if !v.Resolved.IsZero() {
			continue
		}
		unknown := []string{}
		active := false
		changed := false
		for _, id := range v.Affected {
			m, exists := mappingByID[id]
			if !exists || m.Status != model.StatusRunning {
				continue
			}
			active = true
			h, has := status.UpstreamHealth[id]
			stale := !has
			switch v.State {
			case "duplicate-session":
				stale = status.SessionsError != ""
			case "dns":
				stale = stale || !fresh(h.DNS.CheckedAt, now) || !decided(h.DNS.State)
			case "unknown":
				stale = stale || !fresh(h.TCP.CheckedAt, now) || !decided(h.TCP.State)
			default:
				if strings.HasPrefix(v.State, "local-") {
					stale = stale || !fresh(h.LocalCheckedAt, now)
				} else {
					stale = stale || !fresh(h.TCP.CheckedAt, now) || !decided(h.TCP.State)
				}
			}
			if stale {
				unknown = append(unknown, id)
			}
			if h.LocalState != "" && h.LocalState != "healthy" || h.TCP.State == "unavailable" || h.TCP.State == "unstable" || h.DNS.State == "unavailable" || h.DNS.State == "unstable" {
				changed = true
			}
		}
		if len(unknown) > 0 {
			if d, exists := desired[v.Key]; exists {
				for _, id := range unknown {
					found := false
					for _, known := range d.Affected {
						if known == id {
							found = true
						}
					}
					if !found {
						d.Affected = append(d.Affected, id)
					}
				}
				sort.Strings(d.Affected)
				desired[v.Key] = d
			} else {
				if !same(v.Affected, unknown) {
					v.Affected = unknown
					s.dirty = true
				}
				continue
			}
		}
		if d, ok := desired[v.Key]; ok {
			if !same(v.Affected, d.Affected) {
				v.Affected = d.Affected
				s.dirty = true
			}
			v.Updated = now
			delete(desired, v.Key)
		} else {
			v.Resolved = now
			v.Updated = now
			kind, detail := "incident-recovered", "故障已恢复"
			if !active {
				kind, detail = "incident-ended", "映射已停止或删除"
			} else if changed {
				kind, detail = "incident-changed", "故障分类发生变化，仍需检查"
			}
			s.add(now, kind, v.Key, detail)
		}
	}
	keys := make([]string, 0, len(desired))
	for k := range desired {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		v := desired[k]
		v.Started = now
		v.Updated = now
		s.data.Incidents = append(s.data.Incidents, v)
		s.add(now, "incident-open", v.Key, v.State)
	}
	s.trimIncidents()
	if now.Sub(s.lastFlush) >= time.Minute {
		s.dirty = true
	}
}
func same(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
func (s *Store) View() View {
	s.mu.Lock()
	defer s.mu.Unlock()
	warning := s.warning
	if warning == "" {
		warning = s.loadNotice
	}
	v := View{Revision: settingsRevision(s.data.Settings), Events: append([]Event{}, s.data.Events...), Incidents: []Incident{}, Enabled: s.data.Settings.Enabled, Configured: s.data.Settings.Webhook != "", MaintenanceUntil: s.data.Settings.MaintenanceUntil, Warning: warning, Delivery: s.delivery}
	for _, i := range s.data.Incidents {
		i.Affected = append([]string{}, i.Affected...)
		v.Incidents = append(v.Incidents, i)
	}
	return v
}

var ErrConflict = errors.New("通知配置已被其他页面修改，请重新加载后编辑")

func settingsRevision(c Settings) string {
	c.Revision = ""
	b, _ := json.Marshal(c)
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}
func (s *Store) Configure(c Settings) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if c.Revision != "" && c.Revision != settingsRevision(s.data.Settings) {
		return ErrConflict
	}
	c.Revision = ""
	if c.Webhook == "" {
		c.Webhook = s.data.Settings.Webhook
	}
	if c.Token == "" && c.Webhook == s.data.Settings.Webhook {
		c.Token = s.data.Settings.Token
	}
	if c.Webhook != "" {
		u, e := url.Parse(c.Webhook)
		if e != nil || u.Scheme != "https" || u.Hostname() == "" || u.User != nil || u.Fragment != "" || len(c.Webhook) > 2048 {
			return fmt.Errorf("通知地址必须为 HTTPS URL，不能带账号或片段")
		}
	}
	if len(c.Token) > 1024 || strings.ContainsAny(c.Token, "\r\n") || c.MaintenanceUntil.After(time.Now().Add(7*24*time.Hour)) {
		return fmt.Errorf("通知参数超出限制")
	}
	if c.Enabled && c.Webhook == "" {
		return fmt.Errorf("请先配置通知地址")
	}
	old := s.data.Settings
	s.data.Settings = c
	s.dirty = true
	if err := s.flushLocked(); err != nil {
		s.data.Settings = old
		return err
	}
	s.generation++
	if s.sendCancel != nil {
		s.sendCancel()
		s.sendCancel = nil
	}
	s.deliveryAttempts = 0
	s.retryBatch = nil
	s.lastDelivery = time.Time{}
	// Pending events were approved for the previous destination/settings only.
	s.pending = nil
	return nil
}
func (s *Store) Flush() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.dirty {
		return nil
	}
	return s.flushLocked()
}
func (s *Store) flushLocked() (err error) {
	defer func() {
		if err != nil {
			s.warning = "运行事件保存失败"
		} else {
			s.warning = ""
		}
	}()
	b, e := json.Marshal(s.data)
	if e != nil {
		return e
	}
	if e = os.MkdirAll(filepath.Dir(s.path), 0700); e != nil {
		return e
	}
	f, e := os.CreateTemp(filepath.Dir(s.path), ".operations-*")
	if e != nil {
		return e
	}
	defer os.Remove(f.Name())
	_ = f.Chmod(0600)
	_, e = f.Write(b)
	if e == nil {
		e = f.Sync()
	}
	ce := f.Close()
	if e == nil {
		e = ce
	}
	if e == nil {
		e = os.Rename(f.Name(), s.path)
	}
	if e != nil {
		s.warning = "运行事件保存失败"
		return e
	}
	s.dirty = false
	s.lastFlush = time.Now()
	return nil
}

// Deliver runs outside Store locks, with a short timeout and no redirect or
// environment proxy. Failure is visible and retried with a bounded cooldown.
func (s *Store) Deliver(ctx context.Context) {
	s.mu.Lock()
	now := time.Now()
	c := s.data.Settings
	if !c.Enabled || now.Before(c.MaintenanceUntil) {
		s.pending = nil
		s.retryBatch = nil
		s.mu.Unlock()
		return
	}
	if s.sendCancel != nil || (len(s.pending) == 0 && len(s.retryBatch) == 0) || now.Sub(s.lastDelivery) < time.Minute {
		s.mu.Unlock()
		return
	}
	if len(s.retryBatch) == 0 {
		s.retryBatch = append([]Event{}, s.pending...)
		s.pending = nil
		s.deliveryAttempts = 0
	}
	batch := append([]Event{}, s.retryBatch...)
	generation := s.generation
	s.lastDelivery = now
	ctx, cancel := context.WithTimeout(ctx, 4*time.Second)
	s.sendCancel = cancel
	s.mu.Unlock()
	defer cancel()
	payload, _ := json.Marshal(map[string]any{"source": "s2l", "events": batch})
	req, e := http.NewRequestWithContext(ctx, "POST", c.Webhook, bytes.NewReader(payload))
	if e == nil {
		req.Header.Set("Content-Type", "application/json")
		if c.Token != "" {
			req.Header.Set("Authorization", "Bearer "+c.Token)
		}
	}
	tr := &http.Transport{Proxy: nil, MaxResponseHeaderBytes: 8192}
	defer tr.CloseIdleConnections()
	client := &http.Client{Transport: tr, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	if s.client != nil {
		client = s.client
	}
	ok := false
	if e == nil {
		res, err := client.Do(req)
		if err == nil {
			_, _ = io.Copy(io.Discard, io.LimitReader(res.Body, 4096))
			res.Body.Close()
			ok = res.StatusCode >= 200 && res.StatusCode < 300
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if generation != s.generation {
		return
	}
	s.sendCancel = nil
	if ctx.Err() != nil && ctx.Err() == context.Canceled {
		return
	}
	if ok {
		s.retryBatch = nil
		s.delivery = "已发送"
		s.deliveryAttempts = 0
	} else {
		s.deliveryAttempts++
		s.delivery = "发送失败，将在冷却后重试"
		if s.deliveryAttempts >= 5 {
			s.retryBatch = nil
			s.deliveryAttempts = 0
			s.delivery = "连续发送失败，本批已停止重试；事件仍保留在本地"
		}
	}
}

func fresh(at, now time.Time) bool {
	return !at.IsZero() && now.Sub(at) <= 3*time.Minute && at.Sub(now) < time.Minute
}
func (s *Store) trimIncidents() {
	if len(s.data.Incidents) <= 500 {
		return
	}
	sort.SliceStable(s.data.Incidents, func(i, j int) bool {
		a, b := s.data.Incidents[i], s.data.Incidents[j]
		if a.Resolved.IsZero() != b.Resolved.IsZero() {
			return a.Resolved.IsZero()
		}
		return a.Updated.After(b.Updated)
	})
	s.data.Incidents = s.data.Incidents[:500]
}

func decided(state string) bool {
	switch state {
	case "healthy", "degraded", "restricted", "unavailable", "unstable", "unsupported":
		return true
	}
	return false
}
