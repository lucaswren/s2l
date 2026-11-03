package api

import (
	"encoding/json"
	"errors"
	"github.com/lucaswren/s2l/internal/model"
	"github.com/lucaswren/s2l/internal/store"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestManagementPortsAndTransactionalConfiguration(t *testing.T) {
	ports := parseListeningPorts("LISTEN 0 128 0.0.0.0:51234 0.0.0.0:*\nLISTEN 0 128 [::]:38866 [::]:*\nnoise\n")
	if !ports[51234] || !ports[38866] || len(ports) != 2 {
		t.Fatal(ports)
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	initial := `{"listen":"127.0.0.1:8080","admin_user":"admin","admin_pass":"test-password-long","public_url":"https://example.com:38866"}`
	os.WriteFile(path, []byte(initial), 0600)
	st, e := store.New(filepath.Join(dir, "state.json"))
	if e != nil {
		t.Fatal(e)
	}
	s := NewServer(st, nil, nil, "admin", "test-password-long")
	s.ConfigureSettings(path)
	body := `{"enabled":true,"destination_ip":"156.245.235.230","source_cidrs":["10.0.101.0/24"],"tcp_ports":[38866,51234]}`
	post := func(body string) *httptest.ResponseRecorder {
		r := httptest.NewRequest("POST", "/api/settings/ppp-management", strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		s.handlePPPManagement(w, r)
		return w
	}
	called := 0
	s.applyManagement = func(a model.ManagementAccess) error { called++; return errors.New("kernel failure") }
	if w := post(body); w.Code != 409 {
		t.Fatal(w.Code, w.Body.String())
	}
	got, _ := os.ReadFile(path)
	if string(got) != initial {
		t.Fatal("failed apply persisted config")
	}
	s.applyManagement = func(a model.ManagementAccess) error { called++; return nil }
	if w := post(strings.Replace(body, "10.0.101.0/24", "0.0.0.0/0", 1)); w.Code != 400 {
		t.Fatal(w.Code)
	}
	if called != 1 {
		t.Fatal("invalid config applied")
	}
	if w := post(body); w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	got, _ = os.ReadFile(path)
	var fields map[string]json.RawMessage
	json.Unmarshal(got, &fields)
	if string(fields["public_url"]) != `"https://example.com:38866"` || !strings.Contains(string(fields["ppp_management"]), "51234") {
		t.Fatal(string(got))
	}
}
