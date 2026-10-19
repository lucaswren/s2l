package store

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/lucaswren/s2l/internal/model"
)

func legacyPolicyStore(t *testing.T) *Store {
	t.Helper()
	s, err := New(filepath.Join(t.TempDir(), "state.json"))
	if err != nil {
		t.Fatal(err)
	}
	s.state = model.AppState{
		Nodes: []model.SocksNode{{ID: "n", Addr: "127.0.0.1:1080", Password: "secret"}},
		Mappings: []model.TunnelMapping{
			{ID: "a", NodeID: "n", TunName: "tun101", TableID: 101, Subnet: "10.0.101.0/24", L2TPUser: "a", L2TPPass: "secret-a", Status: model.StatusRunning, UDPPolicy: "allow", DNSMode: "tcp", MTU: 1400, MSS: 1200},
			{ID: "b", NodeID: "n", TunName: "tun102", TableID: 102, Subnet: "10.0.102.0/24", L2TPUser: "b", L2TPPass: "secret-b", Status: model.StatusStopped, UDPPolicy: "reject-quic", DNSMode: "doh"},
		},
	}
	if err := s.saveLocked(); err != nil {
		t.Fatal(err)
	}
	return s
}

func TestTCPOnlyPolicyMigrationPersistsAndRunsOnce(t *testing.T) {
	s := legacyPolicyStore(t)
	before := s.Snapshot()
	count, err := s.MigrateTCPOnlyPolicy()
	if err != nil || count != 2 {
		t.Fatal(count, err)
	}
	want := cloneState(before)
	want.NetworkPolicyVersion = tcpOnlyPolicyVersion
	for i := range want.Mappings {
		want.Mappings[i].UDPPolicy = "reject"
		want.Mappings[i].DNSMode = "auto"
	}
	if !reflect.DeepEqual(s.Snapshot(), want) {
		t.Fatal("migration changed unrelated mapping settings or running state")
	}
	backups, err := s.ListBackups()
	if err != nil || len(backups) != 1 {
		t.Fatal(backups, err)
	}
	backup, err := s.ReadBackup(backups[0].ID)
	if err != nil || !reflect.DeepEqual(backup.State, stoppedState(before)) {
		t.Fatal("missing pre-migration backup", err)
	}
	reloaded, err := New(s.path)
	if err != nil || !reflect.DeepEqual(reloaded.Snapshot(), want) {
		t.Fatal("migration not persisted", err)
	}
	// Changes made after the upgrade must survive future service restarts.
	m, _ := reloaded.GetMapping("b")
	m.UDPPolicy = "allow"
	m.DNSMode = "tcp"
	if err := reloaded.UpsertMapping(m); err != nil {
		t.Fatal(err)
	}
	again, err := New(s.path)
	if err != nil {
		t.Fatal(err)
	}
	if count, err := again.MigrateTCPOnlyPolicy(); err != nil || count != 0 {
		t.Fatal(count, err)
	}
	if got, _ := again.GetMapping("b"); got != m {
		t.Fatal("migration repeated on next restart")
	}
}

func TestTCPOnlyPolicyMigrationFailedCommitCanRetry(t *testing.T) {
	s := legacyPolicyStore(t)
	before := s.Snapshot()
	if err := os.Remove(s.path); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(s.path, 0700); err != nil {
		t.Fatal(err)
	}
	if _, err := s.MigrateTCPOnlyPolicy(); err == nil {
		t.Fatal("migration unexpectedly committed")
	}
	if !reflect.DeepEqual(s.Snapshot(), before) {
		t.Fatal("failed migration retained partial state")
	}
	if err := os.Remove(s.path); err != nil {
		t.Fatal(err)
	}
	if _, err := s.MigrateTCPOnlyPolicy(); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(s.path)
	if err != nil {
		t.Fatal(err)
	}
	var state model.AppState
	if err := json.Unmarshal(data, &state); err != nil || state.NetworkPolicyVersion != tcpOnlyPolicyVersion {
		t.Fatal(err)
	}
}

func TestRestoringOldBackupKeepsInstalledMigrationVersion(t *testing.T) {
	s := legacyPolicyStore(t)
	old := s.Snapshot()
	if _, err := s.MigrateTCPOnlyPolicy(); err != nil {
		t.Fatal(err)
	}
	for _, m := range s.ListMappings() {
		if err := s.UpdateMappingStatus(m.ID, model.StatusStopped, ""); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.Restore(old, Revision(s.Snapshot())); err != nil {
		t.Fatal(err)
	}
	restored := s.Snapshot()
	if restored.NetworkPolicyVersion != tcpOnlyPolicyVersion {
		t.Fatal("migration marker rolled back")
	}
	if n, err := s.MigrateTCPOnlyPolicy(); err != nil || n != 0 {
		t.Fatal(n, err)
	}
	if !reflect.DeepEqual(s.ListMappings(), stoppedState(old).Mappings) {
		t.Fatal("restored policies overwritten")
	}
}
