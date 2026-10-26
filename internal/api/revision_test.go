package api

import (
	"encoding/json"
	"github.com/lucaswren/s2l/internal/model"
	"github.com/lucaswren/s2l/internal/store"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
)

func TestPreviewAndStaleEditCannotChangeConfiguration(t *testing.T) {
	st, e := store.New(filepath.Join(t.TempDir(), "state.json"))
	if e != nil {
		t.Fatal(e)
	}
	n := model.SocksNode{ID: "n", Addr: "127.0.0.1:1080", Name: "old", Password: "private-secret"}
	if e = st.UpsertNode(n); e != nil {
		t.Fatal(e)
	}
	h := NewServer(st, nil, nil).Handler()
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("GET", "/api/nodes/n", nil))
	rev := w.Header().Get("ETag")
	if rev == "" {
		t.Fatal("missing revision")
	}
	n.Name = "new"
	b, _ := json.Marshal(n)
	send := func(path, revision string) *httptest.ResponseRecorder {
		r := httptest.NewRequest("POST", path, strings.NewReader(string(b)))
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("If-Match", revision)
		out := httptest.NewRecorder()
		h.ServeHTTP(out, r)
		return out
	}
	p := send("/api/nodes?preview=1", rev)
	if p.Code != 200 || strings.Contains(p.Body.String(), "private-secret") {
		t.Fatal(p.Code, p.Body)
	}
	old, _ := st.GetNode("n")
	if old.Name != "old" {
		t.Fatal("preview wrote configuration")
	}
	if out := send("/api/nodes", rev); out.Code != 200 {
		t.Fatal(out.Body)
	}
	n.Name = "overwrite"
	b, _ = json.Marshal(n)
	if out := send("/api/nodes", rev); out.Code != 409 {
		t.Fatal(out.Code, out.Body)
	}
	old, _ = st.GetNode("n")
	if old.Name != "new" {
		t.Fatal("stale edit won")
	}
}

func TestPreviewIncludesClearedOptionalFields(t *testing.T) {
	st, e := store.New(filepath.Join(t.TempDir(), "state.json"))
	if e != nil {
		t.Fatal(e)
	}
	server := NewServer(st, nil, nil)
	old := model.TunnelMapping{ID: "m", Group: "group", UploadKbps: 10, MaxConnections: 2, MaxSessions: 1}
	next := model.TunnelMapping{ID: "m"}
	w := httptest.NewRecorder()
	server.previewChange(w, httptest.NewRequest("POST", "/api/mappings?preview=1", nil), old, next, "")
	var result struct {
		Fields []string `json:"fields"`
	}
	if e = json.Unmarshal(w.Body.Bytes(), &result); e != nil {
		t.Fatal(e)
	}
	for _, key := range []string{"group", "upload_kbps", "max_connections", "max_sessions"} {
		found := false
		for _, field := range result.Fields {
			if field == key {
				found = true
			}
		}
		if !found {
			t.Fatal("missing cleared field", key, result)
		}
	}
}
func TestDeletedNodeCannotBeResurrectedByStaleEditor(t *testing.T) {
	st, e := store.New(filepath.Join(t.TempDir(), "state.json"))
	if e != nil {
		t.Fatal(e)
	}
	n := model.SocksNode{ID: "n", Addr: "127.0.0.1:1080"}
	if e = st.UpsertNode(n); e != nil {
		t.Fatal(e)
	}
	rev := `"` + resourceRevision(n) + `"`
	if e = st.DeleteNode("n"); e != nil {
		t.Fatal(e)
	}
	body, _ := json.Marshal(n)
	r := httptest.NewRequest("POST", "/api/nodes", strings.NewReader(string(body)))
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("If-Match", rev)
	w := httptest.NewRecorder()
	NewServer(st, nil, nil).Handler().ServeHTTP(w, r)
	if w.Code != 409 {
		t.Fatal(w.Code, w.Body)
	}
	if _, exists := st.GetNode("n"); exists {
		t.Fatal("deleted node resurrected")
	}
}

