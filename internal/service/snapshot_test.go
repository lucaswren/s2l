package service

import (
	"github.com/lucaswren/s2l/internal/model"
	"github.com/lucaswren/s2l/internal/store"
	"path/filepath"
	"testing"
	"time"
)

func TestSnapshotSharesRuntimeButReadsCurrentConfiguration(t *testing.T) {
	st, e := store.New(filepath.Join(t.TempDir(), "state.json"))
	if e != nil {
		t.Fatal(e)
	}
	m := model.TunnelMapping{ID: "m", Status: "stopped"}
	if e = st.UpsertMapping(m); e != nil {
		t.Fatal(e)
	}
	stamp := time.Now()
	s := &MappingService{store: st, snapshotAt: stamp, snapshot: model.SystemStatus{SingboxPIDs: map[string]int{"tun101": 42}}}
	if e = st.UpdateMappingStatus("m", "running", ""); e != nil {
		t.Fatal(e)
	}
	v := s.CachedStatus()
	if v.Mappings[0].Status != "running" || v.SingboxPIDs["tun101"] != 42 || !v.CollectedAt.Equal(stamp) {
		t.Fatal(v)
	}
}
