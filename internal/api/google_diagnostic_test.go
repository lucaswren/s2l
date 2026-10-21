package api

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lucaswren/s2l/internal/model"
	"github.com/lucaswren/s2l/internal/store"
)

func TestGoogleComparisonValidationAndCancelledReadOnlyReports(t *testing.T) {
	st, e := store.New(filepath.Join(t.TempDir(), "config.json"))
	if e != nil {
		t.Fatal(e)
	}
	for _, id := range []string{"a", "b"} {
		if e := st.UpsertNode(model.SocksNode{ID: id, Name: id, Addr: "127.0.0.1:1", Password: "must-not-leak"}); e != nil {
			t.Fatal(e)
		}
	}
	s := NewServer(st, nil, nil)
	if err := st.UpsertMapping(model.TunnelMapping{ID: "m", NodeID: "a", Status: model.StatusStopped}); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"a", "missing"} {
		w := httptest.NewRecorder()
		r := httptest.NewRequest("POST", "/", strings.NewReader(`{"kind":"google","compare_node":"`+id+`"}`))
		s.handleNodeNetworkTest(w, r, "a")
		if w.Code != 400 {
			t.Fatal(w.Code, w.Body.String())
		}
	}
	before := st.ListNodes()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	w := httptest.NewRecorder()
	r := httptest.NewRequest("POST", "/", strings.NewReader(`{"kind":"google","compare_node":"b"}`)).WithContext(ctx)
	s.handleNodeNetworkTest(w, r, "a")
	if w.Code != 200 || strings.Contains(w.Body.String(), "must-not-leak") {
		t.Fatal(w.Code, w.Body.String())
	}
	var result struct {
		OK         bool   `json:"ok"`
		ClientPath string `json:"client_path"`
		Policies   []struct {
			DNS struct {
				State string `json:"state"`
			} `json:"mapping_dns"`
		} `json:"mapping_policies"`
		Schema int `json:"schema_version"`
		Google []struct {
			NodeID  string `json:"node_id"`
			Targets []struct {
				TCP string `json:"tcp"`
			} `json:"targets"`
		} `json:"google"`
	}
	if json.Unmarshal(w.Body.Bytes(), &result) != nil || result.Schema != 1 || len(result.Google) != 2 || result.Google[0].NodeID != "a" || result.Google[1].NodeID != "b" {
		t.Fatal(w.Body.String())
	}
	if result.OK || result.ClientPath != "unverified" || len(result.Policies) != 1 || result.Policies[0].DNS.State != "unavailable" {
		t.Fatal(w.Body.String())
	}
	for _, report := range result.Google {
		if len(report.Targets) != 8 {
			t.Fatal(report)
		}
		for _, row := range report.Targets {
			if row.TCP != "not_tested" {
				t.Fatal(row)
			}
		}
	}
	after := st.ListNodes()
	if len(before) != len(after) || before[0] != after[0] || before[1] != after[1] {
		t.Fatal("diagnostic modified nodes")
	}
}
