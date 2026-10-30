package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
)

// migrations brings the store from one schema version to the next; the
// version of a file is SQLite's PRAGMA user_version. Append new steps at the
// end and never edit a released one: a volume may hold any older version.
//
// Version 1 is the schema db-mcp and the first Comporta releases created
// without a version number (user_version 0). Its statements are idempotent,
// so applying it to such a file changes nothing but the version.
var migrations = []string{
	// 1: connections and audit log
	`CREATE TABLE IF NOT EXISTS connections (
		id           INTEGER PRIMARY KEY AUTOINCREMENT,
		name         TEXT    NOT NULL UNIQUE,
		type         TEXT    NOT NULL,
		description  TEXT    NOT NULL DEFAULT '',
		dsn_enc      TEXT    NOT NULL,
		db_name      TEXT    NOT NULL DEFAULT '',
		enabled      INTEGER NOT NULL DEFAULT 1,
		allow_insert INTEGER NOT NULL DEFAULT 0,
		allow_update INTEGER NOT NULL DEFAULT 0,
		allow_delete INTEGER NOT NULL DEFAULT 0,
		allow_ddl    INTEGER NOT NULL DEFAULT 0,
		max_rows     INTEGER NOT NULL DEFAULT 100,
		timeout_sec  INTEGER NOT NULL DEFAULT 30,
		created_at   INTEGER NOT NULL,
		updated_at   INTEGER NOT NULL
	);
	CREATE TABLE IF NOT EXISTS audit_log (
		id          INTEGER PRIMARY KEY AUTOINCREMENT,
		ts          INTEGER NOT NULL,
		client      TEXT    NOT NULL DEFAULT '',
		connection  TEXT    NOT NULL DEFAULT '',
		db_type     TEXT    NOT NULL DEFAULT '',
		tool        TEXT    NOT NULL,
		operation   TEXT    NOT NULL DEFAULT '',
		statement   TEXT    NOT NULL DEFAULT '',
		params      TEXT    NOT NULL DEFAULT '',
		status      TEXT    NOT NULL,
		message     TEXT    NOT NULL DEFAULT '',
		rows        INTEGER NOT NULL DEFAULT 0,
		duration_ms INTEGER NOT NULL DEFAULT 0
	);
	CREATE INDEX IF NOT EXISTS idx_audit_ts ON audit_log(ts);
	CREATE INDEX IF NOT EXISTS idx_audit_conn_ts ON audit_log(connection, ts);`,

	// 2: the 24-hour summary and the "denied"/"error" filters read by status
	`CREATE INDEX IF NOT EXISTS idx_audit_status_ts ON audit_log(status, ts);`,
}

// SchemaVersion is the version this build writes.
var SchemaVersion = len(migrations)

// Migration reports what Open did to the file.
type Migration struct {
	From, To int
	// Backup is the copy taken before changing a file that already had
	// data; empty when nothing was migrated or the file was new.
	Backup string
}

// ErrNewerSchema means the file was written by a newer Comporta. Starting an
// older image on it could lose data, so Open refuses.
var ErrNewerSchema = errors.New("the database was written by a newer version of Comporta")

func userVersion(ctx context.Context, db *sql.DB) (int, error) {
	var v int
	err := db.QueryRowContext(ctx, "PRAGMA user_version").Scan(&v)
	return v, err
}

func hasTables(ctx context.Context, db *sql.DB) (bool, error) {
	var n int
	err := db.QueryRowContext(ctx, "SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name NOT LIKE 'sqlite_%'").Scan(&n)
	return n > 0, err
}

// migrate applies the pending migrations, each in its own transaction with
// the new version number. A file that already holds tables is copied to
// <path>.v<from>.bak first, so an upgrade can always be undone by hand.
func migrate(ctx context.Context, db *sql.DB, path string) (Migration, error) {
	from, err := userVersion(ctx, db)
	if err != nil {
		return Migration{}, err
	}
	m := Migration{From: from, To: from}
	if from > SchemaVersion {
		return m, fmt.Errorf("%w: %s is at schema version %d and this image knows up to %d; run the newer image or restore a backup",
			ErrNewerSchema, path, from, SchemaVersion)
	}
	if from == SchemaVersion {
		return m, nil
	}
	existing, err := hasTables(ctx, db)
	if err != nil {
		return m, err
	}
	if existing {
		if m.Backup, err = backup(ctx, db, fmt.Sprintf("%s.v%d.bak", path, from)); err != nil {
			return m, fmt.Errorf("backing up %s before migrating it: %w", path, err)
		}
	}
	for v := from; v < SchemaVersion; v++ {
		if err := apply(ctx, db, v+1, migrations[v]); err != nil {
			return m, fmt.Errorf("migrating %s to schema version %d: %w", path, v+1, err)
		}
		m.To = v + 1
	}
	return m, nil
}

func apply(ctx context.Context, db *sql.DB, version int, stmt string) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.ExecContext(ctx, stmt); err != nil {
		return err
	}
	// PRAGMA takes no parameters; version is an int from this file.
	if _, err := tx.ExecContext(ctx, fmt.Sprintf("PRAGMA user_version = %d", version)); err != nil {
		return err
	}
	return tx.Commit()
}

// backup writes a consistent copy of the database with VACUUM INTO,
// replacing an older backup of the same version.
func backup(ctx context.Context, db *sql.DB, target string) (string, error) {
	if err := os.Remove(target); err != nil && !errors.Is(err, os.ErrNotExist) {
		return "", err
	}
	if _, err := db.ExecContext(ctx, "VACUUM INTO ?", target); err != nil {
		return "", err
	}
	return target, nil
}
