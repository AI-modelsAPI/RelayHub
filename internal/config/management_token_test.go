package config

import (
	"os"
	"path/filepath"
	"testing"
)

// AUDIT 2026-09-24 F4: the server had no way to configure its token check.
func TestManagementTokenFromFileAndEnv(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "config.json"), []byte(`{"management_token":"from-file"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.ManagementToken != "from-file" {
		t.Fatalf("file token = %q", cfg.ManagementToken)
	}
	t.Setenv("RELAYHUB_MANAGEMENT_TOKEN", "from-env")
	ApplyEnv(&cfg)
	if cfg.ManagementToken != "from-env" {
		t.Fatalf("env token = %q", cfg.ManagementToken)
	}
}
