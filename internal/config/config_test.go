package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadRequiresExplicitConfig(t *testing.T) {
	if _, err := Load(filepath.Join(t.TempDir(), "missing.json")); err == nil {
		t.Fatal("missing explicit config silently enabled defaults")
	}
	if cfg, err := Load(""); err != nil || cfg.Listen != Default().Listen {
		t.Fatalf("implicit defaults: %+v, %v", cfg, err)
	}
}

func TestLoadRejectsNull(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(path, []byte("null"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(path); err == nil {
		t.Fatal("null config silently enabled defaults")
	}
}
