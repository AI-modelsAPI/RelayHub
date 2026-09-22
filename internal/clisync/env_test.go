package clisync_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"relayhub/internal/clisync"
)

func TestUpsertEnvVarCreatesReplacesAndPreserves(t *testing.T) {
	dir := t.TempDir()
	e := clisync.NewEngine(nil, filepath.Join(dir, "backups"))
	path := filepath.Join(dir, "codex", ".env")

	// Create from nothing.
	if err := e.UpsertEnvVar(path, "RELAYHUB_API_KEY", "rh_one"); err != nil {
		t.Fatalf("create: %v", err)
	}
	got, _ := os.ReadFile(path)
	if string(got) != "RELAYHUB_API_KEY=rh_one\n" {
		t.Fatalf("unexpected fresh content %q", string(got))
	}
	if fi, _ := os.Stat(path); fi.Mode().Perm() != 0o600 {
		t.Fatalf("secret-bearing .env must be 0600, got %o", fi.Mode().Perm())
	}

	// Replace in place, keep unrelated lines and an `export` form.
	_ = os.WriteFile(path, []byte("OPENAI_API_KEY=sk-other\nexport RELAYHUB_API_KEY=rh_one\n# comment\n"), 0o600)
	if err := e.UpsertEnvVar(path, "RELAYHUB_API_KEY", "rh_two"); err != nil {
		t.Fatalf("replace: %v", err)
	}
	got, _ = os.ReadFile(path)
	want := "OPENAI_API_KEY=sk-other\nRELAYHUB_API_KEY=rh_two\n# comment\n"
	if string(got) != want {
		t.Fatalf("replace produced %q, want %q", string(got), want)
	}
	if strings.Count(string(got), "RELAYHUB_API_KEY=") != 1 {
		t.Fatal("key must appear exactly once")
	}

	// Append to an unterminated file without gluing lines together.
	_ = os.WriteFile(path, []byte("A=1"), 0o600)
	if err := e.UpsertEnvVar(path, "RELAYHUB_API_KEY", "rh_three"); err != nil {
		t.Fatalf("append: %v", err)
	}
	got, _ = os.ReadFile(path)
	if string(got) != "A=1\nRELAYHUB_API_KEY=rh_three\n" {
		t.Fatalf("append produced %q", string(got))
	}

	// Placeholder and empty keys are refused before touching disk.
	for _, bad := range []string{"", clisync.PlaceholderAPIKey} {
		if err := e.UpsertEnvVar(path, "RELAYHUB_API_KEY", bad); err == nil {
			t.Fatalf("placeholder key %q must be refused", bad)
		}
	}
	got, _ = os.ReadFile(path)
	if !clisync.HasEnvVar(string(got), "RELAYHUB_API_KEY") || strings.Contains(string(got), clisync.PlaceholderAPIKey) {
		t.Fatalf("refused write must not alter the file: %q", string(got))
	}
}

func TestHasEnvVar(t *testing.T) {
	if !clisync.HasEnvVar("export K=v\n", "K") || clisync.HasEnvVar("K=\n", "K") || clisync.HasEnvVar("KX=1\n", "K") {
		t.Fatal("HasEnvVar mismatch")
	}
}
