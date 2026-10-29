package store

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/slipalison/comporta/internal/model"
)

func TestLoadKeysAndCipher(t *testing.T) {
	dir := t.TempDir()
	k1, err := LoadKeys("", dir)
	failIf(t, err != nil || len(k1.Current) != 32 || k1.Legacy != nil, "generated key: %v %+v", err, k1)
	k2, err := LoadKeys("", dir)
	failIf(t, err != nil || string(k1.Current) != string(k2.Current), "the generated key must be reused on the next start")
	info, err := os.Stat(filepath.Join(dir, "secret.key"))
	failIf(t, err != nil || info.Mode().Perm() != 0o600, "key file: %v %v", info, err)

	fromSecret, err := LoadKeys("uma frase", dir)
	failIf(t, err != nil || string(fromSecret.Current) == string(k1.Current), "an explicit secret must win over the file: %v", err)
	again, _ := LoadKeys("uma frase", dir)
	failIf(t, string(again.Current) != string(fromSecret.Current), "the salt must be reused on the next start")
	otherDir, _ := LoadKeys("uma frase", t.TempDir())
	failIf(t, string(otherDir.Current) == string(fromSecret.Current), "each volume gets its own salt")

	err = os.WriteFile(filepath.Join(dir, "secret.key"), []byte("curta"), 0o600)
	failIf(t, err != nil, "%v", err)
	_, err = LoadKeys("", dir)
	failIf(t, err == nil, "a corrupt key file must be an error")

	c, _ := NewCipher(k1.Current)
	enc, err := c.Encrypt("postgres://u:p@h/db")
	failIf(t, err != nil || !strings.HasPrefix(enc, cipherPrefix) || strings.Contains(enc, "p@h") || c.Outdated(enc), "encrypt: %q %v", enc, err)
	plain, err := c.Decrypt(enc)
	failIf(t, err != nil || plain != "postgres://u:p@h/db", "decrypt: %q %v", plain, err)
	other, _ := NewCipherKeys(fromSecret)
	_, err = other.Decrypt(enc)
	failIf(t, err == nil || !strings.Contains(err.Error(), "COMPORTA_SECRET_KEY"), "wrong key: %v", err)
	_, err = c.Decrypt("v2:AA==")
	failIf(t, err == nil, "short ciphertext must fail")
}

// A volume from db-mcp: v1 values sealed with SHA-256 of the secret are read
// with the legacy key and sealed again as v2 when the store opens.
func TestOpenReencryptsLegacyValues(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	keys, err := LoadKeys("frase antiga", dir)
	failIf(t, err != nil, "%v", err)
	path := filepath.Join(dir, "dbmcp.db")

	legacy, _ := NewCipher(keys.Legacy)
	v1, _ := legacy.Encrypt("postgres://u:p@h/db")
	v1 = legacyPrefixV + strings.TrimPrefix(v1, cipherPrefix)
	st, err := Open(path, legacy)
	failIf(t, err != nil, "%v", err)
	conn := model.Connection{Name: "a", Type: model.Postgres, DSN: "x"}
	failIf(t, st.CreateConnection(ctx, &conn) != nil, "create")
	_, err = st.db.ExecContext(ctx, "UPDATE connections SET dsn_enc = ?", v1)
	failIf(t, err != nil, "%v", err)
	_ = st.Close()

	c, _ := NewCipherKeys(keys)
	st, err = Open(path, c)
	failIf(t, err != nil, "open: %v", err)
	defer func() { _ = st.Close() }()
	var stored string
	err = st.db.QueryRowContext(ctx, "SELECT dsn_enc FROM connections").Scan(&stored)
	failIf(t, err != nil || c.Outdated(stored), "stored value not migrated: %q %v", stored, err)
	got, err := st.GetConnection(ctx, conn.ID)
	failIf(t, err != nil || got.DSN != "postgres://u:p@h/db", "after migration: %+v %v", got, err)

	_, err = Open(path, legacy)
	failIf(t, err != nil, "v2 values are not touched by Open: %v", err)
}

