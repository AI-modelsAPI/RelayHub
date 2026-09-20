package storage

import (
	"context"
	"path/filepath"
	"testing"
)

func TestMigrateIdempotent(t *testing.T) {
	d, err := Open(filepath.Join(t.TempDir(), "relay.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	ctx := context.Background()
	if err = d.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	var appliedAt string
	if err = d.QueryRow(`SELECT applied_at FROM schema_migrations WHERE version=1`).Scan(&appliedAt); err != nil {
		t.Fatal(err)
	}
	if appliedAt == "" {
		t.Fatal("migration timestamp is empty")
	}
	if err = d.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	var n int
	if err = d.QueryRow("SELECT COUNT(*) FROM schema_migrations").Scan(&n); err != nil || n != len(migrations) {
		t.Fatalf("migrations=%d err=%v", n, err)
	}
}

func TestFailedMigrationPreservesPreviousSchemaAndCanRecover(t *testing.T) {
	d, err := Open(filepath.Join(t.TempDir(), "relay.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	ctx := context.Background()
	// Deliberately conflict with the embedded migration. This makes the
	// migration fail inside its transaction without damaging this old table.
	if _, err = d.Exec(`CREATE TABLE providers (id TEXT PRIMARY KEY)`); err != nil {
		t.Fatal(err)
	}
	if err = d.Migrate(ctx); err == nil {
		t.Fatal("expected migration failure")
	}
	var count int
	if err = d.QueryRow(`SELECT COUNT(*) FROM providers`).Scan(&count); err != nil {
		t.Fatalf("previous schema unusable: %v", err)
	}
	if err = d.QueryRow(`SELECT COUNT(*) FROM schema_migrations`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("failed migration was recorded: %d", count)
	}
	if _, err = d.Exec(`DROP TABLE providers`); err != nil {
		t.Fatal(err)
	}
	if err = d.Migrate(ctx); err != nil {
		t.Fatalf("retry migration: %v", err)
	}
	var tables int
	if err = d.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name='routes'`).Scan(&tables); err != nil {
		t.Fatal(err)
	}
	if tables != 1 {
		t.Fatalf("routes table count=%d", tables)
	}
}

func TestMigration003NormalizesExistingProtocols(t *testing.T) {
	d, err := Open(filepath.Join(t.TempDir(), "relay_mig3.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	ctx := context.Background()
	// Apply first 2 migrations
	if _, err := d.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS schema_migrations (version INTEGER PRIMARY KEY, applied_at TEXT NOT NULL)`); err != nil {
		t.Fatal(err)
	}
	for _, m := range migrations[:2] {
		if _, err := d.ExecContext(ctx, m.SQL); err != nil {
			t.Fatal(err)
		}
		if _, err := d.ExecContext(ctx, "INSERT INTO schema_migrations (version, applied_at) VALUES (?, datetime('now'))", m.Version); err != nil {
			t.Fatal(err)
		}
	}
	// Insert old protocol rows
	if _, err := d.ExecContext(ctx, `INSERT INTO providers (id, name, adapter_type, protocol, base_url_template, created_at, updated_at) VALUES ('p1', 'P1', 'generic', 'openai', 'https://api.test', '2026-09-14T00:00:00Z', '2026-09-14T00:00:00Z')`); err != nil {
		t.Fatal(err)
	}
	if _, err := d.ExecContext(ctx, `INSERT INTO models (id, display_name, created_at, updated_at) VALUES ('m1', 'M1', '2026-09-14T00:00:00Z', '2026-09-14T00:00:00Z')`); err != nil {
		t.Fatal(err)
	}
	if _, err := d.ExecContext(ctx, `INSERT INTO provider_models (id, provider_id, channel_id, model_id, upstream_model_name, protocol, created_at, updated_at) VALUES ('pm1', 'p1', NULL, 'm1', 'm1', 'openai', '2026-09-14T00:00:00Z', '2026-09-14T00:00:00Z')`); err != nil {
		t.Fatal(err)
	}
	// Run Migrate to execute migration 3
	if err := d.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	var pProto, pmProto string
	if err := d.QueryRowContext(ctx, `SELECT protocol FROM providers WHERE id='p1'`).Scan(&pProto); err != nil {
		t.Fatal(err)
	}
	if err := d.QueryRowContext(ctx, `SELECT protocol FROM provider_models WHERE id='pm1'`).Scan(&pmProto); err != nil {
		t.Fatal(err)
	}
	if pProto != "openai-chat" || pmProto != "openai-chat" {
		t.Fatalf("expected openai-chat, got p=%s pm=%s", pProto, pmProto)
	}
}

func TestSchemaContainsRequiredTablesAndIndexes(t *testing.T) {
	d, err := Open(filepath.Join(t.TempDir(), "relay.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	if err = d.Migrate(context.Background()); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"providers", "channels", "credentials", "models", "provider_models", "model_groups", "model_group_members", "routes", "checkin_records", "health_records", "request_records", "cli_sync_records", "config_backups", "schema_migrations"} {
		var count int
		if err = d.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name=?`, name).Scan(&count); err != nil {
			t.Fatal(err)
		}
		if count != 1 {
			t.Fatalf("missing required table %q", name)
		}
	}
	for _, name := range []string{"routes_match_idx", "channels_health_idx", "health_channel_state_idx"} {
		var count int
		if err = d.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type='index' AND name=?`, name).Scan(&count); err != nil {
			t.Fatal(err)
		}
		if count != 1 {
			t.Fatalf("missing required index %q", name)
		}
	}
}
