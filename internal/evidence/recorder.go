// Package evidence keeps bounded, persistent outage evidence independently of Debug.
package evidence

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/lucaswren/s2l/internal/debuglog"
	"github.com/lucaswren/s2l/internal/model"
)

const MaxReports = 8
const MaxReportBytes = 2 << 20
const Cooldown = 10 * time.Minute

type Sample struct {
	At        time.Time          `json:"at"`
	Status    model.SystemStatus `json:"status"`
	Resources debuglog.Resources `json:"resources"`
}
type Summary struct {
	ID        string    `json:"id"`
	MappingID string    `json:"mapping_id"`
	Reason    string    `json:"reason"`
	Started   time.Time `json:"started_at"`
	Triggered time.Time `json:"triggered_at"`
	Finished  time.Time `json:"finished_at,omitempty"`
	Capture   string    `json:"capture"`
}
type Report struct {
	Summary
	Before    []Sample          `json:"before"`
	After     []Sample          `json:"after"`
	Snapshots map[string]string `json:"snapshots"`
	Notice    string            `json:"notice"`
}
type Request struct {
	Mapping    model.TunnelMapping
	Node       model.SocksNode
	Reason     string
	LogTail    string
	Detail     string
	before     []Sample
	triggered  time.Time
	generation uint64
	observed   bool
}
type Collected struct {
	Snapshots map[string]string
	PCAP      []byte
	Capture   string
}
type Collect func(context.Context, Request) Collected
type View struct {
	Enabled                bool      `json:"enabled"`
	Busy                   bool      `json:"busy"`
	Queued                 int       `json:"queued"`
	Reports                []Summary `json:"reports"`
	Warning                string    `json:"warning,omitempty"`
	PacketCaptureAvailable bool      `json:"packet_capture_available"`
}
type Recorder struct {
	mu               sync.Mutex
	filesMu          sync.Mutex
	pending          int
	generation       uint64
	running, stopped bool
	dir              string
	enabled, busy    bool
	warning          string
	ring             []Sample
	ringSizes        []int
	ringBytes        int
	peers            map[string]int
	resolverFailures map[string]uint64
	resolverPIDs     map[string]int
	last             map[string]time.Time
	queue            chan Request
	collect          Collect
	enrich           func(Request) Request
	available        bool
	clean            func([]byte) []byte
	cancel           context.CancelFunc
}

var reportID = regexp.MustCompile(`^[0-9]{8}T[0-9]{6}\.[0-9]{9}Z$`)

