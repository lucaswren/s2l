package api

import (
	"bytes"
	"encoding/json"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/lucaswren/s2l/internal/model"
	"github.com/lucaswren/s2l/internal/store"
)

func TestWANMappingPoliciesAcrossSaveImportAndRestore(t *testing.T) {
	for _, policy := range []string{"", "allow", "reject", "reject-quic"} {
		t.Run("policy="+policy, func(t *testing.T) {
			st, err := store.New(filepath.Join(t.TempDir(), "state.json"))
			if err != nil {
				t.Fatal(err)
			}
			n := model.SocksNode{ID: "wan", Egress: "wan", WANInterface: "eth0"}
			if err := st.UpsertNode(n); err != nil {
				t.Fatal(err)
			}
			m := model.TunnelMapping{ID: "m", NodeID: n.ID, TunName: "tun101", TableID: 101, Subnet: "10.0.101.0/24", L2TPUser: "u", L2TPPass: "p", UDPPolicy: policy, DNSMode: "tcp", DNSTCPAddress: "9.9.9.9:53"}
			expected := policy
			if expected == "" {
				expected = "allow"
			}
			check := func(label string, got model.TunnelMapping) {
				t.Helper()
				if got.UDPPolicy != expected || got.DNSMode != "tcp" || got.DNSTCPAddress != "9.9.9.9:53" {
					t.Fatalf("%s lost WAN settings: %+v", label, got)
				}
			}
			body, _ := json.Marshal(m)
			w := httptest.NewRecorder()
			req := httptest.NewRequest("POST", "/api/mappings", bytes.NewReader(body))
			req.Header.Set("Content-Type", "application/json")
			NewServer(st, nil, nil).Handler().ServeHTTP(w, req)
			if w.Code != 200 {
				t.Fatal(w.Code, w.Body.String())
			}
			got, _ := st.GetMapping(m.ID)
			check("save", got)
			res := importMappings(st, importMappingsReq{Mappings: []model.TunnelMapping{m}})
			if res.Updated != 1 || res.Skipped != 0 {
				t.Fatalf("import: %+v", res)
			}
			got, _ = st.GetMapping(m.ID)
			check("import", got)
			target := store.RestoreTarget(model.AppState{Nodes: []model.SocksNode{n}, Mappings: []model.TunnelMapping{m}})
			if policy == "" {
				expected = "reject"
				check("restore legacy default", target.Mappings[0])
				expected = "allow"
			} else {
				check("restore", target.Mappings[0])
			}
			// An explicit existing restriction must survive an ordinary edit.
			body, _ = json.Marshal(got)
			w = httptest.NewRecorder()
			req = httptest.NewRequest("POST", "/api/mappings", bytes.NewReader(body))
			req.Header.Set("Content-Type", "application/json")
			NewServer(st, nil, nil).Handler().ServeHTTP(w, req)
			if w.Code != 200 {
				t.Fatal(w.Code, w.Body.String())
			}
			got, _ = st.GetMapping(m.ID)
			check("edit", got)
			if err := st.UpdateMappingStatus(m.ID, model.StatusRunning, ""); err != nil {
				t.Fatal(err)
			}
			changed := got
			changed.UDPPolicy = "allow"
			if expected == "allow" {
				changed.UDPPolicy = "reject"
			}
			body, _ = json.Marshal(changed)
			w = httptest.NewRecorder()
			req = httptest.NewRequest("POST", "/api/mappings", bytes.NewReader(body))
			req.Header.Set("Content-Type", "application/json")
			NewServer(st, nil, nil).Handler().ServeHTTP(w, req)
			if w.Code != 400 {
				t.Fatal("active WAN policy changed without stop", w.Code, w.Body.String())
			}
			got, _ = st.GetMapping(m.ID)
			check("rejected active edit", got)

		})
	}
}

func TestWANLegacyOmittedPolicyDoesNotOpenUDPOnEditOrImport(t *testing.T) {
	st, err := store.New(filepath.Join(t.TempDir(), "state.json"))
	if err != nil {
		t.Fatal(err)
	}
	n := model.SocksNode{ID: "wan", Egress: "wan", WANInterface: "eth0"}
	if err := st.UpsertNode(n); err != nil {
		t.Fatal(err)
	}
	legacy := model.TunnelMapping{ID: "m", NodeID: n.ID, TunName: "tun101", TableID: 101, Subnet: "10.0.101.0/24", L2TPUser: "u", L2TPPass: "p", Status: model.StatusStopped}
	if err := st.UpsertMapping(legacy); err != nil {
		t.Fatal(err)
	}
	body, _ := json.Marshal(legacy)
	w := httptest.NewRecorder()
	r := httptest.NewRequest("POST", "/api/mappings", bytes.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	NewServer(st, nil, nil).Handler().ServeHTTP(w, r)
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	got, _ := st.GetMapping(legacy.ID)
	if got.UDPPolicy != "reject" {
		t.Fatal("legacy edit opened UDP", got)
	}
	res := importMappings(st, importMappingsReq{Text: "101,wan,u,p"})
	if res.Updated != 1 || res.Skipped != 0 {
		t.Fatal(res)
	}
	got, _ = st.GetMapping(legacy.ID)
	if got.UDPPolicy != "reject" {
		t.Fatal("legacy import opened UDP", got)
	}
}
