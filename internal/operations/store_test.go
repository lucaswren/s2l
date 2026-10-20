package operations

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/lucaswren/s2l/internal/model"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestIncidentsMergeRecoverPersistAndIgnoreUDP(t *testing.T) {
	path := filepath.Join(t.TempDir(), "events.json")
	s := New(path)
	now := time.Now()
	st := model.SystemStatus{Mappings: []model.TunnelMapping{{ID: "a", NodeID: "n", Status: "running"}, {ID: "b", NodeID: "n", Status: "running"}}, UpstreamHealth: map[string]model.UpstreamHealth{}}
	for _, m := range st.Mappings {
		st.UpstreamHealth[m.ID] = model.UpstreamHealth{LocalState: "healthy", TCP: model.ProtocolHealth{State: "unavailable", CheckedAt: now}, DNS: model.ProtocolHealth{State: "healthy"}, UDP: model.ProtocolHealth{State: "unsupported"}}
	}
	s.Observe(st, now)
	s.Observe(st, now.Add(time.Second))
	v := s.View()
	if len(v.Incidents) != 1 || len(v.Incidents[0].Affected) != 2 || len(v.Events) != 1 {
		t.Fatal(v)
	}
	for id, h := range st.UpstreamHealth {
		h.TCP.State = "healthy"
		st.UpstreamHealth[id] = h
	}
	s.Observe(st, now.Add(2*time.Second))
	if len(s.View().Events) != 2 {
		t.Fatal(s.View())
	}
	if e := s.Flush(); e != nil {
		t.Fatal(e)
	}
	v = New(path).View()
	if len(v.Events) != 2 || v.Incidents[0].Resolved.IsZero() {
		t.Fatal(v)
	}
}
func TestNotificationCooldownMaintenanceAndNoSecretInView(t *testing.T) {
	calls := 0
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.Header.Get("Authorization") != "Bearer private-token" {
			t.Error("missing token")
		}
		w.WriteHeader(204)
	}))
	defer srv.Close()
	s := New(filepath.Join(t.TempDir(), "events.json"))
	s.client = srv.Client()
	if e := s.Configure(Settings{Enabled: true, Webhook: srv.URL, Token: "private-token"}); e != nil {
		t.Fatal(e)
	}
	s.mu.Lock()
	s.add(time.Now(), "incident-open", "m", "upstream")
	s.mu.Unlock()
	s.Deliver(context.Background())
	s.Deliver(context.Background())
	if calls != 1 {
		t.Fatal(calls)
	}
	b, _ := json.Marshal(s.View())
	if strings.Contains(string(b), "private-token") || strings.Contains(string(b), srv.URL) {
		t.Fatal(string(b))
	}
	if e := s.Configure(Settings{Enabled: true, MaintenanceUntil: time.Now().Add(time.Hour)}); e != nil {
		t.Fatal(e)
	}
	s.mu.Lock()
	s.add(time.Now(), "incident-open", "m2", "dns")
	s.mu.Unlock()
	s.Deliver(context.Background())
	if calls != 1 {
		t.Fatal("maintenance sent")
	}
	for i := 0; i < 1100; i++ {
		s.Audit("operation", "mapping", "POST HTTP 200")
	}
	if len(s.View().Events) != 1000 {
		t.Fatal("unbounded events")
	}
}

