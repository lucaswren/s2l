package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestUninstallStateInspectionNeverRecoversOrRewrites(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	for _, text := range []string{"null", "{}", `{"nodes":null,"mappings":null}`, "{broken", `{"nodes":[],"mappings":[{"id":"bad","table_id":254}]}`} {
		if err := os.WriteFile(path, []byte(text), 0600); err != nil {
			t.Fatal(err)
		}
		if _, err := uninstallMappings(path); err == nil {
			t.Fatal("corrupt state accepted")
		}
		got, _ := os.ReadFile(path)
		if string(got) != text {
			t.Fatal("inspection rewrote state")
		}
	}
	if err := os.WriteFile(path, []byte(`{"nodes":[],"mappings":[]}`), 0600); err != nil {
		t.Fatal(err)
	}
	if mappings, err := uninstallMappings(path); err != nil || len(mappings) != 0 {
		t.Fatal(err, mappings)
	}
}
