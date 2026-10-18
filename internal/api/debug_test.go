package api

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"github.com/lucaswren/s2l/internal/debuglog"
	"github.com/lucaswren/s2l/internal/model"
	"github.com/lucaswren/s2l/internal/store"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
)

func TestDebugExportRedactsAndAuthenticates(t *testing.T) {
	st, err := store.New(filepath.Join(t.TempDir(), "state.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := st.UpsertNode(model.SocksNode{ID: "n", Password: "secret-proxy-password"}); err != nil {
		t.Fatal(err)
	}
	s := NewServer(st, nil, nil, "admin", "secret-admin-password")
	h := s.Handler()
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("GET", "/api/debug/export", nil))
	if w.Code != 401 {
		t.Fatal(w.Code)
	}
	debuglog.Default.Set(true)
	defer debuglog.Default.Set(false)
	debuglog.Default.Write([]byte("secret-proxy-password secret-admin-password timeout"))
	r := httptest.NewRequest("GET", "/api/debug/export", nil)
	r.SetBasicAuth("admin", "secret-admin-password")
	w = httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 200 {
		t.Fatal(w.Code)
	}
	z, err := zip.NewReader(bytes.NewReader(w.Body.Bytes()), int64(w.Body.Len()))
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range z.File {
		rc, _ := f.Open()
		b, _ := io.ReadAll(rc)
		rc.Close()
		if bytes.Contains(b, []byte("secret-proxy-password")) || bytes.Contains(b, []byte("secret-admin-password")) {
			t.Fatal("credential leak")
		}
	}
}

func TestJSONRedactionPreservesSyntaxAndEscapedSecrets(t *testing.T) {
	secret := "quoted\"password\\with\nnewline<>&"
	input := map[string]any{"message": "error " + secret, "count": 12345, "nested": []any{"abc-long-password", "abc"}}
	data, _ := json.Marshal(input)
	out := redactJSON(data, secretCleaner([]string{"abc", "abc-long-password", secret, "123"}))
	encoded, _ := json.Marshal(secret)
	if bytes.Contains([]byte(secretCleaner([]string{secret})(string(data))), encoded[1:len(encoded)-1]) {
		t.Fatal("JSON-escaped secret leaked in raw health log")
	}
	var got map[string]any
	if err := json.Unmarshal(out, &got); err != nil {
		t.Fatal("export JSON invalid", err)
	}
	if got["count"] != float64(12345) {
		t.Fatal("numeric password changed metric type or value")
	}
	if got["message"] != "error [REDACTED]" || got["nested"].([]any)[0] != "[REDACTED]" {
		t.Fatal("escaped or overlapping password leaked")
	}
}

type blockedExportWriter struct {
	recorder *httptest.ResponseRecorder
	ready    chan<- struct{}
	release  <-chan struct{}
}

func (w blockedExportWriter) Header() http.Header  { return w.recorder.Header() }
func (w blockedExportWriter) WriteHeader(code int) { w.recorder.WriteHeader(code) }
func (w blockedExportWriter) Write(p []byte) (int, error) {
	w.ready <- struct{}{}
	<-w.release
	return w.recorder.Write(p)
}

func TestConcurrentExportLimitReleasesSlots(t *testing.T) {
	st, _ := store.New(filepath.Join(t.TempDir(), "state.json"))
	s := NewServer(st, nil, nil)
	ready, release, done := make(chan struct{}, 2), make(chan struct{}), make(chan struct{}, 2)
	for i := 0; i < 2; i++ {
		go func() {
			defer func() { done <- struct{}{} }()
			s.handleDebugExport(blockedExportWriter{httptest.NewRecorder(), ready, release}, httptest.NewRequest("GET", "/api/debug/export", nil))
		}()
	}
	<-ready
	<-ready
	w := httptest.NewRecorder()
	s.handleDebugExport(w, httptest.NewRequest("GET", "/api/debug/export", nil))
	if w.Code != 429 {
		close(release)
		<-done
		<-done
		t.Fatal("exports were unbounded", w.Code)
	}
	close(release)
	<-done
	<-done
	w = httptest.NewRecorder()
	s.handleDebugExport(w, httptest.NewRequest("GET", "/api/debug/export", nil))
	if w.Code != 200 {
		t.Fatal("export semaphore leaked", w.Code)
	}
}
