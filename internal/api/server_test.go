package api

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestAPIMutationProtection(t *testing.T) {
	s := &Server{adminUser: "admin", adminPass: "test-password-123"}
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusNoContent) })
	for _, tc := range []struct {
		name, method, origin, contentType string
		want                              int
	}{
		{"cross-site form", "POST", "https://attacker.example", "text/plain", 403},
		{"same-site form", "POST", "https://admin.example", "text/plain", 415},
		{"cross-site delete", "DELETE", "https://attacker.example", "", 403},
		{"opaque origin", "POST", "null", "application/json", 403},
		{"same-site JSON", "POST", "https://admin.example", "application/json; charset=utf-8", 204},
		{"CLI JSON", "POST", "", "application/json", 204},
		{"same-site delete", "DELETE", "https://admin.example", "", 204},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := httptest.NewRequest(tc.method, "https://admin.example/api/nodes", strings.NewReader(`{}`))
			r.SetBasicAuth("admin", "test-password-123")
			r.Header.Set("Origin", tc.origin)
			r.Header.Set("Content-Type", tc.contentType)
			w := httptest.NewRecorder()
			s.withAuth(next).ServeHTTP(w, r)
			if w.Code != tc.want {
				t.Fatalf("status = %d, want %d", w.Code, tc.want)
			}
			if w.Header().Get("Cache-Control") != "no-store" {
				t.Fatal("API response is cacheable")
			}
		})
	}
}
