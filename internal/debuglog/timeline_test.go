package debuglog

import (
	"github.com/lucaswren/s2l/internal/model"
	"strings"
	"testing"
	"time"
)

func TestTimelineRecordsOnlySessionDeltasAndBounds(t *testing.T) {
	c := &Collector{}
	status := model.SystemStatus{UpstreamHealth: map[string]model.UpstreamHealth{"m": {State: "healthy", TunName: "tun1", Process: model.ProcessStatus{Timeouts: 100, LogBytes: 100}, Resolver: model.ResolverStatus{Active: "doh", Failures: 10}}}}
	now := time.Now()
	c.RecordStatus(status, Resources{}, now)
	if len(c.Analysis().Events) != 0 || len(c.Analysis().Resources) != 0 {
		t.Fatal("sampled when disabled")
	}
	c.Set(true)
	c.RecordStatus(status, Resources{}, now)
	if len(c.Analysis().Events) != 1 {
		t.Fatal("lifetime totals reported as new incidents")
	}
	h := status.UpstreamHealth["m"]
	h.Process.Timeouts += 2
	h.Resolver.Active = "tcp"
	h.Resolver.Failures++
	status.UpstreamHealth["m"] = h
	c.RecordStatus(status, Resources{HeapBytes: 123}, now.Add(time.Second))
	kinds := map[string]bool{}
	for _, e := range c.Analysis().Events {
		kinds[e.Kind] = true
	}
	if !kinds["timeout"] || !kinds["dns-switch"] || !kinds["dns-failure"] {
		t.Fatal("missing timeline evidence", kinds)
	}
	for i := 0; i < 600; i++ {
		h.State = "healthy"
		if i%2 == 0 {
			h.State = "unstable"
		}
		status.UpstreamHealth["m"] = h
		c.RecordStatus(status, Resources{}, now.Add(time.Duration(i)*time.Second))
	}
	a := c.Analysis()
	if len(a.Events) > eventLimit || a.DroppedEvents == 0 || len(a.Resources) > 360 {
		t.Fatal("unbounded timeline")
	}
	c.Set(false)
	before := len(a.Events)
	c.RecordStatus(status, Resources{}, now)
	if len(c.Analysis().Events) != before {
		t.Fatal("recorded after stop")
	}
	c.Set(true)
	if len(c.Analysis().Events) != 0 || len(c.Analysis().Resources) != 0 {
		t.Fatal("new session leaked old timeline")
	}
}

func TestTimelineCounterResetDoesNotInventFailures(t *testing.T) {
	c := &Collector{}
	c.Set(true)
	h := model.UpstreamHealth{Process: model.ProcessStatus{Timeouts: 100, LogBytes: 100}}
	s := model.SystemStatus{UpstreamHealth: map[string]model.UpstreamHealth{"m": h}}
	c.RecordStatus(s, Resources{}, time.Now())
	h.Process = model.ProcessStatus{Timeouts: 3, LogBytes: 5, Exits: 1}
	s.UpstreamHealth["m"] = h
	c.RecordStatus(s, Resources{}, time.Now())
	for _, e := range c.Analysis().Events {
		if e.Kind == "timeout" {
			t.Fatal("new process totals attributed to old process")
		}
	}
	if counterDelta(3, 10) != 0 {
		t.Fatal("counter reset underflow")
	}
}

func TestPIDChangesAndMissingTrafficDoNotInventDeltas(t *testing.T) {
	c := &Collector{}
	c.Set(true)
	now := time.Now()
	h := model.UpstreamHealth{TunName: "tun1", Process: model.ProcessStatus{Timeouts: 2, LogBytes: 10, Exits: 1}}
	s := model.SystemStatus{UpstreamHealth: map[string]model.UpstreamHealth{"m": h}, SingboxPIDs: map[string]int{"tun1": 100}}
	c.RecordStatus(s, Resources{}, now)
	h.Process.Timeouts = 50
	h.Process.LogBytes = 100
	s.UpstreamHealth["m"] = h
	s.SingboxPIDs["tun1"] = 200
	s.TunTraffic = []model.TunTraffic{{TunName: "tun1", RxDropped: 100, RxErrors: 100}}
	c.RecordStatus(s, Resources{}, now.Add(time.Second))
	for _, e := range c.Analysis().Events {
		if e.Kind == "timeout" || e.Kind == "drops" || e.Kind == "interface-error" {
			t.Fatal("new process or missing traffic caused false delta", e)
		}
	}
	s.TunTraffic[0].RxDropped++
	c.RecordStatus(s, Resources{}, now.Add(2*time.Second))
	found := false
	for _, e := range c.Analysis().Events {
		if e.Kind == "drops" {
			found = true
		}
	}
	if !found {
		t.Fatal("valid traffic increment lost")
	}
}

func TestUnknownDiskSampleIsNotReportedAsFull(t *testing.T) {
	c := &Collector{}
	c.Set(true)
	c.RecordStatus(model.SystemStatus{}, Resources{}, time.Now())
	for _, finding := range c.Analysis().Findings {
		if strings.Contains(finding, "磁盘") {
			t.Fatal("unknown disk treated as full")
		}
	}
	c.RecordStatus(model.SystemStatus{}, Resources{DiskSampled: true}, time.Now().Add(time.Second))
	found := false
	for _, finding := range c.Analysis().Findings {
		if strings.Contains(finding, "磁盘") {
			found = true
		}
	}
	if !found {
		t.Fatal("full sampled disk was not reported")
	}
}
