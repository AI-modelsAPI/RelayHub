package storage

import "context"

func (d *DB) MigrateWithContext(ctx context.Context) error { return d.Migrate(ctx) }
