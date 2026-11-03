package evidence

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/lucaswren/s2l/internal/model"
)

func TestOutageEvidencePersistsAndBoundsRetention(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "evidence")
	secret := "password-\"private"
	r := New(dir, func(context.Context, Request) Collected {
		return Collected{Snapshots: map[string]string{"log": "failed " + secret}, Capture: "unavailable: tcpdump missing"}
	}, false, func(b []byte) []byte { return CleanJSON(b, []string{secret}) })
	m := model.TunnelMapping{ID: "map_a", Status: model.StatusRunning, L2TPPass: secret}
	status := model.SystemStatus{Mappings: []model.TunnelMapping{m}, Sessions: []model.PPPSession{{MappingID: m.ID}}}
	r.Observe(status, time.Now().Add(-time.Second))
	if r.View().Busy {
		t.Fatal("healthy status triggered capture")
	}
	status.Sessions = nil
	r.Observe(status, time.Now())
	if !r.View().Busy {
		t.Fatal("PPP disconnect not captured")
	}
	if r.Trigger(Request{Mapping: m}, time.Now()) {
		t.Fatal("concurrent duplicate accepted")
	}
	r.capture(context.Background(), <-r.queue)
	view := r.View()
	if len(view.Reports) != 1 || view.Reports[0].Finished.IsZero() {
		t.Fatal(view)
	}
	entries, err := r.Read(view.Reports[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(entries["report.json"], []byte("private")) {
		t.Fatal("credential persisted")
	}
	var report Report
	if err = json.Unmarshal(entries["report.json"], &report); err != nil || len(report.Before) != 2 {
		t.Fatal(err, report)
	}
	if r.Trigger(Request{Mapping: m}, time.Now().Add(time.Minute)) {
		t.Fatal("cooldown ignored")
	}
	for i := 0; i < 12; i++ {
		report.ID = time.Date(2026, 11, 2, 0, 0, i, 0, time.UTC).Format("20060102T150405.000000000Z")
		if err = r.save(report, nil); err != nil {
			t.Fatal(err)
		}
	}
	if len(New(dir, nil, false, nil).View().Reports) != MaxReports {
		t.Fatal("retention or restart failed")
	}
	if _, err = r.Read("../settings"); err == nil {
		t.Fatal("path traversal accepted")
	}
	if err = r.SetEnabled(false); err != nil {
		t.Fatal(err)
	}
	if New(dir, nil, false, nil).View().Enabled {
		t.Fatal("setting lost on restart")
	}
	if r.Trigger(Request{Mapping: m}, time.Now().Add(time.Hour)) {
		t.Fatal("disabled recorder triggered")
	}
}
func TestIdlePPPAndSnapshotErrorsDoNotPretendDisconnect(t *testing.T) {
	r := New(t.TempDir(), nil, false, nil)
	m := model.TunnelMapping{ID: "m", Status: model.StatusRunning}
	s := model.SystemStatus{Mappings: []model.TunnelMapping{m}}
	r.Observe(s, time.Now())
	if r.View().Busy {
		t.Fatal("idle is not a disconnect")
	}
	s.Sessions = []model.PPPSession{{MappingID: m.ID}}
	r.Observe(s, time.Now())
	s.Sessions = nil
	s.SessionsError = "snapshot failed"
	r.Observe(s, time.Now())
	if r.View().Busy {
		t.Fatal("inspection error is not a disconnect")
	}
}
func TestRingStorageAndJSONRedactionAreBounded(t *testing.T) {
	r := New(t.TempDir(), nil, false, nil)
	for i := 0; i < 100; i++ {
		r.Observe(model.SystemStatus{SessionsError: fmt.Sprintf("%08000d", i)}, time.Now())
	}
	if len(r.ring) > 24 || r.ringBytes > 384<<10 {
		t.Fatal("unbounded ring")
	}
	input := []byte(`{"n":123,"s":"123 private\\\"secret"}`)
	b := CleanJSON(input, []string{"123", "private\\\"secret"})
	var v map[string]any
	if err := json.Unmarshal(b, &v); err != nil || v["n"] != float64(123) {
		t.Fatal(string(b), err)
	}
	if err := r.SetEnabled(true); err != nil {
		t.Fatal(err)
	}
	if info, err := os.Stat(filepath.Join(r.dir, "settings.json")); err != nil || info.Size() > 1024 {
		t.Fatal("invalid settings file")
	}
}
func TestDisablingCancelsActiveCapture(t *testing.T) {
	started := make(chan struct{})
	finished := make(chan struct{})
	r := New(t.TempDir(), func(ctx context.Context, _ Request) Collected {
		close(started)
		<-ctx.Done()
		return Collected{Capture: "cancelled"}
	}, false, nil)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { r.Run(ctx); close(finished) }()
	r.Trigger(Request{Mapping: model.TunnelMapping{ID: "m"}}, time.Now())
	select {
	case <-started:
	case <-time.After(3 * time.Second):
		t.Fatal("capture did not start")
	}
	if err := r.SetEnabled(false); err != nil {
		t.Fatal(err)
	}
	cancel()
	select {
	case <-finished:
	case <-time.After(3 * time.Second):
		t.Fatal("capture did not stop")
	}
	if r.View().Busy {
		t.Fatal("busy after cancellation")
	}
}

func TestEscapedCredentialsInNestedJournalTextAreRedacted(t *testing.T) {
	secret := "quoted\"password<>&"
	nested, _ := json.Marshal(map[string]string{"error": secret})
	input, _ := json.Marshal(map[string]string{"journal": string(nested)})
	out := CleanJSON(input, []string{secret})
	var value map[string]string
	if err := json.Unmarshal(out, &value); err != nil {
		t.Fatal(err)
	}
	if bytes.Contains([]byte(value["journal"]), []byte("password")) {
		t.Fatal("escaped journal credential retained", value)
	}
}

func TestTriggerDoesNotWaitForStorageLock(t *testing.T) {
	r := New(t.TempDir(), nil, false, nil)
	r.filesMu.Lock()
	done := make(chan bool, 1)
	go func() { done <- r.Trigger(Request{Mapping: model.TunnelMapping{ID: "m"}}, time.Now()) }()
	select {
	case started := <-done:
		if !started {
			t.Error("capture could not queue while storage busy")
		}
	case <-time.After(time.Second):
		t.Error("health worker blocked on storage")
	}
	r.filesMu.Unlock()
}

func TestConcurrentMappingFaultsKeepTheirTriggerHistoryAndLogs(t *testing.T) {
	a := model.TunnelMapping{ID: "a", TunName: "tun101", Status: model.StatusRunning}
	b := model.TunnelMapping{ID: "b", TunName: "tun102", Status: model.StatusRunning}
	logTail := "before failure"
	r := New(t.TempDir(), func(context.Context, Request) Collected { return Collected{Capture: "unavailable"} }, false, nil, func(req Request) Request { req.LogTail = logTail; req.Node.Password = "frozen-secret"; return req })
	now := time.Now()
	status := model.SystemStatus{Mappings: []model.TunnelMapping{a, b}, Sessions: []model.PPPSession{{MappingID: "a"}, {MappingID: "b"}}}
	r.Observe(status, now)
	if !r.Trigger(Request{Mapping: a, Reason: "upstream"}, now) {
		t.Fatal("first fault rejected")
	}
	status.Sessions = status.Sessions[:1]
	r.Observe(status, now.Add(time.Second))
	if r.View().Queued != 2 {
		t.Fatal("second mapping fault was lost while busy", r.View())
	}
	logTail = "replacement process log"
	for i := 0; i < 30; i++ {
		r.Observe(model.SystemStatus{Mappings: []model.TunnelMapping{a, b}}, now.Add(time.Duration(i+2)*time.Second))
	}
	first, second := <-r.queue, <-r.queue
	if first.Mapping.ID != "a" || second.Mapping.ID != "b" || second.LogTail != "before failure" || len(second.before) != 2 || !second.triggered.Equal(now.Add(time.Second)) {
		t.Fatal("queued evidence lost original context", second)
	}
	r.capture(context.Background(), first)
	r.capture(context.Background(), second)
	if r.View().Busy || len(r.View().Reports) != 2 {
		t.Fatal("queued capture did not finish", r.View())
	}
}

func TestOversizedStatusStillTriggersFaultAndPIDResetStartsNewDNSBaseline(t *testing.T) {
	r := New(t.TempDir(), nil, false, nil)
	m := model.TunnelMapping{ID: "m", TunName: "tun101", Status: model.StatusRunning}
	status := model.SystemStatus{Mappings: []model.TunnelMapping{m}, SessionsError: strings.Repeat("x", 150<<10), SingboxPIDs: map[string]int{"tun101": 101}, UpstreamHealth: map[string]model.UpstreamHealth{"m": {Resolver: model.ResolverStatus{Failures: 5}}}}
	now := time.Now()
	r.Observe(status, now)
	if len(r.queue) != 1 || len(r.ring) != 0 {
		t.Fatal("size limit disabled fault detection")
	}
	status.SingboxPIDs["tun101"] = 202
	status.UpstreamHealth["m"] = model.UpstreamHealth{Resolver: model.ResolverStatus{Failures: 1}}
	r.Observe(status, now.Add(Cooldown+time.Second))
	if len(r.queue) != 2 {
		t.Fatal("new process DNS failures hidden by old counters")
	}
}

func TestDisableAndReenableRejectAnEnrichmentFromTheOldGeneration(t *testing.T) {
	entered, release := make(chan struct{}), make(chan struct{})
	r := New(t.TempDir(), nil, false, nil, func(req Request) Request { close(entered); <-release; return req })
	result := make(chan bool, 1)
	go func() { result <- r.Trigger(Request{Mapping: model.TunnelMapping{ID: "m"}}, time.Now()) }()
	<-entered
	if err := r.SetEnabled(false); err != nil {
		t.Fatal(err)
	}
	if err := r.SetEnabled(true); err != nil {
		t.Fatal(err)
	}
	close(release)
	if <-result || r.View().Busy {
		t.Fatal("old-generation work resurrected")
	}
}

func TestSettingsCorruptionAndFailedRotationAreVisibleAndBounded(t *testing.T) {
	for _, bad := range []string{"null", "{}", `{"enabled":"true"}`} {
		dir := t.TempDir()
		if err := os.WriteFile(filepath.Join(dir, "settings.json"), []byte(bad), 0600); err != nil {
			t.Fatal(err)
		}
		view := New(dir, nil, false, nil).View()
		if !view.Enabled || view.Warning == "" {
			t.Fatal("invalid settings silently disabled evidence", bad, view)
		}
	}
	dir := t.TempDir()
	r := New(dir, nil, false, nil)
	for i := 0; i < MaxReports; i++ {
		id := fmt.Sprintf("20260101T0000%02d.000000000Z", i)
		if err := r.save(Report{Summary: Summary{ID: id}}, nil); err != nil {
			t.Fatal(err)
		}
	}
	oldest := filepath.Join(dir, "20260101T000000.000000000Z.json")
	if err := os.Remove(oldest); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(oldest, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(oldest, "keep"), []byte("simulate unremovable report"), 0600); err != nil {
		t.Fatal(err)
	}
	newID := "20260101T010000.000000000Z"
	if err := r.save(Report{Summary: Summary{ID: newID}}, nil); err == nil {
		t.Fatal("rotation failure ignored")
	}
	if _, err := os.Stat(filepath.Join(dir, newID+".json")); !os.IsNotExist(err) {
		t.Fatal("new report exceeded retention cap")
	}
}

func TestShortPasswordCannotCorruptArchiveIdentityOrTypedTimestamps(t *testing.T) {
	r := New(t.TempDir(), func(context.Context, Request) Collected {
		return Collected{Snapshots: map[string]string{"journal": "password=1"}, Capture: "unavailable"}
	}, false, func(b []byte) []byte { return CleanJSON(b, []string{"1"}) })
	now := time.Date(2026, 11, 1, 1, 1, 1, 0, time.UTC)
	m := model.TunnelMapping{ID: "m1", Status: model.StatusRunning}
	r.Observe(model.SystemStatus{Mappings: []model.TunnelMapping{m}, CollectedAt: now, UpstreamHealth: map[string]model.UpstreamHealth{"m1": {CheckedAt: now}}}, now)
	if !r.Trigger(Request{Mapping: m, Reason: "manual"}, now) {
		t.Fatal("trigger failed")
	}
	r.capture(context.Background(), <-r.queue)
	view := r.View()
	if len(view.Reports) != 1 || !reportID.MatchString(view.Reports[0].ID) || view.Reports[0].MappingID != "m1" {
		t.Fatal("redaction made the archive inaccessible", view)
	}
	files, err := r.Read(view.Reports[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	var report Report
	if err := json.Unmarshal(files["report.json"], &report); err != nil || len(report.Before) != 1 {
		t.Fatal("redaction broke typed history", err, string(files["report.json"]))
	}
	if report.ID != view.Reports[0].ID || !report.Triggered.Equal(now) || !report.Before[0].Status.CollectedAt.Equal(now) || !report.Before[0].Status.UpstreamHealth["m1"].CheckedAt.Equal(now) {
		t.Fatal("metadata changed", report)
	}
	if strings.Contains(report.Snapshots["journal"], "1") {
		t.Fatal("password retained in free-form log")
	}
}

func TestRedactionExpansionCannotExceedHistoryBudget(t *testing.T) {
	r := New(t.TempDir(), nil, false, func(b []byte) []byte { return CleanJSON(b, []string{"1"}) })
	for i := 0; i < 30; i++ {
		r.Observe(model.SystemStatus{SessionsError: strings.Repeat("1", 6000)}, time.Now())
	}
	actual := 0
	for _, sample := range r.ring {
		b, _ := json.Marshal(sample)
		actual += len(b)
	}
	if actual > 384<<10 || actual != r.ringBytes || len(r.ring) == 0 {
		t.Fatal("post-redaction history exceeded its memory budget", actual, r.ringBytes)
	}
	count := len(r.ring)
	r.Observe(model.SystemStatus{SessionsError: strings.Repeat("1", 20000)}, time.Now())
	if len(r.ring) != count {
		t.Fatal("oversized redacted frame retained")
	}
}
