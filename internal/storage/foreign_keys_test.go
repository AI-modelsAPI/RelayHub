package storage

import (
	"context"
	"path/filepath"
	"testing"
)

func TestForeignKeysEnforced(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "fk_test.db")
	d, err := Open(dbPath)
	if err != nil {
		t.Fatalf("Open failed: %v", err)
	}
	defer d.Close()

	ctx := context.Background()
	if err := d.Migrate(ctx); err != nil {
		t.Fatalf("Migrate failed: %v", err)
	}

	// 1. Check PRAGMA foreign_keys is 1
	var fk int
	if err := d.QueryRowContext(ctx, "PRAGMA foreign_keys").Scan(&fk); err != nil {
		t.Fatalf("query PRAGMA foreign_keys: %v", err)
	}
	if fk != 1 {
		t.Fatalf("expected PRAGMA foreign_keys == 1, got %d", fk)
	}

	// 2. Inserting orphan foreign key into provider_models must fail
	_, err = d.ExecContext(ctx, `INSERT INTO provider_models (id, provider_id, model_id, upstream_model_name, protocol, created_at, updated_at)
		VALUES ('pm_orphan', 'nonexistent_p', 'nonexistent_m', 'm', 'openai-chat', '2026-09-14T00:00:00Z', '2026-09-14T00:00:00Z')`)
	if err == nil {
		t.Fatalf("expected orphan foreign key insert to fail, but it succeeded")
	}

	// 3. Deleting provider referenced by channels (ON DELETE RESTRICT) must fail
	if _, err := d.ExecContext(ctx, `INSERT INTO providers (id, name, adapter_type, protocol, created_at, updated_at)
		VALUES ('p1', 'P1', 'generic', 'openai-chat', '2026-09-14T00:00:00Z', '2026-09-14T00:00:00Z')`); err != nil {
		t.Fatalf("insert provider: %v", err)
	}
	if _, err := d.ExecContext(ctx, `INSERT INTO channels (id, provider_id, name, base_url, created_at, updated_at)
		VALUES ('c1', 'p1', 'C1', 'http://127.0.0.1', '2026-09-14T00:00:00Z', '2026-09-14T00:00:00Z')`); err != nil {
		t.Fatalf("insert channel: %v", err)
	}
	_, err = d.ExecContext(ctx, `DELETE FROM providers WHERE id = 'p1'`)
	if err == nil {
		t.Fatalf("expected DELETE on referenced provider to fail with RESTRICT constraint, but it succeeded")
	}
}
