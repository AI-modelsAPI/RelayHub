package storage

import (
	"context"
	"path/filepath"
	"testing"
)

// TestWALModeAndPersistenceAcrossReopen verifies the durability configuration:
// WAL journal engages, foreign keys are on, and committed rows survive a full
// close+reopen of the database file (the persistence guarantee a desktop user
// relies on — data must outlive an app restart).
func TestWALModeAndPersistenceAcrossReopen(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "persist.db")

	db, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	if db.JournalMode != "wal" {
		t.Fatalf("expected WAL journal mode on local fs, got %q", db.JournalMode)
	}
	var fk int
	if err := db.QueryRow("PRAGMA foreign_keys").Scan(&fk); err != nil || fk != 1 {
		t.Fatalf("foreign_keys not enforced: fk=%d err=%v", fk, err)
	}
	// Write a row via a direct provider insert, commit, then close.
	if _, err := db.ExecContext(ctx, `INSERT INTO providers(id,name,adapter_type,protocol,created_at,updated_at) VALUES('p','P','generic','openai-chat','2026-01-01T00:00:00Z','2026-01-01T00:00:00Z')`); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	// Reopen the same file — the row must still be there (persisted, not in-memory).
	db2, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer db2.Close()
	var name string
	if err := db2.QueryRow(`SELECT name FROM providers WHERE id='p'`).Scan(&name); err != nil {
		t.Fatalf("row did not persist across reopen: %v", err)
	}
	if name != "P" {
		t.Fatalf("persisted value wrong: %q", name)
	}
	// Migrations must be idempotent on the reopened (already-migrated) DB.
	if err := db2.Migrate(ctx); err != nil {
		t.Fatalf("re-migrate on existing DB failed: %v", err)
	}
}
