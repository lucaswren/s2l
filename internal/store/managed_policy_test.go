package store

import (
	"os"
	"reflect"
	"testing"

	"github.com/lucaswren/s2l/internal/model"
)

func TestManagedPolicyMigrationAndRestoreRetainBandwidth(t *testing.T) {
	s := legacyPolicyStore(t)
	s.state.NetworkPolicyVersion = tcpOnlyPolicyVersion
	s.state.Mappings[0].UploadKbps, s.state.Mappings[0].DownloadKbps = 3000, 20000
	s.state.Mappings[0].MaxConnections = 5
	s.state.Mappings[0].MTU, s.state.Mappings[0].MSS, s.state.Mappings[0].QueueLength = 1300, 1100, 500
	s.state.Mappings[0].DNSDoHURL = "https://dns.example/dns-query"
	before := s.Snapshot()
	if err := s.saveLocked(); err != nil {
		t.Fatal(err)
	}
	if n, err := s.MigrateManagedLinePolicy(); err != nil || n != 2 {
		t.Fatal(n, err)
	}
	want := cloneState(before)
	want.NetworkPolicyVersion = managedLinePolicyVersion
	for i, m := range want.Mappings {
		want.Mappings[i] = m.ManagedLineSettings()
	}
	if !reflect.DeepEqual(s.Snapshot(), want) {
		t.Fatal("migration changed identity, credentials, bandwidth or lifecycle")
	}
	backups, err := s.ListBackups()
	if err != nil || len(backups) == 0 {
		t.Fatal(backups, err)
	}
	backup, err := s.ReadBackup(backups[0].ID)
	if err != nil || !reflect.DeepEqual(backup.State, stoppedState(before)) {
		t.Fatal("old settings not backed up", err)
	}
	reloaded, err := New(s.path)
	if err != nil || !reflect.DeepEqual(reloaded.Snapshot(), want) {
		t.Fatal("migration not persisted", err)
	}
	if n, err := reloaded.MigrateManagedLinePolicy(); err != nil || n != 0 {
		t.Fatal(n, err)
	}
	for _, m := range s.ListMappings() {
		if err := s.UpdateMappingStatus(m.ID, model.StatusStopped, ""); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.Restore(before, Revision(s.Snapshot())); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(s.Snapshot(), stoppedState(want)) {
		t.Fatal("old backup reintroduced retired settings or lost bandwidth")
	}
}

func TestManagedPolicyMigrationCanRetryFailedCommit(t *testing.T) {
	s := legacyPolicyStore(t)
	before := s.Snapshot()
	if err := os.Remove(s.path); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(s.path, 0700); err != nil {
		t.Fatal(err)
	}
	if _, err := s.MigrateManagedLinePolicy(); err == nil {
		t.Fatal("migration unexpectedly committed")
	}
	if !reflect.DeepEqual(s.Snapshot(), before) {
		t.Fatal("failed migration left partial settings")
	}
	if err := os.Remove(s.path); err != nil {
		t.Fatal(err)
	}
	if _, err := s.MigrateManagedLinePolicy(); err != nil {
		t.Fatal(err)
	}
}
