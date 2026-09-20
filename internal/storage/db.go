package storage

import (
	"context"
	"database/sql"
	_ "embed"
	"errors"
	"os"
	"path/filepath"
	"time"

	_ "modernc.org/sqlite"
)

//go:embed migrations/001_initial.sql
var initialSchema string

//go:embed migrations/002_channel_extensions.sql
var channelExtensions string

//go:embed migrations/003_normalize_protocols.sql
var normalizeProtocols string

//go:embed migrations/004_channel_checkin_mode.sql
var channelCheckinMode string

//go:embed migrations/005_model_catalog.sql
var modelCatalog string

//go:embed migrations/006_channel_keys.sql
var channelKeys string

//go:embed migrations/007_model_icon_archive.sql
var modelIconArchive string

type DB struct {
	*sql.DB
	// JournalMode is the effective SQLite journal mode after Open (normally "wal").
	JournalMode string
}

func Open(path string) (*DB, error) {
	if path == "" {
		return nil, errors.New("database path must not be empty")
	}
	if dir := filepath.Dir(path); dir != "." {
		if err := os.MkdirAll(dir, 0700); err != nil {
			return nil, err
		}
	}
	// Persistence optimum for a local single-writer desktop app:
	//   journal_mode=WAL  — crash/power-loss safe, readers never block the writer,
	//                        and the mode is persisted in the DB file header.
	//   synchronous=NORMAL — the WAL-recommended durability level: safe against
	//                        app crashes, only a same-instant OS/power loss can lose
	//                        the very last commit (acceptable for local config data),
	//                        far fewer fsyncs than FULL.
	//   busy_timeout=5000  — wait up to 5s on a lock instead of erroring immediately.
	//   foreign_keys=1     — enforce referential integrity (fail-closed; verified below).
	dsn := path + "?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)&_pragma=synchronous(NORMAL)&_pragma=foreign_keys(1)"
	d, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	d.SetMaxOpenConns(1)
	d.SetMaxIdleConns(1)
	if err = d.Ping(); err != nil {
		_ = d.Close()
		return nil, err
	}
	var fkEnabled int
	if err := d.QueryRow("PRAGMA foreign_keys").Scan(&fkEnabled); err != nil {
		_ = d.Close()
		return nil, err
	}
	if fkEnabled != 1 {
		_ = d.Close()
		return nil, errors.New("sqlite foreign keys pragma failed to enable (PRAGMA foreign_keys != 1)")
	}
	// Confirm the journal mode engaged (readable back from the connection). WAL is
	// the target; on network filesystems SQLite silently keeps a rollback journal,
	// which is still crash-safe, so we record the effective mode rather than fail.
	var jm string
	if err := d.QueryRow("PRAGMA journal_mode").Scan(&jm); err != nil {
		_ = d.Close()
		return nil, err
	}
	return &DB{DB: d, JournalMode: jm}, nil
}

// migrations is the ordered list of schema migrations. Each entry runs once,
// tracked in schema_migrations; failures roll back and leave prior versions intact.
var migrations = []struct {
	Version int
	SQL     string
}{
	{1, initialSchema},
	{2, channelExtensions},
	{3, normalizeProtocols},
	{4, channelCheckinMode},
	{5, modelCatalog},
	{6, channelKeys},
	{7, modelIconArchive},
}

func (d *DB) Migrate(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if _, err := d.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS schema_migrations (version INTEGER PRIMARY KEY, applied_at TEXT NOT NULL)`); err != nil {
		return err
	}
	for _, m := range migrations {
		var applied int
		if err := d.QueryRowContext(ctx, `SELECT COUNT(*) FROM schema_migrations WHERE version=?`, m.Version).Scan(&applied); err != nil {
			return err
		}
		if applied != 0 {
			continue
		}
		tx, err := d.BeginTx(ctx, nil)
		if err != nil {
			return err
		}
		if _, err = tx.ExecContext(ctx, m.SQL); err != nil {
			_ = tx.Rollback()
			return err
		}
		if _, err = tx.ExecContext(ctx, `INSERT INTO schema_migrations(version, applied_at) VALUES(?, ?)`, m.Version, time.Now().UTC().Format(time.RFC3339Nano)); err != nil {
			_ = tx.Rollback()
			return err
		}
		if err = tx.Commit(); err != nil {
			return err
		}
	}
	return nil
}