func TestSharedFailureIsEvidenceNotProvenHostOutage(t *testing.T) {
	s := New(filepath.Join(t.TempDir(), "ops.json"))
	now := time.Now()
	st := model.SystemStatus{Uptime: time.Hour, Mappings: []model.TunnelMapping{{ID: "a", NodeID: "n1", Status: "running"}, {ID: "b", NodeID: "n2", Status: "running"}}, UpstreamHealth: map[string]model.UpstreamHealth{}}
	for _, m := range st.Mappings {
		st.UpstreamHealth[m.ID] = model.UpstreamHealth{LocalState: "healthy", TCP: model.ProtocolHealth{State: "unavailable", CheckedAt: now}}
	}
	s.Observe(st, now)
	v := s.View()
	if len(v.Incidents) != 1 || v.Incidents[0].Scope != "server-suspected" || len(v.Incidents[0].Affected) != 2 {
		t.Fatal(v)
	}
}
func TestFailedNotificationStopsBatchAfterFiveAttempts(t *testing.T) {
	calls := 0
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls++; w.WriteHeader(503) }))
	defer srv.Close()
	s := New(filepath.Join(t.TempDir(), "ops.json"))
	s.client = srv.Client()
	if e := s.Configure(Settings{Enabled: true, Webhook: srv.URL}); e != nil {
		t.Fatal(e)
	}
	s.mu.Lock()
	s.add(time.Now(), "incident-open", "m", "dns")
	s.mu.Unlock()
	for i := 0; i < 7; i++ {
		s.mu.Lock()
		s.lastDelivery = time.Time{}
		s.mu.Unlock()
		s.Deliver(context.Background())
	}
	if calls != 5 || !strings.Contains(s.View().Delivery, "停止重试") || len(s.View().Events) != 1 {
		t.Fatal(calls, s.View())
	}
}

func TestUnknownSamplesAndStoppedMappingsAreNotRecovery(t *testing.T) {
	s := New(filepath.Join(t.TempDir(), "ops.json"))
	now := time.Now()
	st := model.SystemStatus{Uptime: time.Hour, Mappings: []model.TunnelMapping{{ID: "m", NodeID: "n", Status: "running"}}, UpstreamHealth: map[string]model.UpstreamHealth{"m": {TCP: model.ProtocolHealth{State: "unavailable", CheckedAt: now}}}}
	s.Observe(st, now)
	st.UpstreamHealth = map[string]model.UpstreamHealth{}
	s.Observe(st, now.Add(time.Second))
	for _, e := range s.View().Events {
		if e.Kind == "incident-recovered" {
			t.Fatal("unknown became recovery", e)
		}
	}
	st.Mappings[0].Status = "stopped"
	s.Observe(st, now.Add(2*time.Second))
	for _, e := range s.View().Events {
		if e.Kind == "incident-recovered" {
			t.Fatal("stop became recovery", e)
		}
	}
}
func TestDuplicateIncidentSurvivesCollectionFailure(t *testing.T) {
	s := New(filepath.Join(t.TempDir(), "ops.json"))
	now := time.Now()
	st := model.SystemStatus{Uptime: time.Hour, Mappings: []model.TunnelMapping{{ID: "m", Status: "running"}}, Sessions: []model.PPPSession{{MappingID: "m"}, {MappingID: "m"}}, UpstreamHealth: map[string]model.UpstreamHealth{"m": {TCP: model.ProtocolHealth{State: "healthy", CheckedAt: now}}}}
	s.Observe(st, now)
	st.SessionsError = "collection failed"
	st.Sessions = nil
	s.Observe(st, now.Add(time.Second))
	v := s.View()
	if len(v.Incidents) != 1 || !v.Incidents[0].Resolved.IsZero() {
		t.Fatal(v)
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func TestNewNotificationDoesNotInheritOldBatchRetries(t *testing.T) {
	s := New(filepath.Join(t.TempDir(), "ops.json"))
	if e := s.Configure(Settings{Enabled: true, Webhook: "https://example.com/hook"}); e != nil {
		t.Fatal(e)
	}
	batches := [][]Event{}
	s.client = &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		var b struct {
			Events []Event `json:"events"`
		}
		_ = json.NewDecoder(r.Body).Decode(&b)
		batches = append(batches, b.Events)
		return &http.Response{StatusCode: 503, Body: io.NopCloser(strings.NewReader("")), Header: make(http.Header)}, nil
	})}
	s.Audit("incident-open", "old", "dns")
	for i := 0; i < 4; i++ {
		s.lastDelivery = time.Time{}
		s.Deliver(context.Background())
	}
	s.Audit("incident-open", "new", "dns")
	s.lastDelivery = time.Time{}
	s.Deliver(context.Background())
	s.lastDelivery = time.Time{}
	s.Deliver(context.Background())
	if len(batches) != 6 || len(batches[4]) != 1 || batches[4][0].Resource != "old" || len(batches[5]) != 1 || batches[5][0].Resource != "new" {
		t.Fatal(batches)
	}
}
func TestNotificationChangeCancelsOldDeliveryAndRejectsStaleConfig(t *testing.T) {
	s := New(filepath.Join(t.TempDir(), "ops.json"))
	if e := s.Configure(Settings{Enabled: true, Webhook: "https://example.com/old", Token: "old-token"}); e != nil {
		t.Fatal(e)
	}
	revision := s.View().Revision
	started, release := make(chan context.Context, 1), make(chan struct{})
	s.client = &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		started <- r.Context()
		<-release
		return &http.Response{StatusCode: 503, Body: io.NopCloser(strings.NewReader("")), Header: make(http.Header)}, nil
	})}
	s.Audit("incident-open", "old", "dns")
	done := make(chan struct{})
	go func() { s.Deliver(context.Background()); close(done) }()
	oldCtx := <-started
	if e := s.Configure(Settings{Enabled: true, Webhook: "https://example.com/new", Revision: revision}); e != nil {
		t.Fatal(e)
	}
	select {
	case <-oldCtx.Done():
	case <-time.After(time.Second):
		t.Fatal("old delivery not canceled")
	}
	s.Audit("incident-open", "new", "dns")
	close(release)
	<-done
	if s.deliveryAttempts != 0 || len(s.pending) != 1 || s.data.Settings.Token != "" {
		t.Fatal("old result affected new configuration")
	}
	if e := s.Configure(Settings{Enabled: false, Revision: revision}); e == nil {
		t.Fatal("stale settings accepted")
	}
}
func TestIncidentRetentionPrioritizesActiveAndStaysBounded(t *testing.T) {
	s := New(filepath.Join(t.TempDir(), "ops.json"))
	for i := 0; i < 600; i++ {
		s.data.Incidents = append(s.data.Incidents, Incident{Key: fmt.Sprint(i), Updated: time.Now()})
	}
	s.trimIncidents()
	if len(s.data.Incidents) != 500 {
		t.Fatal(len(s.data.Incidents))
	}
}

