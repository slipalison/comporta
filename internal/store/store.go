// Package store persists connections and the audit log in a local SQLite file.
package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	_ "modernc.org/sqlite"

	"dbmcp/internal/model"
)

var ErrNotFound = errors.New("not found")

type Store struct {
	db     *sql.DB
	cipher *Cipher
	Path   string
}

const schema = `
CREATE TABLE IF NOT EXISTS connections (
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
CREATE INDEX IF NOT EXISTS idx_audit_conn_ts ON audit_log(connection, ts);
`

func Open(path string, c *Cipher) (*Store, error) {
	db, err := sql.Open("sqlite", "file:"+path+"?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)&_pragma=synchronous(NORMAL)")
	if err != nil {
		return nil, err
	}
	// One connection serialises writes and avoids SQLITE_BUSY; the load is
	// one row per tool call.
	db.SetMaxOpenConns(1)
	if _, err := db.Exec(schema); err != nil {
		db.Close()
		return nil, fmt.Errorf("migrating %s: %w", path, err)
	}
	return &Store{db: db, cipher: c, Path: path}, nil
}

func (s *Store) Close() error { return s.db.Close() }

const connColumns = `id, name, type, description, dsn_enc, db_name, enabled, allow_insert, allow_update,
	allow_delete, allow_ddl, max_rows, timeout_sec, created_at, updated_at`

func (s *Store) scanConnection(row interface{ Scan(...any) error }) (model.Connection, error) {
	var c model.Connection
	var dsnEnc string
	var created, updated int64
	err := row.Scan(&c.ID, &c.Name, &c.Type, &c.Description, &dsnEnc, &c.Database, &c.Enabled,
		&c.AllowInsert, &c.AllowUpdate, &c.AllowDelete, &c.AllowDDL, &c.MaxRows, &c.TimeoutSec, &created, &updated)
	if err != nil {
		return c, err
	}
	c.CreatedAt, c.UpdatedAt = time.UnixMilli(created), time.UnixMilli(updated)
	c.DSN, err = s.cipher.Decrypt(dsnEnc)
	return c, err
}

