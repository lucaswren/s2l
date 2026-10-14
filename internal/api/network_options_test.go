package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/lucaswren/s2l/internal/model"
	"github.com/lucaswren/s2l/internal/service"
	"github.com/lucaswren/s2l/internal/store"
)

func TestMappingNetworkOptionsRoundTripAndRunningProtection(t *testing.T) {
	st, err := store.New(filepath.Join(t.TempDir(), "state.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := st.UpsertNode(model.SocksNode{ID: "n", Addr: "127.0.0.1:1080"}); err != nil {
		t.Fatal(err)
	}
	m := model.TunnelMapping{ID: "m", NodeID: "n", TunName: "tun101", TableID: 101, Subnet: "10.0.101.0/24", L2TPUser: "test-user", L2TPPass: "test-password", MTU: 1280, MSS: 1100, QueueLength: 500, UDPPolicy: "allow", DNSMode: "tcp"}
	handler := NewServer(st, nil, nil).Handler()
	post := func(m model.TunnelMapping) int {
		body, _ := json.Marshal(m)
		r := httptest.NewRequest(http.MethodPost, "/api/mappings", bytes.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		return w.Code
	}
	if code := post(m); code != http.StatusOK {
		t.Fatal(code)
	}
	saved, ok := st.GetMapping("m")
	if !ok || saved.MTU != 1280 || saved.MSS != 1100 || saved.QueueLength != 500 || saved.UDPPolicy != "allow" || saved.DNSMode != "tcp" {
		t.Fatal("network options lost during persistence", saved)
	}
	if err := st.UpdateMappingStatus("m", model.StatusRunning, ""); err != nil {
		t.Fatal(err)
	}
	changes := []model.TunnelMapping{m, m, m, m, m}
	changes[0].MTU = 1300
	changes[1].MSS = 1150
	changes[2].QueueLength = 600
	changes[3].UDPPolicy = "reject-quic"
	changes[4].DNSMode = "auto"
	for _, change := range changes {
		if code := post(change); code != http.StatusBadRequest {
			t.Fatalf("running mapping accepted options change: %+v, code=%d", change, code)
		}
	}
}

func TestDiagnosticTrailingSlashDoesNotAcquireMutationLock(t *testing.T) {
	st, err := store.New(filepath.Join(t.TempDir(), "state.json"))
	if err != nil {
		t.Fatal(err)
	}
	s := NewServer(st, service.NewMappingService(st, nil, nil, nil), nil)
	handler := s.Handler()
	s.mutationMu.Lock()
	defer s.mutationMu.Unlock()
	done := make(chan int, 1)
	go func() {
		r := httptest.NewRequest(http.MethodPost, "/api/mappings/m/diagnose/", bytes.NewBufferString(`{"url":"https://example.com/image"}`))
		r.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		done <- w.Code
	}()
	select {
	case code := <-done:
		if code != http.StatusBadRequest {
			t.Fatal(code)
		}
	case <-time.After(time.Second):
		t.Fatal("diagnostic acquired global mutation lock")
	}
}
