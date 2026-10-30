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
	k1, err := LoadKeys("", "", dir)
	failIf(t, err != nil || len(k1.Current) != 32 || k1.Legacy != nil, "generated key: %v %+v", err, k1)
	k2, err := LoadKeys("", "", dir)
	failIf(t, err != nil || string(k1.Current) != string(k2.Current), "the generated key must be reused on the next start")
	info, err := os.Stat(filepath.Join(dir, "secret.key"))
	failIf(t, err != nil || info.Mode().Perm() != 0o600, "key file: %v %v", info, err)

	fromSecret, err := LoadKeys("uma frase", "", dir)
	failIf(t, err != nil || string(fromSecret.Current) == string(k1.Current), "an explicit secret must win over the file: %v", err)
	again, _ := LoadKeys("uma frase", "", dir)
	failIf(t, string(again.Current) != string(fromSecret.Current), "the salt must be reused on the next start")
	otherDir, _ := LoadKeys("uma frase", "", t.TempDir())
	failIf(t, string(otherDir.Current) == string(fromSecret.Current), "each volume gets its own salt")

	err = os.WriteFile(filepath.Join(dir, "secret.key"), []byte("curta"), 0o600)
	failIf(t, err != nil, "%v", err)
	_, err = LoadKeys("", "", dir)
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

// A volume from db-mcp that ran with a secret: its v1 values are opened with
// COMPORTA_LEGACY_KEY and sealed again as v2 when the store opens. Without
// that key, Open refuses and says how to provide it.
func TestOpenReencryptsLegacyValues(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	const legacyHex = "8f2a6c1d9e0b4f3a7c5d2e1f0a9b8c7d6e5f4a3b2c1d0e9f8a7b6c5d4e3f2a1b"
	keys, err := LoadKeys("frase antiga", legacyHex, dir)
	failIf(t, err != nil, "%v", err)
	path := filepath.Join(dir, "dbmcp.db")

	legacy, _ := NewCipher(keys.Legacy)
	v1, _ := legacy.Encrypt("postgres://u:p@h/db")
	v1 = legacyPrefixV + strings.TrimPrefix(v1, cipherPrefix)
	st, err := Open(path, legacy)
	failIf(t, err != nil, "%v", err)
	conn := model.Connection{Name: "a", Type: model.Postgres, DSN: "x"}
	err = st.CreateConnection(ctx, &conn)
	failIf(t, err != nil, "%v", err)
	_, err = st.db.ExecContext(ctx, "UPDATE connections SET dsn_enc = ?", v1)
	failIf(t, err != nil, "%v", err)
	_ = st.Close()

	noLegacy, _ := LoadKeys("frase antiga", "", dir)
	c, _ := NewCipherKeys(noLegacy)
	_, err = Open(path, c)
	failIf(t, err == nil || !strings.Contains(err.Error(), "COMPORTA_LEGACY_KEY"), "without the legacy key: %v", err)

	c, _ = NewCipherKeys(keys)
	st, err = Open(path, c)
	failIf(t, err != nil, "open: %v", err)
	defer func() { _ = st.Close() }()
	var stored string
	err = st.db.QueryRowContext(ctx, "SELECT dsn_enc FROM connections").Scan(&stored)
	failIf(t, err != nil || c.Outdated(stored), "stored value not migrated: %q %v", stored, err)
	got, err := st.GetConnection(ctx, conn.ID)
	failIf(t, err != nil || got.DSN != "postgres://u:p@h/db", "after migration: %+v %v", got, err)

	// Migrated: the legacy key is no longer needed.
	c, _ = NewCipherKeys(noLegacy)
	st2, err := Open(path, c)
	failIf(t, err != nil, "after migration the legacy key is not needed: %v", err)
	_ = st2.Close()

	_, err = LoadKeys("x", "curta", dir)
	failIf(t, err == nil, "an invalid legacy key must be an error")
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
	var dupErr *DuplicateNameError
	failIf(t, !errors.As(err, &dupErr) || dupErr.Name != "a", "duplicate: %v", err)
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

func TestMountedAt(t *testing.T) {
	info := `22 1 0:21 / / rw,relatime - overlay overlay rw
23 22 0:22 / /proc rw - proc proc rw
30 22 8:1 /var/lib/docker/volumes/comporta-data/_data /data rw,relatime - ext4 /dev/sda1 rw
31 22 8:1 /home/me/my\040files /srv/my\040files rw - ext4 /dev/sda1 rw
`
	failIf(t, !mountedAt(strings.NewReader(info), "/data"), "/data is a mount")
	failIf(t, !mountedAt(strings.NewReader(info), "/data/sub"), "a folder inside a mount is persistent")
	failIf(t, !mountedAt(strings.NewReader(info), "/srv/my files"), "escaped spaces")
	failIf(t, mountedAt(strings.NewReader(info), "/database"), "/database is not under /data")
	failIf(t, mountedAt(strings.NewReader(info), "/app/data"), "only / covers it: not a volume")
}

func TestCheckWritable(t *testing.T) {
	dir := t.TempDir()
	failIf(t, CheckWritable(dir) != nil, "a temp dir is writable")
	entries, _ := os.ReadDir(dir)
	failIf(t, len(entries) != 0, "the probe file must be removed")
	ro := filepath.Join(dir, "ro")
	failIf(t, os.Mkdir(ro, 0o500) != nil, "mkdir")
	t.Cleanup(func() { _ = os.Chmod(ro, 0o700) })
	if os.Getuid() == 0 {
		t.Skip("root writes anywhere")
	}
	err := CheckWritable(ro)
	failIf(t, err == nil || !strings.Contains(err.Error(), "chown -R 65532:65532"), "read-only dir: %v", err)
	failIf(t, CheckWritable(filepath.Join(dir, "missing")) == nil, "a missing dir is an error")
	st := DetectStorage(dir)
	failIf(t, !st.Persistent || st.Path != dir, "outside a container the disk is persistent: %+v", st)
}