func (s *Store) ListConnections(ctx context.Context) ([]model.Connection, error) {
	rows, err := s.db.QueryContext(ctx, "SELECT "+connColumns+" FROM connections ORDER BY name")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []model.Connection
	for rows.Next() {
		c, err := s.scanConnection(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

func (s *Store) GetConnection(ctx context.Context, id int64) (model.Connection, error) {
	c, err := s.scanConnection(s.db.QueryRowContext(ctx, "SELECT "+connColumns+" FROM connections WHERE id = ?", id))
	if errors.Is(err, sql.ErrNoRows) {
		return c, ErrNotFound
	}
	return c, err
}

func (s *Store) CreateConnection(ctx context.Context, c *model.Connection) error {
	enc, err := s.cipher.Encrypt(c.DSN)
	if err != nil {
		return err
	}
	now := time.Now()
	res, err := s.db.ExecContext(ctx, `INSERT INTO connections (name, type, description, dsn_enc, db_name, enabled,
		allow_insert, allow_update, allow_delete, allow_ddl, max_rows, timeout_sec, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		c.Name, c.Type, c.Description, enc, c.Database, c.Enabled, c.AllowInsert, c.AllowUpdate,
		c.AllowDelete, c.AllowDDL, c.MaxRows, c.TimeoutSec, now.UnixMilli(), now.UnixMilli())
	if err != nil {
		return uniqueErr(err, c.Name)
	}
	c.ID, _ = res.LastInsertId()
	c.CreatedAt, c.UpdatedAt = now, now
	return nil
}

func (s *Store) UpdateConnection(ctx context.Context, c *model.Connection) error {
	enc, err := s.cipher.Encrypt(c.DSN)
	if err != nil {
		return err
	}
	c.UpdatedAt = time.Now()
	res, err := s.db.ExecContext(ctx, `UPDATE connections SET name = ?, type = ?, description = ?, dsn_enc = ?,
		db_name = ?, enabled = ?, allow_insert = ?, allow_update = ?, allow_delete = ?, allow_ddl = ?,
		max_rows = ?, timeout_sec = ?, updated_at = ? WHERE id = ?`,
		c.Name, c.Type, c.Description, enc, c.Database, c.Enabled, c.AllowInsert, c.AllowUpdate,
		c.AllowDelete, c.AllowDDL, c.MaxRows, c.TimeoutSec, c.UpdatedAt.UnixMilli(), c.ID)
	if err != nil {
		return uniqueErr(err, c.Name)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *Store) DeleteConnection(ctx context.Context, id int64) error {
	res, err := s.db.ExecContext(ctx, "DELETE FROM connections WHERE id = ?", id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

func uniqueErr(err error, name string) error {
	if strings.Contains(err.Error(), "UNIQUE") {
		return fmt.Errorf("a connection named %q already exists", name)
	}
	return err
}

func (s *Store) InsertAudit(ctx context.Context, e model.AuditEntry) error {
	if e.Time.IsZero() {
		e.Time = time.Now()
	}
	_, err := s.db.ExecContext(ctx, `INSERT INTO audit_log (ts, client, connection, db_type, tool, operation,
		statement, params, status, message, rows, duration_ms) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		e.Time.UnixMilli(), e.Client, e.Connection, e.DBType, e.Tool, e.Operation, e.Statement, e.Params,
		e.Status, e.Message, e.Rows, e.DurationMS)
	return err
}

type AuditFilter struct {
	Connection string
	Status     string
	Operation  string
	Tool       string
	Client     string
	Search     string
	Since      time.Time
	Until      time.Time
	BeforeID   int64
	Limit      int
}

func (f AuditFilter) where() (string, []any) {
	var conds []string
	var args []any
	add := func(cond string, v any) { conds = append(conds, cond); args = append(args, v) }
	if f.Connection != "" {
		add("connection = ?", f.Connection)
	}
	if f.Status != "" {
		add("status = ?", f.Status)
	}
	if f.Operation != "" {
		add("operation LIKE ?", "%"+f.Operation+"%")
	}
	if f.Tool != "" {
		add("tool = ?", f.Tool)
	}
	if f.Client != "" {
		add("client LIKE ?", "%"+f.Client+"%")
	}
	if f.Search != "" {
		conds = append(conds, "(statement LIKE ? OR message LIKE ? OR params LIKE ?)")
		q := "%" + f.Search + "%"
		args = append(args, q, q, q)
	}
	if !f.Since.IsZero() {
		add("ts >= ?", f.Since.UnixMilli())
	}
	if !f.Until.IsZero() {
		add("ts <= ?", f.Until.UnixMilli())
	}
	if f.BeforeID > 0 {
		add("id < ?", f.BeforeID)
	}
	if len(conds) == 0 {
		return "", nil
	}
	return " WHERE " + strings.Join(conds, " AND "), args
}

func (s *Store) ListAudit(ctx context.Context, f AuditFilter) ([]model.AuditEntry, error) {
	where, args := f.where()
	args = append(args, f.Limit)
	rows, err := s.db.QueryContext(ctx, `SELECT id, ts, client, connection, db_type, tool, operation, statement,
		params, status, message, rows, duration_ms FROM audit_log`+where+` ORDER BY id DESC LIMIT ?`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []model.AuditEntry{}
	for rows.Next() {
		var e model.AuditEntry
		var ts int64
		if err := rows.Scan(&e.ID, &ts, &e.Client, &e.Connection, &e.DBType, &e.Tool, &e.Operation, &e.Statement,
			&e.Params, &e.Status, &e.Message, &e.Rows, &e.DurationMS); err != nil {
			return nil, err
		}
		e.Time = time.UnixMilli(ts)
		out = append(out, e)
	}
	return out, rows.Err()
}

type AuditStats struct {
	Total  int64 `json:"total"`
	OK     int64 `json:"ok"`
	Denied int64 `json:"denied"`
	Error  int64 `json:"error"`
	Stored int64 `json:"stored"`
}

func (s *Store) Stats(ctx context.Context, since time.Time) (AuditStats, error) {
	var st AuditStats
	rows, err := s.db.QueryContext(ctx, "SELECT status, COUNT(*) FROM audit_log WHERE ts >= ? AND tool != 'admin' GROUP BY status", since.UnixMilli())
	if err != nil {
		return st, err
	}
	defer rows.Close()
	for rows.Next() {
		var status string
		var n int64
		if err := rows.Scan(&status, &n); err != nil {
			return st, err
		}
		st.Total += n
		switch status {
		case model.StatusOK:
			st.OK = n
		case model.StatusDenied:
			st.Denied = n
		case model.StatusError:
			st.Error = n
		}
	}
	if err := rows.Err(); err != nil {
		return st, err
	}
	err = s.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM audit_log").Scan(&st.Stored)
	return st, err
}

// PurgeAudit removes entries older than the cutoff and returns how many.
func (s *Store) PurgeAudit(ctx context.Context, before time.Time) (int64, error) {
	res, err := s.db.ExecContext(ctx, "DELETE FROM audit_log WHERE ts < ?", before.UnixMilli())
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}