func TestDBPathKeepsLegacyFile(t *testing.T) {
	dir := t.TempDir()
	if got := DBPath(dir); filepath.Base(got) != "comporta.db" {
		t.Fatalf("fresh volume: %s", got)
	}
	if err := os.WriteFile(filepath.Join(dir, "dbmcp.db"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if got := DBPath(dir); filepath.Base(got) != "dbmcp.db" {
		t.Fatalf("legacy volume: %s", got)
	}
}

func TestStore(t *testing.T) {
	ctx := context.Background()
	c, _ := NewCipher(make([]byte, 32))
	st, err := Open(filepath.Join(t.TempDir(), "comporta.db"), c)
	failIf(t, err != nil, "%v", err)
	defer func() { _ = st.Close() }()

	conn := model.Connection{Name: "a", Type: model.SQLite, DSN: "/x.db", Enabled: true, MaxRows: 10, TimeoutSec: 5}
	err = st.CreateConnection(ctx, &conn)
	failIf(t, err != nil, "%v", err)
	dup := conn
	err = st.CreateConnection(ctx, &dup)
	failIf(t, !errors.Is(err, ErrDuplicateName), "duplicate: %v", err)
	conn.AllowDelete = true
	err = st.UpdateConnection(ctx, &conn)
	failIf(t, err != nil, "%v", err)
	got, err := st.GetConnection(ctx, conn.ID)
	failIf(t, err != nil || !got.AllowDelete || got.DSN != "/x.db", "get: %+v %v", got, err)
	_, err = st.GetConnection(ctx, 999)
	failIf(t, !errors.Is(err, ErrNotFound), "missing: %v", err)
	missing := model.Connection{ID: 999, Name: "z", Type: model.SQLite}
	err = st.UpdateConnection(ctx, &missing)
	failIf(t, !errors.Is(err, ErrNotFound), "update missing: %v", err)

	old := time.Now().Add(-48 * time.Hour)
	for _, e := range []model.AuditEntry{
		{Time: old, Connection: "a", Tool: "sql_query", Status: model.StatusOK},
		{Connection: "a", Tool: "sql_query", Status: model.StatusDenied, Client: "claude-code", Operation: "delete", Message: "nope"},
		{Connection: "a", Tool: "admin", Status: model.StatusOK},
	} {
		err = st.InsertAudit(ctx, e)
		failIf(t, err != nil, "%v", err)
	}
	stats, err := st.Stats(ctx, time.Now().Add(-24*time.Hour))
	failIf(t, err != nil || stats.Total != 1 || stats.Denied != 1 || stats.Stored != 3, "stats: %+v %v", stats, err)
	act, err := st.ConnectionActivity(ctx, time.Now().Add(-24*time.Hour))
	failIf(t, err != nil || act["a"].Calls != 1 || act["a"].Denied != 1 || act["a"].LastCall.Before(old), "activity: %+v %v", act, err)
	f := AuditFilter{Client: "claude", Operation: "del", Tool: "sql_query", Until: time.Now().Add(time.Minute), Limit: 10}
	if rows, err := st.ListAudit(ctx, f); err != nil || len(rows) != 1 {
		t.Fatalf("filter: %v %v", rows, err)
	}
	if n, err := st.PurgeAudit(ctx, time.Now().Add(-24*time.Hour)); err != nil || n != 1 {
		t.Fatalf("purge: %d %v", n, err)
	}
	err = st.DeleteConnection(ctx, conn.ID)
	failIf(t, err != nil, "%v", err)
	err = st.DeleteConnection(ctx, conn.ID)
	failIf(t, !errors.Is(err, ErrNotFound), "delete twice: %v", err)
}

// failIf stops the test with the message when bad is true.
func failIf(t *testing.T, bad bool, format string, args ...any) {
	t.Helper()
	if bad {
		t.Fatalf(format, args...)
	}
}
