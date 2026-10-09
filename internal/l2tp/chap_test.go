package l2tp

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lucaswren/s2l/internal/model"
)

func TestChapUpsertAndRemove(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "chap-secrets")
	if err := os.WriteFile(path, []byte("# header\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	c := NewChapManager(path)
	m := model.TunnelMapping{
		ID:       "map_abc",
		Subnet:   "10.0.101.0/24",
		L2TPUser: "user101",
		L2TPPass: "pass101",
	}
	if err := c.Upsert(m); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(path)
	s := string(data)
	if !strings.Contains(s, "# s2l:map_abc") {
		t.Fatalf("missing marker: %s", s)
	}
	if !strings.Contains(s, `"user101" * "pass101" 10.0.101.2`) {
		t.Fatalf("missing account line: %s", s)
	}

	if err := c.Remove("map_abc"); err != nil {
		t.Fatal(err)
	}
	data, _ = os.ReadFile(path)
	if strings.Contains(string(data), "user101") {
		t.Fatalf("account should be removed: %s", data)
	}
}

func TestClientAndLocalIP(t *testing.T) {
	local, err := LocalIPFromSubnet("10.0.101.0/24")
	if err != nil || local != "10.0.101.1" {
		t.Fatalf("local %q %v", local, err)
	}
	client, err := ClientIPFromSubnet("10.0.101.0/24")
	if err != nil || client != "10.0.101.2" {
		t.Fatalf("client %q %v", client, err)
	}
}

func TestConfSyncUsesSemicolonComments(t *testing.T) {
	dir := t.TempDir()
	confPath := filepath.Join(dir, "xl2tpd.conf")
	optPath := filepath.Join(dir, "options.xl2tpd")

	cm := NewConfManager(confPath, optPath)
	active := []model.TunnelMapping{{
		ID:       "map_1",
		TunName:  "tun101",
		TableID:  101,
		Subnet:   "10.0.101.0/24",
		L2TPUser: "user101",
	}}
	if err := cm.Sync(active); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(confPath)
	s := string(data)

	if strings.Contains(s, "# BEGIN") || strings.Contains(s, "# map") {
		t.Fatalf("xl2tpd.conf must not use # comments: %s", s)
	}
	if !strings.Contains(s, confBegin) || !strings.Contains(s, "[lns default]") {
		t.Fatalf("missing managed block: %s", s)
	}
	if !strings.Contains(s, "name = s2l") {
		t.Fatalf("missing lns name: %s", s)
	}
	if !strings.Contains(s, "; map map_1") || !strings.Contains(s, "peer=10.0.101.2") {
		t.Fatalf("missing map comment: %s", s)
	}
	if strings.Contains(s, "listen-addr") {
		t.Fatalf("listen-addr is not portable for xl2tpd: %s", s)
	}

	if err := cm.Sync(nil); err != nil {
		t.Fatal(err)
	}
	data, _ = os.ReadFile(confPath)
	s = string(data)
	if strings.Contains(s, "peer=10.0.101.2") {
		t.Fatalf("map comment should be cleared: %s", s)
	}
}
