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

func TestLoadKeyAndCipher(t *testing.T) {
	dir := t.TempDir()
	k1, err := LoadKey("", dir)
	if err != nil || len(k1) != 32 {
		t.Fatalf("generated key: %v %d", err, len(k1))
	}
	k2, err := LoadKey("", dir)
	if err != nil || string(k1) != string(k2) {
		t.Fatal("the generated key must be reused on the next start")
	}
	if info, _ := os.Stat(filepath.Join(dir, "secret.key")); info.Mode().Perm() != 0o600 {
		t.Fatalf("key file mode %v", info.Mode())
	}
	fromSecret, _ := LoadKey("uma frase", dir)
	if string(fromSecret) == string(k1) {
		t.Fatal("an explicit secret must win over the file")
	}
	if err := os.WriteFile(filepath.Join(dir, "secret.key"), []byte("curta"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadKey("", dir); err == nil {
		t.Fatal("a corrupt key file must be an error")
	}

	c, _ := NewCipher(k1)
	enc, err := c.Encrypt("postgres://u:p@h/db")
	if err != nil || !strings.HasPrefix(enc, cipherPrefix) || strings.Contains(enc, "p@h") {
		t.Fatalf("encrypt: %q %v", enc, err)
	}
	if plain, err := c.Decrypt(enc); err != nil || plain != "postgres://u:p@h/db" {
		t.Fatalf("decrypt: %q %v", plain, err)
	}
	other, _ := NewCipher(fromSecret)
	if _, err := other.Decrypt(enc); err == nil || !strings.Contains(err.Error(), "COMPORTA_SECRET_KEY") {
		t.Fatalf("wrong key: %v", err)
	}
	if _, err := c.Decrypt("v1:AA=="); err == nil {
		t.Fatal("short ciphertext must fail")
	}
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
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = st.Close() }()

	conn := model.Connection{Name: "a", Type: model.SQLite, DSN: "/x.db", Enabled: true, MaxRows: 10, TimeoutSec: 5}
	if err := st.CreateConnection(ctx, &conn); err != nil {
		t.Fatal(err)
	}
	dup := conn
	if err := st.CreateConnection(ctx, &dup); !errors.Is(err, ErrDuplicateName) {
		t.Fatalf("duplicate: %v", err)
	}
	conn.AllowDelete = true
	if err := st.UpdateConnection(ctx, &conn); err != nil {
		t.Fatal(err)
	}
	got, err := st.GetConnection(ctx, conn.ID)
	if err != nil || !got.AllowDelete || got.DSN != "/x.db" {
		t.Fatalf("get: %+v %v", got, err)
	}
	if _, err := st.GetConnection(ctx, 999); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing: %v", err)
	}
	missing := model.Connection{ID: 999, Name: "z", Type: model.SQLite}
	if err := st.UpdateConnection(ctx, &missing); !errors.Is(err, ErrNotFound) {
		t.Fatalf("update missing: %v", err)
	}

	old := time.Now().Add(-48 * time.Hour)
	for _, e := range []model.AuditEntry{
		{Time: old, Connection: "a", Tool: "sql_query", Status: model.StatusOK},
		{Connection: "a", Tool: "sql_query", Status: model.StatusDenied, Client: "claude-code", Operation: "delete", Message: "nope"},
		{Connection: "a", Tool: "admin", Status: model.StatusOK},
	} {
		if err := st.InsertAudit(ctx, e); err != nil {
			t.Fatal(err)
		}
	}
	stats, err := st.Stats(ctx, time.Now().Add(-24*time.Hour))
	if err != nil || stats.Total != 1 || stats.Denied != 1 || stats.Stored != 3 {
		t.Fatalf("stats: %+v %v", stats, err)
	}
	act, err := st.ConnectionActivity(ctx, time.Now().Add(-24*time.Hour))
	if err != nil || act["a"].Calls != 1 || act["a"].Denied != 1 || act["a"].LastCall.Before(old) {
		t.Fatalf("activity: %+v %v", act, err)
	}
	f := AuditFilter{Client: "claude", Operation: "del", Tool: "sql_query", Until: time.Now().Add(time.Minute), Limit: 10}
	if rows, err := st.ListAudit(ctx, f); err != nil || len(rows) != 1 {
		t.Fatalf("filter: %v %v", rows, err)
	}
	if n, err := st.PurgeAudit(ctx, time.Now().Add(-24*time.Hour)); err != nil || n != 1 {
		t.Fatalf("purge: %d %v", n, err)
	}
	if err := st.DeleteConnection(ctx, conn.ID); err != nil {
		t.Fatal(err)
	}
	if err := st.DeleteConnection(ctx, conn.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("delete twice: %v", err)
	}
}
