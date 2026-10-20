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
