package service

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/lucaswren/s2l/internal/model"
	"github.com/lucaswren/s2l/internal/singbox"
	"github.com/lucaswren/s2l/internal/speedtest"
	"github.com/lucaswren/s2l/internal/store"
)

func TestFailedProbeQueuesEvidenceWithoutRestartingOrChangingMapping(t *testing.T) {
	st, err := store.New(filepath.Join(t.TempDir(), "state.json"))
	if err != nil {
		t.Fatal(err)
	}
	n := model.SocksNode{ID: "n", Name: "fixed", Addr: "127.0.0.1:1080"}
	m := model.TunnelMapping{ID: "m", NodeID: "n", TunName: "tun101", TableID: 101, Subnet: "10.0.101.0/24", Status: model.StatusRunning}
	if err = st.UpsertNode(n); err != nil {
		t.Fatal(err)
	}
	if err = st.UpsertMapping(m); err != nil {
		t.Fatal(err)
	}
	sb := singbox.NewManager("")
	svc := NewMappingService(st, sb, nil, nil)
	h := &healthRecord{}
	svc.health[m.ID] = h
	svc.finishProbe(context.Background(), m, n, h, 0, speedtest.HealthReport{TCP: model.ProtocolHealth{State: "unavailable", CheckedAt: time.Now()}, DNS: model.ProtocolHealth{State: "healthy", CheckedAt: time.Now()}})
	if !svc.Evidence().View().Busy {
		t.Fatal("failed probe did not queue evidence")
	}
	if sb.PID(m.TunName) != 0 {
		t.Fatal("probe started a process")
	}
	if saved, _ := st.GetMapping(m.ID); saved != m {
		t.Fatal("probe changed mapping")
	}
	if svc.CaptureEvidence(m.ID) {
		t.Fatal("same-mapping capture ignored cooldown")
	}
}
