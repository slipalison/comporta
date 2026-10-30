package store

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func plainCipher(t *testing.T) *Cipher {
	t.Helper()
	c, err := NewCipher(make([]byte, 32))
	failIf(t, err != nil, "%v", err)
	return c
}

func TestMigrateNewFile(t *testing.T) {
	st, err := Open(filepath.Join(t.TempDir(), "comporta.db"), plainCipher(t))
	failIf(t, err != nil, "%v", err)
	defer func() { _ = st.Close() }()
	m := st.Migration
	failIf(t, m.From != 0 || m.To != SchemaVersion || m.Backup != "", "new file: %+v", m)
}

// A file from db-mcp or an early Comporta: the tables exist, the version is 0.
func TestMigrateExistingFileKeepsDataAndBacksUp(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "dbmcp.db")
	raw, err := sql.Open("sqlite", path)
	failIf(t, err != nil, "%v", err)
	_, err = raw.ExecContext(ctx, migrations[0])
	failIf(t, err != nil, "%v", err)
	c := plainCipher(t)
	enc, _ := c.Encrypt("postgres://u:p@h/db")
	_, err = raw.ExecContext(ctx, `INSERT INTO connections (name, type, dsn_enc, created_at, updated_at) VALUES ('old', 'postgres', ?, 1, 1)`, enc)
	failIf(t, err != nil, "%v", err)
	_ = raw.Close()

	st, err := Open(path, c)
	failIf(t, err != nil, "%v", err)
	m := st.Migration
	failIf(t, m.From != 0 || m.To != SchemaVersion || m.Backup != path+".v0.bak", "migration: %+v", m)
	conns, err := st.ListConnections(ctx)
	failIf(t, err != nil || len(conns) != 1 || conns[0].DSN != "postgres://u:p@h/db", "data after migration: %+v %v", conns, err)
	var idx int
	_ = st.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM sqlite_master WHERE name = 'idx_audit_status_ts'").Scan(&idx)
	failIf(t, idx != 1, "migration 2 not applied")
	_ = st.Close()

	// The backup is a working copy of the file as it was.
	bak, err := sql.Open("sqlite", m.Backup)
	failIf(t, err != nil, "%v", err)
	var n, v int
	_ = bak.QueryRowContext(ctx, "SELECT COUNT(*) FROM connections").Scan(&n)
	_ = bak.QueryRowContext(ctx, "PRAGMA user_version").Scan(&v)
	_ = bak.Close()
	failIf(t, n != 1 || v != 0, "backup: %d rows, version %d", n, v)

	// Opening again changes nothing.
	st, err = Open(path, c)
	failIf(t, err != nil, "%v", err)
	failIf(t, st.Migration.From != SchemaVersion || st.Migration.Backup != "", "second open: %+v", st.Migration)
	_ = st.Close()
}

func TestMigrateRefusesNewerSchema(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "comporta.db")
	raw, err := sql.Open("sqlite", path)
	failIf(t, err != nil, "%v", err)
	_, err = raw.ExecContext(ctx, "CREATE TABLE future (x INTEGER); PRAGMA user_version = 99")
	failIf(t, err != nil, "%v", err)
	_ = raw.Close()
	_, err = Open(path, plainCipher(t))
	failIf(t, !errors.Is(err, ErrNewerSchema), "got %v", err)
	_, statErr := os.Stat(path + ".v99.bak")
	failIf(t, statErr == nil, "nothing should be backed up or changed")
}

func TestMigrationsAreAppendOnly(t *testing.T) {
	// A guard for whoever edits migrations: every step must be applicable on
	// top of the previous ones, from an empty file.
	db, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "x.db"))
	failIf(t, err != nil, "%v", err)
	defer func() { _ = db.Close() }()
	for i, m := range migrations {
		failIf(t, apply(context.Background(), db, i+1, m) != nil, "migration %d", i+1)
	}
	v, _ := userVersion(context.Background(), db)
	failIf(t, v != SchemaVersion, "version %d", v)
}