func New(dir string, collect Collect, available bool, clean func([]byte) []byte, enrich ...func(Request) Request) *Recorder {
	r := &Recorder{dir: dir, enabled: true, collect: collect, available: available, clean: clean, queue: make(chan Request, 4), peers: map[string]int{}, resolverFailures: map[string]uint64{}, resolverPIDs: map[string]int{}, last: map[string]time.Time{}}
	if len(enrich) > 0 {
		r.enrich = enrich[0]
	}
	var settings struct {
		Enabled *bool `json:"enabled"`
	}
	if b, err := readBounded(filepath.Join(dir, "settings.json")); err == nil {
		if json.Unmarshal(b, &settings) == nil && settings.Enabled != nil {
			r.enabled = *settings.Enabled
		} else {
			r.warning = "取证设置无效，使用默认设置"
		}
	} else if !os.IsNotExist(err) {
		r.warning = "取证设置读取失败，使用默认设置"
	}
	return r
}
func (r *Recorder) SetEnabled(enabled bool) error {
	r.filesMu.Lock()
	defer r.filesMu.Unlock()
	if err := r.prepare(); err != nil {
		return err
	}
	b, _ := json.Marshal(struct {
		Enabled bool `json:"enabled"`
	}{enabled})
	if err := atomicWrite(filepath.Join(r.dir, "settings.json"), b); err != nil {
		return err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.enabled = enabled
	if !enabled {
		r.generation++
		r.ring = nil
		r.ringSizes = nil
		r.ringBytes = 0
		r.peers = map[string]int{}
		r.resolverFailures = map[string]uint64{}
		r.resolverPIDs = map[string]int{}
		r.last = map[string]time.Time{}
		r.drainLocked()
		r.busy = r.pending > 0
		if r.cancel != nil {
			r.cancel()
		}
	}
	return nil
}
func (r *Recorder) prepare() error {
	if err := os.MkdirAll(r.dir, 0700); err != nil {
		return err
	}
	info, err := os.Lstat(r.dir)
	if err != nil {
		return err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("invalid evidence directory")
	}
	entries, _ := os.ReadDir(r.dir)
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".json.tmp") || strings.HasSuffix(e.Name(), ".pcap.tmp") {
			id := strings.TrimSuffix(strings.TrimSuffix(e.Name(), ".tmp"), filepath.Ext(strings.TrimSuffix(e.Name(), ".tmp")))
			if reportID.MatchString(id) {
				if err := os.Remove(filepath.Join(r.dir, e.Name())); err != nil && !os.IsNotExist(err) {
					return err
				}
			}
		}
	}
	return os.Chmod(r.dir, 0700)
}
func atomicWrite(path string, b []byte) error {
	f, err := os.OpenFile(path+".tmp", os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	_, err = f.Write(b)
	if err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err == nil {
		err = closeErr
	}
	if err != nil {
		_ = os.Remove(path + ".tmp")
		return err
	}
	return os.Rename(path+".tmp", path)
}
func (r *Recorder) scrub(b []byte) []byte {
	if r.clean != nil {
		return r.clean(b)
	}
	return b
}

// Observe stores two minutes of pre-fault status, never mapping credentials.
// A missing PPP session is evidence only after an observed connected session.
func (r *Recorder) Observe(status model.SystemStatus, now time.Time) {
	r.mu.Lock()
	if !r.enabled {
		r.mu.Unlock()
		return
	}
	generation := r.generation
	r.mu.Unlock()
	status.Mappings = append([]model.TunnelMapping(nil), status.Mappings...)
	for i := range status.Mappings {
		status.Mappings[i].L2TPPass = ""
	}
	b, err := json.Marshal(Sample{At: now, Status: status, Resources: debuglog.SampleResources(filepath.Dir(r.dir))})
	var sample Sample
	sampleBytes := 0
	valid := err == nil && len(b) <= 128<<10
	if valid {
		cleaned := preserveMetadata(r.scrub(b), Sample{At: now, Status: status})
		sampleBytes = len(cleaned)
		valid = sampleBytes <= 128<<10 && json.Unmarshal(cleaned, &sample) == nil
	}
	r.mu.Lock()
	if !r.enabled || r.generation != generation {
		r.mu.Unlock()
		return
	}
	if valid {
		r.ring = append(r.ring, sample)
		r.ringSizes = append(r.ringSizes, sampleBytes)
		r.ringBytes += sampleBytes
		for len(r.ring) > 24 || r.ringBytes > 384<<10 {
			r.ringBytes -= r.ringSizes[0]
			r.ringSizes = r.ringSizes[1:]
			r.ring = r.ring[1:]
		}
	}
	counts := map[string]int{}
	for _, p := range status.Sessions {
		counts[p.MappingID]++
	}
	requests := []Request{}
	present := map[string]bool{}
	for _, m := range status.Mappings {
		present[m.ID] = true
		if m.Status != model.StatusRunning {
			delete(r.peers, m.ID)
			delete(r.resolverFailures, m.ID)
			delete(r.resolverPIDs, m.ID)
			continue
		}
		health := status.UpstreamHealth[m.ID]
		pid := status.SingboxPIDs[m.TunName]
		if previous, ok := r.resolverFailures[m.ID]; health.Resolver.Failures > 0 && (!ok || pid != r.resolverPIDs[m.ID] || health.Resolver.Failures > previous) {
			requests = append(requests, Request{Mapping: m, Reason: "live-dns", Detail: fmt.Sprintf("resolver failures: %d -> %d", previous, health.Resolver.Failures)})
		}
		r.resolverFailures[m.ID] = health.Resolver.Failures
		r.resolverPIDs[m.ID] = pid
		if status.SessionsError == "" {
			if r.peers[m.ID] > 0 && counts[m.ID] == 0 {
				requests = append(requests, Request{Mapping: m, Reason: "ppp-disconnected"})
			}
			r.peers[m.ID] = counts[m.ID]
		}
	}
	for id := range r.peers {
		if !present[id] {
			delete(r.peers, id)
		}
	}
	for id := range r.resolverFailures {
		if !present[id] {
			delete(r.resolverFailures, id)
			delete(r.resolverPIDs, id)
		}
	}
	for id := range r.last {
		if !present[id] {
			delete(r.last, id)
		}
	}
	r.mu.Unlock()
	// Enrichment reads the current process tail before a restart can replace it.
	// It runs outside the state lock, including for counter/session triggers.
	for _, req := range requests {
		req.generation = generation
		req.observed = true
		r.Trigger(req, now)
	}
}

// Trigger performs no file I/O. One worker serializes collection; up to four
// pending faults keep their original pre-fault history instead of being lost.
func (r *Recorder) Trigger(req Request, now time.Time) bool {
	r.mu.Lock()
	generation := r.generation
	allowed := r.enabled && !r.stopped && (!req.observed || req.generation == generation) && now.Sub(r.last[req.Mapping.ID]) >= Cooldown
	r.mu.Unlock()
	if !allowed {
		return false
	}
	if r.enrich != nil {
		req = r.enrich(req)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if generation != r.generation {
		return false
	}
	return r.enqueueLocked(req, now)
}
func (r *Recorder) enqueueLocked(req Request, now time.Time) bool {
	if !r.enabled || r.stopped || now.Sub(r.last[req.Mapping.ID]) < Cooldown {
		return false
	}
	req.before = append([]Sample(nil), r.ring...)
	req.triggered = now
	req.generation = r.generation
	select {
	case r.queue <- req:
		r.last[req.Mapping.ID] = now
		r.pending++
		r.busy = true
		return true
	default:
		r.warning = "故障取证队列已满，本次未排队"
		return false
	}
}
func (r *Recorder) Run(ctx context.Context) {
	r.mu.Lock()
	if r.running || r.stopped {
		r.mu.Unlock()
		return
	}
	r.running = true
	r.mu.Unlock()
	defer func() {
		r.mu.Lock()
		r.drainLocked()
		r.pending = 0
		r.busy = false
		r.running = false
		r.stopped = true
		r.mu.Unlock()
	}()
	for {
		select {
		case <-ctx.Done():
			return
		case req := <-r.queue:
			r.capture(ctx, req)
		}
	}
}
func (r *Recorder) drainLocked() {
	for {
		select {
		case <-r.queue:
			r.pending--
		default:
			return
		}
	}
}
func (r *Recorder) setWarning(value string) { r.mu.Lock(); r.warning = value; r.mu.Unlock() }
func (r *Recorder) capture(ctx context.Context, req Request) {
	defer func() { r.mu.Lock(); r.pending--; r.busy = r.pending > 0; r.cancel = nil; r.mu.Unlock() }()
	now := time.Now().UTC()
	r.mu.Lock()
	if !r.enabled || req.generation != r.generation || ctx.Err() != nil {
		r.mu.Unlock()
		return
	}
	work, cancel := context.WithTimeout(ctx, 35*time.Second)
	r.cancel = cancel
	r.mu.Unlock()
	defer cancel()
	report := Report{Summary: Summary{ID: now.Format("20060102T150405.000000000Z"), MappingID: req.Mapping.ID, Reason: req.Reason, Started: now, Triggered: req.triggered, Capture: "collecting"}, Before: req.before, Snapshots: map[string]string{}, Notice: "Header-only IPv4 capture; application payload and L2TP/PPP authentication are excluded. Contains network addresses. Captures start after detection and may queue; downstream iWAN is not observed. No restart or route changes are performed."}
	report.Snapshots["singbox-before"] = req.LogTail
	report.Snapshots["trigger-details"] = req.Detail
	if err := r.save(report, nil, req.Node.Password, req.Mapping.L2TPPass); err != nil {
		r.setWarning("故障取证无法写入：" + err.Error())
		return
	}
	data := r.collect(work, req)
	for k, v := range data.Snapshots {
		report.Snapshots[k] = v
	}
	report.Capture = data.Capture
	report.Finished = time.Now().UTC()
	r.mu.Lock()
	for _, sample := range r.ring {
		if sample.At.After(now) {
			report.After = append(report.After, sample)
		}
	}
	r.mu.Unlock()
	if err := r.save(report, data.PCAP, req.Node.Password, req.Mapping.L2TPPass); err != nil {
		r.setWarning("故障取证保存失败：" + err.Error())
	} else {
		r.setWarning("")
	}
}
func (r *Recorder) save(report Report, pcap []byte, secrets ...string) error {
	r.filesMu.Lock()
	defer r.filesMu.Unlock()
	if err := r.prepare(); err != nil {
		return err
	}
	b, err := json.Marshal(report)
	if err != nil {
		return err
	}
	b = r.scrub(b)
	b = CleanJSON(b, secrets)
	b = preserveMetadata(b, report)
	if len(b) > MaxReportBytes || len(pcap) > MaxReportBytes {
		return fmt.Errorf("evidence size limit exceeded")
	}
	// Reserve space before writing. A failed rotation must not allow each
	// subsequent outage to grow the directory beyond the advertised limit.
	if err := r.rotate(report.ID); err != nil {
		return err
	}
	if len(pcap) > 0 {
		if err := atomicWrite(filepath.Join(r.dir, report.ID+".pcap"), pcap); err != nil {
			return err
		}
	}
	if err := atomicWrite(filepath.Join(r.dir, report.ID+".json"), b); err != nil {
		return err
	}
	return nil
}
func (r *Recorder) rotate(current string) error {
	entries, err := os.ReadDir(r.dir)
	if err != nil {
		return err
	}
	ids := []string{}
	exists := false
	for _, e := range entries {
		if filepath.Ext(e.Name()) == ".json" && reportID.MatchString(e.Name()[:len(e.Name())-5]) {
			ids = append(ids, e.Name()[:len(e.Name())-5])
			if e.Name() == current+".json" {
				exists = true
			}
		}
	}
	sort.Strings(ids)
	limit := MaxReports
	if !exists {
		limit--
	}
	for len(ids) > limit {
		index := 0
		if ids[index] == current {
			index++
		}
		if index >= len(ids) {
			return fmt.Errorf("cannot rotate current report")
		}
		for _, ext := range []string{".pcap", ".json"} {
			if err := os.Remove(filepath.Join(r.dir, ids[index]+ext)); err != nil && !os.IsNotExist(err) {
				return fmt.Errorf("rotate report: %w", err)
			}
		}
		ids = append(ids[:index], ids[index+1:]...)
	}
	return nil
}
func (r *Recorder) View() View {
	r.mu.Lock()
	v := View{Enabled: r.enabled, Busy: r.busy, Queued: len(r.queue), Warning: r.warning, PacketCaptureAvailable: r.available, Reports: []Summary{}}
	r.mu.Unlock()
	r.filesMu.Lock()
	defer r.filesMu.Unlock()
	entries, err := os.ReadDir(r.dir)
	if err != nil {
		if !os.IsNotExist(err) {
			v.Warning = "故障取证目录读取失败"
		}
		return v
	}
	for _, e := range entries {
		name := e.Name()
		if len(name) < 6 || filepath.Ext(name) != ".json" || !reportID.MatchString(name[:len(name)-5]) {
			continue
		}
		b, err := readBounded(filepath.Join(r.dir, name))
		var summary Summary
		if err == nil && json.Unmarshal(b, &summary) == nil {
			v.Reports = append(v.Reports, summary)
		}
	}
	sort.Slice(v.Reports, func(i, j int) bool { return v.Reports[i].ID > v.Reports[j].ID })
	if len(v.Reports) > MaxReports {
		v.Reports = v.Reports[:MaxReports]
	}
	return v
}
func (r *Recorder) Read(id string) (map[string][]byte, error) {
	r.filesMu.Lock()
	defer r.filesMu.Unlock()
	if !reportID.MatchString(id) {
		return nil, os.ErrNotExist
	}
	b, err := readBounded(filepath.Join(r.dir, id+".json"))
	if err != nil {
		return nil, err
	}
	var report Report
	if err := json.Unmarshal(b, &report); err != nil {
		return nil, err
	}
	entries := map[string][]byte{"report.json": preserveMetadata(r.scrub(b), report)}
	if b, err := readBounded(filepath.Join(r.dir, id+".pcap")); err == nil {
		entries["headers.pcap"] = b
	}
	return entries, nil
}
func readBounded(path string) ([]byte, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || info.Size() > MaxReportBytes {
		return nil, fmt.Errorf("invalid evidence file")
	}
	return os.ReadFile(path)
}

// CleanJSON replaces decoded string values, never JSON syntax or numbers.
func CleanJSON(data []byte, secrets []string) []byte {
	variants := make([]string, 0, len(secrets)*3)
	for _, secret := range secrets {
		if secret == "" {
			continue
		}
		encoded, _ := json.Marshal(secret)
		escaped := string(encoded[1 : len(encoded)-1])
		variants = append(variants, secret, escaped, strings.NewReplacer(`\u003c`, "<", `\u003e`, ">", `\u0026`, "&").Replace(escaped))
	}
	secrets = variants
	sort.Slice(secrets, func(i, j int) bool { return len(secrets[i]) > len(secrets[j]) })
	pairs := []string{}
	for _, s := range secrets {
		if s != "" {
			pairs = append(pairs, s, "[REDACTED]")
		}
	}
	clean := strings.NewReplacer(pairs...).Replace
	var value any
	decoder := json.NewDecoder(strings.NewReader(string(data)))
	decoder.UseNumber()
	if decoder.Decode(&value) != nil {
		return []byte(`{"error":"invalid evidence JSON"}`)
	}
	var walk func(any) any
	walk = func(v any) any {
		switch x := v.(type) {
		case string:
			return clean(x)
		case []any:
			for i := range x {
				x[i] = walk(x[i])
			}
		case map[string]any:
			for k, v := range x {
				x[k] = walk(v)
			}
		}
		return v
	}
	b, _ := json.Marshal(walk(value))
	return b
}
