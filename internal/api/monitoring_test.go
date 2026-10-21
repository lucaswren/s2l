package api

import (
	"encoding/json"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lucaswren/s2l/internal/model"
	"github.com/lucaswren/s2l/internal/service"
	"github.com/lucaswren/s2l/internal/singbox"
	"github.com/lucaswren/s2l/internal/store"
)

func TestMonitoringEndpointsAreReadOnlyAndPolicyPreserving(t *testing.T) {
	st, err := store.New(filepath.Join(t.TempDir(), "state.json"))
	if err != nil {
		t.Fatal(err)
	}
	original := model.TunnelMapping{ID: "m", NodeID: "fixed", TunName: "tun101", TableID: 101, Subnet: "10.0.101.0/24", Status: model.StatusStopped, UDPPolicy: "reject", DNSMode: "auto"}
	if err := st.UpsertMapping(original); err != nil {
		t.Fatal(err)
	}
	svc := service.NewMappingService(st, singbox.NewManager(""), nil, nil)
	server := NewServer(st, svc, nil)
	for _, tc := range []struct {
		method, path string
		code         int
	}{{"GET", "/api/history?mapping_id=m", 200}, {"GET", "/api/history?mapping_id=missing", 404}, {"POST", "/api/history?mapping_id=m", 405}, {"POST", "/api/mappings/m/network-test", 409}, {"GET", "/api/mappings/m/network-test", 405}} {
		r := httptest.NewRequest(tc.method, tc.path, strings.NewReader(`{}`))
		r.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		server.Handler().ServeHTTP(w, r)
		if w.Code != tc.code {
			t.Fatalf("%s: %d %s", tc.path, w.Code, w.Body)
		}
	}
	saved, _ := st.GetMapping("m")
	if saved != original {
		t.Fatal("diagnostics changed fixed mapping", saved)
	}
	w := httptest.NewRecorder()
	server.Handler().ServeHTTP(w, httptest.NewRequest("GET", "/api/status", nil))
	var status map[string]json.RawMessage
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &status) != nil || status["sessions"] == nil || status["sessions_checked_at"] == nil {
		t.Fatal(w.Code, w.Body)
	}
}

func TestMetricsAndOperationsRequireAuthentication(t *testing.T) {
	st, err := store.New(filepath.Join(t.TempDir(), "state.json"))
	if err != nil {
		t.Fatal(err)
	}
	svc := service.NewMappingService(st, singbox.NewManager(""), nil, nil)
	h := NewServer(st, svc, nil, "admin", "private").Handler()
	for _, path := range []string{"/api/metrics", "/api/operations"} {
		r := httptest.NewRequest("GET", path, nil)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != 401 {
			t.Fatal(path, w.Code)
		}
		r = httptest.NewRequest("GET", path, nil)
		r.AddCookie(testLogin(t, h, "admin", "private"))
		w = httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != 200 || strings.Contains(w.Body.String(), "private") {
			t.Fatal(path, w.Code, w.Body)
		}
	}
}
