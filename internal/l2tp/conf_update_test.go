package l2tp

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lucaswren/s2l/internal/model"
)

func TestRefusedStructuralChangePreservesBothFiles(t *testing.T) {
	dir := t.TempDir()
	conf, options := filepath.Join(dir, "xl2tpd.conf"), filepath.Join(dir, "options")
	oldConf, oldOptions := []byte("[global]\nport = 9999\n"), []byte("mtu 1300\n")
	if err := os.WriteFile(conf, oldConf, 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(options, oldOptions, 0644); err != nil {
		t.Fatal(err)
	}
	c := NewConfManager(conf, options)
	c.beforeChange = func() error { return errors.New("active daemon cannot reload") }
	for i := 0; i < 2; i++ {
		if c.Sync(nil) == nil {
			t.Fatal("refused change succeeded")
		}
		actualConf, _ := os.ReadFile(conf)
		actualOptions, _ := os.ReadFile(options)
		if string(actualConf) != string(oldConf) || string(actualOptions) != string(oldOptions) {
			t.Fatal("rejected update overwrote live configuration")
		}
	}
}

func TestDNSIsOfferedToNewClientsWithoutRestartingDaemon(t *testing.T) {
	dir := t.TempDir()
	options := filepath.Join(dir, "options")
	c := NewConfManager(filepath.Join(dir, "xl2tpd.conf"), options)
	if err := c.Sync(nil); err != nil {
		t.Fatal(err)
	}
	// Simulate an upgrade from the old options file while xl2tpd is active.
	if err := os.WriteFile(options, []byte("mtu 1400\n"), 0600); err != nil {
		t.Fatal(err)
	}
	c.beforeChange = func() error { t.Fatal("DNS offer must not restart xl2tpd"); return nil }
	if err := c.Sync(nil); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(options)
	if err != nil {
		t.Fatal(err)
	}
	installer, err := os.ReadFile("../../script/install.sh")
	if err != nil {
		t.Fatal(err)
	}
	const dnsOffer = "ms-dns 8.8.8.8\nms-dns 1.1.1.1\n"
	if strings.Count(string(data), dnsOffer) != 1 ||
		strings.Count(strings.ReplaceAll(string(installer), "\r\n", "\n"), dnsOffer) != 1 {
		t.Fatal("runtime and fresh installer must offer 8.8.8.8 before 1.1.1.1")
	}
	if c.Changed() || !strings.Contains(string(data), "nodefaultroute\n") || !strings.Contains(string(data), "require-mschap-v2\n") {
		t.Fatal("DNS offer changed daemon structure, routing or authentication")
	}
}

func TestCredentialCommentUpdatesNeverRequireDaemonReload(t *testing.T) {
	dir := t.TempDir()
	c := NewConfManager(filepath.Join(dir, "xl2tpd.conf"), filepath.Join(dir, "options"))
	if err := c.Sync(nil); err != nil {
		t.Fatal(err)
	}
	c.beforeChange = func() error { t.Fatal("comment update treated as structural change"); return nil }
	m := model.TunnelMapping{ID: "m1", TunName: "tun101", TableID: 101, Subnet: "10.0.101.0/24", L2TPUser: "user"}
	if err := c.Sync([]model.TunnelMapping{m}); err != nil {
		t.Fatal(err)
	}
	if c.Changed() {
		t.Fatal("account comments require reload")
	}
	if err := c.Sync(nil); err != nil {
		t.Fatal(err)
	}
}
