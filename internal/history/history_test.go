package history

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/lucaswren/s2l/internal/model"
)

func sample(now time.Time) model.SystemStatus {
	return model.SystemStatus{Mappings: []model.TunnelMapping{{ID: "m", TunName: "tun101", Status: model.StatusRunning}}, UpstreamHealth: map[string]model.UpstreamHealth{"m": {TCP: model.ProtocolHealth{State: "healthy", CheckedAt: now, DurationMS: 20}, DNS: model.ProtocolHealth{State: "healthy", CheckedAt: now}, UDP: model.ProtocolHealth{State: "unsupported", CheckedAt: now}, LocalState: "healthy"}}, SingboxPIDs: map[string]int{"tun101": 10}, TunTraffic: []model.TunTraffic{{TunName: "tun101", RxBytes: 100, TxBytes: 100}}}
}
func TestHistoryDeduplicatesPersistsAndDetectsChanges(t *testing.T) {
	path := filepath.Join(t.TempDir(), "history.json")
	s := New(path)
	now := time.Unix(time.Now().Unix()/300*300+30, 0)
	status := sample(now)
	s.Record(status, now)
	s.Record(status, now.Add(5*time.Second))
	rows, _ := s.Snapshot("m")
	if len(rows) != 1 || rows[0].TCP.Pass != 1 || rows[0].UDP.Unsupported != 1 {
		t.Fatal(rows)
	}
	status.SingboxPIDs["tun101"] = 11
	status.Sessions = []model.PPPSession{{MappingID: "m", Interface: "ppp0", Index: 2, PeerIP: "10.0.101.2"}}
	s.Record(status, now.Add(10*time.Second))
	s.Flush()
	restored := New(path)
	rows, warning := restored.Snapshot("m")
	if warning != "" || len(rows) != 1 || rows[0].Restarts != 1 || rows[0].SessionChanges != 1 {
		t.Fatal(rows, warning)
	}
	s.Record(model.SystemStatus{}, now)
	rows, _ = s.Snapshot("m")
	if len(rows) != 0 {
		t.Fatal("deleted mapping retained")
	}
}
func TestHistoryUnknownAndCounterResetNeverFabricateTraffic(t *testing.T) {
	s := New("")
	now := time.Unix(time.Now().Unix()/300*300+30, 0)
	status := sample(now)
	s.Record(status, now)
	status.TunTraffic[0].RxBytes = 1
	status.TunTraffic[0].TxBytes = 1
	s.Record(status, now.Add(5*time.Second))
	rows, _ := s.Snapshot("m")
	if rows[0].RxPeakBPS != 0 || rows[0].TxPeakBPS != 0 {
		t.Fatal(rows)
	}
	if got := trim([]Bucket{{At: now.Add(-25 * time.Hour).Unix()}, {At: now.Add(time.Hour).Unix()}}, now); len(got) != 0 {
		t.Fatal(got)
	}
	path := filepath.Join(t.TempDir(), "broken.json")
	os.WriteFile(path, []byte(`null`), 0600)
	restored := New(path)
	restored.Record(status, now)
	if _, warning := restored.Snapshot("m"); warning == "" {
		t.Fatal("corrupt history loss was hidden")
	}
}

func TestHistoryRecordsMeasurementsAndSuccessfulRepairs(t *testing.T) {
	s := New("")
	now := time.Unix(time.Now().Unix()/300*300+30, 0)
	status := sample(now)
	h := status.UpstreamHealth["m"]
	h.TCP.State = "checking"
	h.TCP.MeasuredState = "healthy"
	h.DNS.State = "degraded"
	status.UpstreamHealth["m"] = h
	s.Record(status, now)
	h.RepairSuccesses = 2
	h.Process.Exits = 3
	status.UpstreamHealth["m"] = h
	s.Record(status, now.Add(5*time.Second))
	rows, _ := s.Snapshot("m")
	if rows[0].TCP.Pass != 1 || rows[0].LatencySamples != 1 || rows[0].DNS.Pass != 1 || rows[0].DNS.Fallback != 1 || rows[0].Repairs != 2 || rows[0].Restarts != 3 {
		t.Fatal(rows)
	}
	s.RecordDiagnostic("m", model.MappingDiagnostic{Forwarding: model.ProtocolHealth{State: "healthy", ExitIP: "203.0.113.1"}})
	s.RecordDiagnostic("m", model.MappingDiagnostic{Forwarding: model.ProtocolHealth{State: "healthy", ExitIP: "203.0.113.2"}})
	rows, _ = s.Snapshot("m")
	last := rows[len(rows)-1]
	if last.ForwardingPass != 2 || last.ExitChanges != 1 {
		t.Fatal(rows)
	}
}

func TestStoppedMappingStillExpiresHistory(t *testing.T) {
	s := New("")
	now := time.Now()
	status := sample(now)
	s.Record(status, now)
	status.Mappings[0].Status = model.StatusStopped
	s.Record(status, now.Add(25*time.Hour))
	if len(s.rows["m"]) != 0 {
		t.Fatal("stopped mapping retained expired on-disk buckets")
	}
}
