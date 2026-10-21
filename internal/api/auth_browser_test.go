package api

import (
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/lucaswren/s2l/internal/store"
	"github.com/lucaswren/s2l/web"
)

// Explicit opt-in fixture: uses the real middleware and embedded frontend,
// never the host service, firewall, credentials or production data.
func TestAuthBrowserFixture(t *testing.T) {
	address := os.Getenv("S2L_AUTH_UI_FIXTURE")
	if address == "" {
		t.Skip("browser fixture requires explicit loopback listener")
	}
	if !strings.HasPrefix(address, "127.0.0.1:") {
		t.Fatal("loopback required")
	}
	assets, err := web.FS()
	if err != nil {
		t.Fatal(err)
	}
	st, err := store.New(filepath.Join(t.TempDir(), "state.json"))
	if err != nil {
		t.Fatal(err)
	}
	s := NewServer(st, nil, assets, "fixture-admin", "fixture-password-123")
	configPath := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(configPath, []byte(`{"listen":"127.0.0.1:8080","admin_user":"fixture-admin","admin_pass":"fixture-password-123"}`), 0600); err != nil {
		t.Fatal(err)
	}
	s.ConfigureSettings(configPath)
	listener, err := net.Listen("tcp4", address)
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	server := &http.Server{Handler: s.Handler(), ReadHeaderTimeout: 5 * time.Second}
	if err = server.Serve(listener); err != nil && err != http.ErrServerClosed {
		t.Fatal(err)
	}
}
