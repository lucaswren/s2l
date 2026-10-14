package l2tp

import (
	"errors"
	"os"
	"path/filepath"
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
