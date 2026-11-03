package api

import (
	"encoding/json"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLanguagePreferenceSurvivesRestartWithoutChangingCredentials(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	original := `{"listen":"127.0.0.1:8080","admin_user":"admin","admin_pass":"long-secret-password","custom":{"keep":true}}`
	if err := os.WriteFile(path, []byte(original), 0600); err != nil {
		t.Fatal(err)
	}
	s := NewServer(nil, nil, nil, "admin", "long-secret-password")
	s.ConfigureSettings(path)
	h := s.Handler()
	cookie := testLogin(t, h, "admin", "long-secret-password")
	if w := authRequest(h, "POST", "/api/settings/language", `{"language":"en"}`, nil); w.Code != 401 {
		t.Fatal(w.Code)
	}
	w := authRequest(h, "POST", "/api/settings/language", `{"language":"en"}`, cookie)
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var fields map[string]any
	if json.Unmarshal(data, &fields) != nil || fields["admin_user"] != "admin" || fields["admin_pass"] != "long-secret-password" || fields["custom"] == nil || fields["ui_language"] != "en" {
		t.Fatal("language write changed unrelated settings")
	}
	if status := authRequest(h, "GET", "/api/auth/status", "", cookie); !strings.Contains(status.Body.String(), `"authenticated":true`) {
		t.Fatal("language change revoked session")
	}
	restarted := NewServer(nil, nil, nil, "admin", "long-secret-password")
	restarted.ConfigureSettings(path)
	status := authRequest(restarted.Handler(), "GET", "/api/auth/status", "", nil)
	if !strings.Contains(status.Body.String(), `"language":"en"`) || strings.Contains(status.Body.String(), "long-secret-password") {
		t.Fatal(status.Body.String())
	}
	for _, language := range []string{"de", "en-US", "", "../config"} {
		payload, _ := json.Marshal(map[string]string{"language": language})
		if got := authRequest(h, "POST", "/api/settings/language", string(payload), cookie); got.Code != 400 {
			t.Fatal(language, got.Code)
		}
	}
	current, _ := os.ReadFile(path)
	if string(current) != string(data) {
		t.Fatal("invalid language changed config")
	}
	request := httptest.NewRequest("POST", "https://panel.example/api/settings/language", strings.NewReader(`{"language":"zh-TW"}`))
	request.AddCookie(cookie)
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Origin", "https://other.example")
	result := httptest.NewRecorder()
	h.ServeHTTP(result, request)
	if result.Code != 403 {
		t.Fatal(result.Code)
	}
	s.ConfigureSettings(t.TempDir())
	if got := authRequest(h, "POST", "/api/settings/language", `{"language":"zh-CN"}`, cookie); got.Code != 500 || s.uiLanguage != "en" {
		t.Fatal("failed persistence changed active preference")
	}
}
