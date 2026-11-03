package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/lucaswren/s2l/internal/model"
	"github.com/lucaswren/s2l/internal/store"
)

func TestMappingManagedOptionsAndRunningProtection(t *testing.T) {
	st, err := store.New(filepath.Join(t.TempDir(), "state.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := st.UpsertNode(model.SocksNode{ID: "n", Addr: "127.0.0.1:1080"}); err != nil {
		t.Fatal(err)
	}
	m := model.TunnelMapping{ID: "m", NodeID: "n", TunName: "tun101", TableID: 101, Subnet: "10.0.101.0/24", L2TPUser: "test-user", L2TPPass: "test-password", MTU: 1280, MSS: 1100, QueueLength: 500, UDPPolicy: "allow", DNSMode: "tcp"}
	m.DNSDoHURL, m.DNSTCPAddress = "https://resolver.example/dns-query", "dns.internal:5353"
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
	want := m.ManagedLineSettings()
	want.Status = model.StatusStopped
	if !ok || saved != want {
		t.Fatal("network options not normalized during persistence", saved)
	}
	for _, bad := range []model.DNSOptions{{DoHURL: "http://dns.example/query"}, {DoHURL: "https://dns.example/query#"}, {TCPAddress: "dns.example:0"}} {
		invalid := m
		invalid.DNSDoHURL, invalid.DNSTCPAddress = bad.DoHURL, bad.TCPAddress
		if code := post(invalid); code != http.StatusBadRequest {
			t.Fatalf("invalid DNS accepted: %+v, code=%d", bad, code)
		}
		got, _ := st.GetMapping("m")
		if got != saved {
			t.Fatal("invalid DNS changed persisted mapping", got)
		}
	}
	if err := st.UpdateMappingStatus("m", model.StatusRunning, ""); err != nil {
		t.Fatal(err)
	}
	// Legacy clients cannot re-enable retired options or restart a mapping.
	if code := post(m); code != http.StatusOK {
		t.Fatal("legacy settings should normalize to the running policy", code)
	}
	got, _ := st.GetMapping("m")
	want.Status = model.StatusRunning
	if got != want {
		t.Fatal("legacy update changed active network policy", got)
	}
	changes := []model.TunnelMapping{m, m, m, m}
	changes[0].UploadKbps = 1000
	changes[1].DownloadKbps = 2000
	changes[2].L2TPPass = "different-password"
	changes[3].ObserveConnections = !m.ObserveConnections
	for _, change := range changes {
		if code := post(change); code != http.StatusBadRequest {
			t.Fatalf("running mapping accepted options change: %+v, code=%d", change, code)
		}
	}
}

func TestErrorMappingCannotBeEditedOrImportedUntilStopped(t *testing.T) {
	st, err := store.New(filepath.Join(t.TempDir(), "state.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err = st.UpsertNode(model.SocksNode{ID: "n", Addr: "127.0.0.1:1080"}); err != nil {
		t.Fatal(err)
	}
	m := model.TunnelMapping{ID: "m", NodeID: "n", TunName: "tun101", TableID: 101, Subnet: "10.0.101.0/24", L2TPUser: "u", L2TPPass: "p", Status: model.StatusError, ErrorMsg: "cleanup failed"}
	if err = st.UpsertMapping(m); err != nil {
		t.Fatal(err)
	}
	handler := NewServer(st, nil, nil).Handler()
	change := m
	change.TunName = "tun102"
	for _, path := range []string{"/api/mappings", "/api/mappings/import"} {
		var body []byte
		if path == "/api/mappings" {
			body, _ = json.Marshal(change)
		} else {
			body, _ = json.Marshal(importMappingsReq{Mappings: []model.TunnelMapping{change}})
		}
		r := httptest.NewRequest("POST", path, bytes.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		if path == "/api/mappings" && w.Code != 400 {
			t.Fatal(w.Code, w.Body.String())
		}
		if path == "/api/mappings/import" {
			var result importResult
			json.Unmarshal(w.Body.Bytes(), &result)
			if result.Skipped != 1 || result.Updated != 0 {
				t.Fatal(w.Body.String())
			}
		}
		saved, _ := st.GetMapping("m")
		if saved != m {
			t.Fatal("lost failed cleanup configuration", saved)
		}
	}
}
