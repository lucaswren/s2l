package api

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestWebUpdateRequiresAuthenticatedSameOriginJSON(t *testing.T) {
	s := NewServer(nil, nil, nil, "admin", "test-password-123")
	s.ConfigureSettings("/opt/s2l/config.json")
	for _, tc := range []struct {
		body, origin, contentType string
		auth                      bool
		want                      int
	}{
		{`{"action":"start"}`, "", "application/json", false, 401},
		{`{"action":"start"}`, "https://other.example", "application/json", true, 403},
		{`{"action":"start"}`, "", "text/plain", true, 415},
		{`{"action":"execute"}`, "", "application/json", true, 400},
	} {
		r := httptest.NewRequest("POST", "https://admin.example/api/settings/update", strings.NewReader(tc.body))
		r.Header.Set("Content-Type", tc.contentType)
		r.Header.Set("Origin", tc.origin)
		if tc.auth {
			r.SetBasicAuth("admin", "test-password-123")
		}
		w := httptest.NewRecorder()
		s.Handler().ServeHTTP(w, r)
		if w.Code != tc.want {
			t.Fatal(w.Code, tc.want, w.Body.String())
		}
	}
}

func TestUpdateBlocksMutationsButKeepsStatusAndDebugAvailable(t *testing.T) {
	path := filepath.Join(t.TempDir(), "job.json")
	s := NewServer(nil, nil, nil)
	s.ConfigureSettings("/opt/s2l/config.json")
	s.updateStatePath = path
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(204) })
	handler := s.withUpdateProtection(next)
	for _, state := range []struct {
		data string
		want int
	}{
		{`{"status":"queued"}`, 409}, {`{"status":"running"}`, 409},
		{`{"status":"succeeded"}`, 204}, {`{"status":"failed"}`, 204},
		{`broken-json`, 503}, {`null`, 503},
	} {
		if err := os.WriteFile(path, []byte(state.data), 0600); err != nil {
			t.Fatal(err)
		}
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, httptest.NewRequest("POST", "/api/mappings", nil))
		if w.Code != state.want {
			t.Fatal(state.data, w.Code)
		}
	}
	os.WriteFile(path, []byte(`{"status":"running"}`), 0600)
	for _, path := range []string{"/api/debug", "/api/settings/update"} {
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, httptest.NewRequest("POST", path, nil))
		if w.Code != 204 {
			t.Fatal(path, w.Code)
		}
	}
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, httptest.NewRequest("GET", "/api/status", nil))
	if w.Code != 204 {
		t.Fatal(w.Code)
	}
}

func TestUpdateWaitsForAdmittedMutationBeforeLaunching(t *testing.T) {
	s := NewServer(nil, nil, nil)
	s.ConfigureSettings("/opt/s2l/config.json")
	s.updateStatePath = filepath.Join(t.TempDir(), "job.json")
	entered, release, finished := make(chan struct{}), make(chan struct{}), make(chan struct{})
	handler := s.withUpdateProtection(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { close(entered); <-release; w.WriteHeader(204) }))
	go func() {
		defer close(finished)
		handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("POST", "/api/nodes", nil))
	}()
	<-entered
	acquired := make(chan struct{})
	go func() { s.updateGate.Lock(); s.updateGate.Unlock(); close(acquired) }()
	select {
	case <-acquired:
		close(release)
		t.Fatal("upgrade bypassed in-progress configuration write")
	case <-time.After(30 * time.Millisecond):
	}
	close(release)
	<-finished
	select {
	case <-acquired:
	case <-time.After(time.Second):
		t.Fatal("upgrade remained blocked after mutation completed")
	}
}
