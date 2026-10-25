package api

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/lucaswren/s2l/internal/model"
	"github.com/lucaswren/s2l/internal/store"
)

func TestStaticUpgradeAndSPAFallback(t *testing.T) {
	h := NewServer(nil, nil, fstest.MapFS{"index.html": {Data: []byte("entry")}, "assets/current.js": {Data: []byte("bundle")}}).Handler()
	for _, tc := range []struct {
		path string
		code int
		body string
	}{
		{"/", 200, "entry"}, {"/settings", 200, "entry"},
		{"/assets/current.js", 200, "bundle"}, {"/assets/old.js", 404, "404"},
		{"/favicon.ico", 404, "404"}, {"/api/unknown", 404, "404"},
	} {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest("GET", tc.path, nil))
		if w.Code != tc.code || !strings.Contains(w.Body.String(), tc.body) {
			t.Fatalf("%s: %d %s", tc.path, w.Code, w.Body.String())
		}
		if tc.code == 200 && w.Header().Get("Cache-Control") != "no-cache" {
			t.Fatal("entry/bundle cache is not revalidated")
		}
	}
}

func TestMissingMappingServiceDoesNotPanicOrDeleteRuntimeState(t *testing.T) {
	st, err := store.New(filepath.Join(t.TempDir(), "state.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := st.UpsertMapping(model.TunnelMapping{ID: "m", Status: model.StatusRunning}); err != nil {
		t.Fatal(err)
	}
	h := NewServer(st, nil, nil).Handler()
	for _, method := range []string{"POST", "DELETE"} {
		path := "/api/mappings/m"
		if method == "POST" {
			path += "/action"
		}
		r := httptest.NewRequest(method, path, strings.NewReader(`{"action":"start"}`))
		r.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != http.StatusServiceUnavailable {
			t.Fatalf("%s: %d", method, w.Code)
		}
	}
	if _, ok := st.GetMapping("m"); !ok {
		t.Fatal("deleted active mapping without cleanup")
	}
}

func TestTextImportPreservesNetworkTuning(t *testing.T) {
	st, err := store.New(filepath.Join(t.TempDir(), "state.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := st.UpsertNode(model.SocksNode{ID: "n", Name: "node", Addr: "127.0.0.1:1080"}); err != nil {
		t.Fatal(err)
	}
	m := model.TunnelMapping{ID: "m", NodeID: "n", TableID: 101, TunName: "tun101", Subnet: "10.0.101.0/24", L2TPUser: "user", L2TPPass: "old", Status: model.StatusStopped, MTU: 1280, MSS: 1100, QueueLength: 500, UDPPolicy: "reject", DNSMode: "tcp", ObserveConnections: true, UploadKbps: 1000, DownloadKbps: 2000, MaxConnections: 50, MaxSessions: 2}
	m.DNSDoHURL, m.DNSTCPAddress = "https://dns.example/dns-query", "dns.example:5353"
	if err := st.UpsertMapping(m); err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequest("POST", "/api/mappings/import", strings.NewReader(`{"text":"101,node,user,new-password"}`))
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	NewServer(st, nil, nil).Handler().ServeHTTP(w, r)
	got, _ := st.GetMapping("m")
	if got.EffectiveDNSOptions() != m.EffectiveDNSOptions() {
		t.Fatal("text import replaced custom DNS", got.EffectiveDNSOptions())
	}
	if w.Code != 200 || got.L2TPPass != "new-password" || got.MTU != m.MTU || got.MSS != m.MSS || got.QueueLength != m.QueueLength || got.UDPPolicy != m.UDPPolicy || got.DNSMode != m.DNSMode || got.ObserveConnections != m.ObserveConnections || got.UploadKbps != m.UploadKbps || got.DownloadKbps != m.DownloadKbps || got.MaxConnections != m.MaxConnections || got.MaxSessions != m.MaxSessions {
		t.Fatalf("import lost tuning: %+v, %s", got, w.Body.String())
	}
}