func TestFirstSuccessfulProbeDoesNotPrematurelyResolveIncident(t *testing.T) {
	s := New(filepath.Join(t.TempDir(), "ops.json"))
	now := time.Now()
	st := model.SystemStatus{Uptime: time.Hour, Mappings: []model.TunnelMapping{{ID: "m", NodeID: "n", Status: "running"}}, UpstreamHealth: map[string]model.UpstreamHealth{"m": {TCP: model.ProtocolHealth{State: "unavailable", CheckedAt: now}}}}
	s.Observe(st, now)
	h := st.UpstreamHealth["m"]
	h.TCP = model.ProtocolHealth{State: "checking", MeasuredState: "healthy", CheckedAt: now.Add(time.Second)}
	st.UpstreamHealth["m"] = h
	s.Observe(st, now.Add(time.Second))
	for _, ev := range s.View().Events {
		if ev.Kind == "incident-recovered" {
			t.Fatal("recovered before confirmation")
		}
	}
	h.TCP.State = "healthy"
	h.TCP.CheckedAt = now.Add(2 * time.Second)
	st.UpstreamHealth["m"] = h
	s.Observe(st, now.Add(2*time.Second))
	found := false
	for _, ev := range s.View().Events {
		if ev.Kind == "incident-recovered" {
			found = true
		}
	}
	if !found {
		t.Fatal("confirmed recovery missing")
	}
}
