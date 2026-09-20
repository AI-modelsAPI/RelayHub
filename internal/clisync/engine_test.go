package clisync_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"relayhub/internal/clisync"
	"relayhub/internal/clisync/claude"
	"relayhub/internal/clisync/codex"
	"relayhub/internal/clisync/hermes"
)

func TestAtomicWriteAndSymlinkRejection(t *testing.T) {
	dir := t.TempDir()
	e := clisync.NewEngine(nil, filepath.Join(dir, "backups"))

	target := filepath.Join(dir, "config.json")
	if err := e.WriteAtomic(target, []byte(`{"a":1}`)); err != nil {
		t.Fatalf("WriteAtomic failed: %v", err)
	}
	content, _ := os.ReadFile(target)
	if string(content) != `{"a":1}` {
		t.Fatalf("unexpected content: %s", content)
	}

	// Symlink rejection test
	sym := filepath.Join(dir, "symlink-target")
	_ = os.Symlink(target, sym)

	if err := e.WriteAtomic(sym, []byte(`{"a":2}`)); err == nil {
		t.Fatal("expected error writing to symlink, got nil")
	}
}

func TestClaudeCodeSyncCycle(t *testing.T) {
	dir := t.TempDir()
	e := clisync.NewEngine(nil, filepath.Join(dir, "backups"))
	cfgFile := filepath.Join(dir, "settings.json")

	// Prepopulate with existing MCP / hook settings
	initial := `{"mcpServers":{"test":{"command":"node"}},"env":{"EXISTING_VAR":"123"}}`
	_ = os.WriteFile(cfgFile, []byte(initial), 0600)

	syncer := claude.New(e, cfgFile)
	ctx := context.Background()

	target, _ := syncer.Detect(ctx)
	if !target.Exists {
		t.Fatal("target should exist")
	}

	diff, err := syncer.Preview(ctx, clisync.DesiredState{
		BaseURL: "http://127.0.0.1:8789",
		APIKey:  "my-local-key",
		Model:   "claude-3-7-sonnet",
	})
	if err != nil || !diff.HasChanges {
		t.Fatalf("preview failed: %v, diff=%+v", err, diff)
	}

	backup, err := syncer.Apply(ctx, diff)
	if err != nil {
		t.Fatalf("apply failed: %v", err)
	}
	if backup.BackupPath == "" {
		t.Fatal("backup path should not be empty")
	}

	// Verify settings
	if err := syncer.Verify(ctx, clisync.DesiredState{BaseURL: "http://127.0.0.1:8789"}); err != nil {
		t.Fatalf("verify failed: %v", err)
	}

	// Ensure pre-existing settings remained intact
	updated, _ := os.ReadFile(cfgFile)
	if !contains(string(updated), "EXISTING_VAR") || !contains(string(updated), "mcpServers") {
		t.Fatalf("pre-existing settings were wiped: %s", string(updated))
	}
}

func TestCodexSyncCycle(t *testing.T) {
	dir := t.TempDir()
	e := clisync.NewEngine(nil, filepath.Join(dir, "backups"))
	cfgFile := filepath.Join(dir, "config.toml")

	initial := "model = \"gpt-4\"\napproval_policy = \"never\"\n"
	_ = os.WriteFile(cfgFile, []byte(initial), 0600)

	syncer := codex.New(e, cfgFile)
	ctx := context.Background()

	diff, err := syncer.Preview(ctx, clisync.DesiredState{
		BaseURL: "http://127.0.0.1:8789/v1",
		Model:   "coding",
	})
	if err != nil || !diff.HasChanges {
		t.Fatalf("preview failed: %v, diff=%+v", err, diff)
	}

	_, err = syncer.Apply(ctx, diff)
	if err != nil {
		t.Fatalf("apply failed: %v", err)
	}

	if err := syncer.Verify(ctx, clisync.DesiredState{}); err != nil {
		t.Fatalf("verify failed: %v", err)
	}

	updated, _ := os.ReadFile(cfgFile)
	if !contains(string(updated), "approval_policy") || !contains(string(updated), "model_providers.relayhub") {
		t.Fatalf("codex config invalid: %s", string(updated))
	}
}

func TestHermesSyncCycle(t *testing.T) {
	dir := t.TempDir()
	e := clisync.NewEngine(nil, filepath.Join(dir, "backups"))

	syncer := hermes.New(e, dir)
	ctx := context.Background()

	cfgFile := filepath.Join(dir, "config.yaml")
	initial := "display:\n  skin: default\nskills:\n  creation_nudge_interval: 15\n"
	_ = os.WriteFile(cfgFile, []byte(initial), 0600)

	diff, err := syncer.Preview(ctx, clisync.DesiredState{
		BaseURL:  "http://127.0.0.1:8789/v1",
		Model:    "coding",
		Provider: "relayhub",
	})
	if err != nil || !diff.HasChanges {
		t.Fatalf("preview failed: %v, diff=%+v", err, diff)
	}

	// Writing a config without a real gateway key would produce a CLI that
	// looks synced but 401s on first use, so apply must refuse rather than
	// substitute a placeholder.
	if _, err := syncer.Apply(ctx, diff); err == nil {
		t.Fatal("expected apply to refuse writing without an api key, got nil error")
	}
	if _, statErr := os.Stat(filepath.Join(dir, ".env")); statErr == nil {
		t.Fatal("refused apply must not leave a .env behind")
	}

	diff.APIKey = "rh_test-local-key"
	_, err = syncer.Apply(ctx, diff)
	if err != nil {
		t.Fatalf("apply failed: %v", err)
	}

	if err := syncer.Verify(ctx, clisync.DesiredState{}); err != nil {
		t.Fatalf("verify failed: %v", err)
	}

	updated, _ := os.ReadFile(cfgFile)
	if !contains(string(updated), "skills") || !contains(string(updated), "providers") {
		t.Fatalf("hermes config wiped: %s", string(updated))
	}

	// Check .env created
	envData, _ := os.ReadFile(filepath.Join(dir, ".env"))
	if !contains(string(envData), "HERMES_CUSTOM_RELAYHUB_API_KEY") {
		t.Fatalf("hermes .env missing key: %s", string(envData))
	}
}

func contains(s, substr string) bool {
	for i := 0; i+len(substr) <= len(s); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