func TestNodePreviewBlocksConnectionChangesWithoutStoppingMappings(t *testing.T) {
	for _, status := range []string{model.StatusRunning, model.StatusError, model.StatusStopped} {
		t.Run(status, func(t *testing.T) {
			st, err := store.New(filepath.Join(t.TempDir(), "state.json"))
			if err != nil {
				t.Fatal(err)
			}
			node := model.SocksNode{ID: "n", Addr: "127.0.0.1:1080", Password: "original-secret"}
			if err := st.UpsertNode(node); err != nil {
				t.Fatal(err)
			}
			mapping := model.TunnelMapping{ID: "m", NodeID: "n", L2TPUser: "account", L2TPPass: "dial-secret", Status: status}
			if err := st.UpsertMapping(mapping); err != nil {
				t.Fatal(err)
			}
			h := NewServer(st, nil, nil).Handler()
			post := func(path string, next model.SocksNode) *httptest.ResponseRecorder {
				body, _ := json.Marshal(next)
				r := httptest.NewRequest("POST", path, strings.NewReader(string(body)))
				r.Header.Set("Content-Type", "application/json")
				w := httptest.NewRecorder()
				h.ServeHTTP(w, r)
				return w
			}
			next := node
			next.Password = "replacement-secret"
			preview := post("/api/nodes?preview=1", next)
			var result struct {
				Blocked []map[string]string `json:"blocked"`
			}
			if preview.Code != 200 || json.Unmarshal(preview.Body.Bytes(), &result) != nil {
				t.Fatal(preview.Code, preview.Body)
			}
			want := 0
			if status != model.StatusStopped {
				want = 1
			}
			if len(result.Blocked) != want || strings.Contains(preview.Body.String(), "secret") {
				t.Fatal(preview.Body)
			}
			if want == 1 && (result.Blocked[0]["id"] != "m" || result.Blocked[0]["name"] != "account") {
				t.Fatal(result)
			}
			saved, _ := st.GetNode("n")
			if saved != node {
				t.Fatal("preview changed node")
			}
			response := post("/api/nodes", next)
			code := 200
			if want == 1 {
				code = 409
			}
			if response.Code != code {
				t.Fatal(response.Code, response.Body)
			}
			savedMapping, _ := st.GetMapping("m")
			if savedMapping != mapping {
				t.Fatal("edit changed mapping lifecycle")
			}
			// Metadata edits and no-op saves must remain possible while in use.
			saved, _ = st.GetNode("n")
			saved.Name = "new name"
			saved.Group = "new group"
			preview = post("/api/nodes?preview=1", saved)
			if json.Unmarshal(preview.Body.Bytes(), &result) != nil || len(result.Blocked) != 0 {
				t.Fatal(preview.Body)
			}
			if response := post("/api/nodes", saved); response.Code != 200 {
				t.Fatal(response.Code, response.Body)
			}
		})
	}
}

func TestNodeStartedAfterPreviewStillRejectsConnectionChange(t *testing.T) {
	st, err := store.New(filepath.Join(t.TempDir(), "state.json"))
	if err != nil {
		t.Fatal(err)
	}
	node := model.SocksNode{ID: "n", Addr: "127.0.0.1:1080"}
	if err := st.UpsertNode(node); err != nil {
		t.Fatal(err)
	}
	if err := st.UpsertMapping(model.TunnelMapping{ID: "m", NodeID: "n", Status: model.StatusStopped}); err != nil {
		t.Fatal(err)
	}
	node.Addr = "127.0.0.2:1080"
	body, _ := json.Marshal(node)
	h := NewServer(st, nil, nil).Handler()
	for _, path := range []string{"/api/nodes?preview=1", "/api/nodes"} {
		w := httptest.NewRecorder()
		r := httptest.NewRequest("POST", path, strings.NewReader(string(body)))
		r.Header.Set("Content-Type", "application/json")
		h.ServeHTTP(w, r)
		if strings.Contains(path, "preview") {
			if w.Code != 200 {
				t.Fatal(w.Code, w.Body)
			}
			if err := st.UpdateMappingStatus("m", model.StatusRunning, ""); err != nil {
				t.Fatal(err)
			}
		} else if w.Code != 409 {
			t.Fatal(w.Code, w.Body)
		}
	}
	saved, _ := st.GetNode("n")
	if saved.Addr != "127.0.0.1:1080" {
		t.Fatal("changed active node")
	}
}
