package api

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"io"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/lucaswren/s2l/internal/evidence"
	"github.com/lucaswren/s2l/internal/model"
	"github.com/lucaswren/s2l/internal/service"
	"github.com/lucaswren/s2l/internal/singbox"
	"github.com/lucaswren/s2l/internal/store"
)

func TestEvidenceAuthenticationPersistenceAndCredentialFreeExport(t *testing.T) {
	root := t.TempDir()
	st, err := store.New(filepath.Join(root, "state.json"))
	if err != nil {
		t.Fatal(err)
	}
	original := model.TunnelMapping{ID: "m", TunName: "tun101", TableID: 101, Subnet: "10.0.101.0/24", Status: model.StatusStopped, L2TPPass: "ppp-private"}
	if err = st.UpsertMapping(original); err != nil {
		t.Fatal(err)
	}
	svc := service.NewMappingService(st, singbox.NewManager(""), nil, nil)
	h := NewServer(st, svc, nil, "admin", "admin-private").Handler()
	for _, path := range []string{"/api/evidence", "/api/evidence/export?id=x"} {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest("GET", path, nil))
		if w.Code != 401 {
			t.Fatal("unauthenticated access", path, w.Code)
		}
	}
	cookie := testLogin(t, h, "admin", "admin-private")
	for _, tc := range []struct {
		method, path, body string
		code               int
	}{
		{"GET", "/api/evidence", "", 200},
		{"POST", "/api/evidence", `{"enabled":false}`, 200},
		{"POST", "/api/evidence", `{"mapping_id":"m"}`, 409},
		{"POST", "/api/evidence", `{"enabled":true,"mapping_id":"m"}`, 400},
		{"POST", "/api/evidence", `{}`, 400},
		{"DELETE", "/api/evidence", `{}`, 405},
		{"GET", "/api/evidence/export?id=../../state", "", 404},
	} {
		r := httptest.NewRequest(tc.method, tc.path, strings.NewReader(tc.body))
		r.AddCookie(cookie)
		r.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != tc.code {
			t.Fatal(tc, w.Code, w.Body)
		}
	}
	if svc.Evidence().View().Enabled {
		t.Fatal("disable not saved")
	}
	if saved, _ := st.GetMapping("m"); saved != original {
		t.Fatal("evidence changed mapping")
	}
	id := "20261102T040000.000000000Z"
	report := evidence.Report{Summary: evidence.Summary{ID: id, MappingID: "m", Started: time.Now(), Capture: "unavailable"}, Snapshots: map[string]string{"journal": "admin-private ppp-private"}}
	b, _ := json.Marshal(report)
	if err = os.WriteFile(filepath.Join(root, "evidence", id+".json"), b, 0600); err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequest("GET", "/api/evidence/export?id="+id, nil)
	r.AddCookie(cookie)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 200 || w.Header().Get("Content-Type") != "application/zip" {
		t.Fatal(w.Code, w.Body)
	}
	z, err := zip.NewReader(bytes.NewReader(w.Body.Bytes()), int64(w.Body.Len()))
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range z.File {
		reader, _ := f.Open()
		b, _ := io.ReadAll(reader)
		reader.Close()
		if bytes.Contains(b, []byte("private")) {
			t.Fatal("credential exported", string(b))
		}
	}
}
