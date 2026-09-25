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

// AUDIT 2026-09-24 F4: authentication is on unless explicitly turned off.
func TestManagementAuthDefaultsToToken(t *testing.T) {
	t.Setenv("RELAYHUB_MANAGEMENT_AUTH", "")
	cfg, err := Load(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	ApplyEnv(&cfg)
	if cfg.ManagementAuth != "token" {
		t.Fatalf("default management_auth %q, want token", cfg.ManagementAuth)
	}
	t.Setenv("RELAYHUB_MANAGEMENT_AUTH", "off")
	ApplyEnv(&cfg)
	if cfg.ManagementAuth != "off" {
		t.Fatalf("env override not applied: %q", cfg.ManagementAuth)
	}
}
