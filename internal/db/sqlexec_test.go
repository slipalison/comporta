package db

import (
	"context"
	"math"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/slipalison/comporta/internal/model"
)

func TestRunSQLOnSQLite(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	r := NewRegistry(filepath.Join(dir, "comporta.db"))
	defer r.Close()
	c := model.Connection{ID: 1, Name: "loja", Type: model.SQLite, DSN: filepath.Join(dir, "loja.db")}
	if err := r.Test(ctx, c); err != nil {
		t.Fatal(err)
	}
	pool, err := r.SQL(c)
	failIf(t, err != nil, "%v", err)
	if _, err := RunSQL(ctx, pool, c.Type, SQLRequest{Query: "CREATE TABLE t (id INTEGER, nome TEXT, foto BLOB)", Write: true}); err != nil {
		t.Fatal(err)
	}
	res, err := RunSQL(ctx, pool, c.Type, SQLRequest{Query: "INSERT INTO t VALUES (1, 'a', x'ff00'), (2, 'b', NULL), (3, 'c', NULL)", Write: true, DryRun: true})
	failIf(t, err != nil || *res.Committed || *res.RowsAffected != 3, "dry run: %+v %v", res, err)
	if _, err := RunSQL(ctx, pool, c.Type, SQLRequest{Query: "INSERT INTO t VALUES (1, 'a', x'ff00'), (2, 'b', NULL), (3, 'c', NULL)", Write: true}); err != nil {
		t.Fatal(err)
	}
	res, err = RunSQL(ctx, pool, c.Type, SQLRequest{Query: "SELECT * FROM t ORDER BY id", MaxRows: 2})
	failIf(t, err != nil || res.RowCount != 2 || !res.Truncated || res.Columns[1].Name != "nome", "read: %+v %v", res, err)
	if got := res.Rows[0][2]; got != "base64:/wA=" {
		t.Fatalf("binary cell: %#v", got)
	}
	// A write smuggled into a read is rolled back.
	if _, err := RunSQL(ctx, pool, c.Type, SQLRequest{Query: "DELETE FROM t RETURNING id", MaxRows: 10}); err != nil {
		t.Fatal(err)
	}
	res, _ = RunSQL(ctx, pool, c.Type, SQLRequest{Query: "SELECT COUNT(*) FROM t", MaxRows: 1})
	failIf(t, res.Rows[0][0] != int64(3), "read transaction must roll back: %v", res.Rows)

	tables, err := DescribeSQL(ctx, pool, c.Type, "", "")
	failIf(t, err != nil || tables.RowCount != 1, "describe: %+v %v", tables, err)
	cols, err := DescribeSQL(ctx, pool, c.Type, "", "t")
	failIf(t, err != nil || cols.RowCount != 3, "describe columns: %+v %v", cols, err)

	// Editing the DSN replaces the pool; Forget closes it.
	c.DSN = filepath.Join(dir, "outro.db")
	if p2, _ := r.SQL(c); p2 == pool {
		t.Fatal("a changed DSN must open a new pool")
	}
	r.Forget(c.ID)
	if _, err := r.Mongo(c); err == nil {
		t.Fatal("a SQL connection is not MongoDB")
	}
}

func TestJSONValue(t *testing.T) {
	now := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	cases := []struct {
		in   any
		want any
	}{
		{nil, nil}, {int64(1), int64(1)}, {true, true}, {1.5, 1.5}, {float32(2), 2.0},
		{math.Inf(1), "+Inf"}, {now, "2026-01-02T03:04:05Z"}, {[]byte("oi"), "oi"}, {uint8(7), "7"},
	}
	for _, tc := range cases {
		if got := jsonValue(tc.in, ""); got != tc.want {
			t.Errorf("jsonValue(%#v) = %#v, want %#v", tc.in, got, tc.want)
		}
	}
	long := strings.Repeat("é", MaxCellChars)
	got := truncate(long)
	failIf(t, !utf8.ValidString(got) || !strings.Contains(got, "truncated"), "truncate broke UTF-8 or did not mark the cut")
}

func TestValidateAndMongoDatabase(t *testing.T) {
	r := NewRegistry("/data/comporta.db")
	if err := r.Validate(model.Connection{Type: model.MongoDB, DSN: "mongodb://h:27017/app"}); err != nil {
		t.Fatal(err)
	}
	if err := r.Validate(model.Connection{Type: model.MongoDB, DSN: "http://h"}); err == nil {
		t.Fatal("invalid mongo URI accepted")
	}
	if got := MongoDatabase(model.Connection{DSN: "mongodb://h:27017/app"}); got != "app" {
		t.Fatalf("database from URI: %q", got)
	}
	if got := MongoDatabase(model.Connection{DSN: "mongodb://h:27017/app", Database: "outro"}); got != "outro" {
		t.Fatalf("explicit database: %q", got)
	}
	if _, err := r.SQL(model.Connection{Type: model.MongoDB}); err == nil {
		t.Fatal("MongoDB is not SQL")
	}
}

// failIf stops the test with the message when bad is true.
func failIf(t *testing.T, bad bool, format string, args ...any) {
	t.Helper()
	if bad {
		t.Fatalf(format, args...)
	}
}

// The schema queries of every dialect take the schema filter as a bound
// parameter, never as text.
func TestSchemaQueries(t *testing.T) {
	for _, dbType := range []model.DBType{model.Postgres, model.SQLServer, model.MySQL, model.SQLite} {
		for _, schema := range []string{"", "vendas"} {
			q, args, err := tablesQuery(dbType, schema)
			failIf(t, err != nil || strings.Contains(q, "vendas"), "%s tables: %v %q", dbType, err, q)
			failIf(t, schema != "" && dbType != model.SQLite && len(args) != 1, "%s tables args: %v", dbType, args)
			q, args, err = columnsQuery(dbType, schema, "pedidos")
			failIf(t, err != nil || strings.Contains(q, "pedidos") || len(args) == 0, "%s columns: %v %q", dbType, err, q)
		}
	}
	_, _, err := tablesQuery(model.MongoDB, "")
	failIf(t, err == nil, "MongoDB has no SQL schema query")
	_, _, err = columnsQuery(model.MongoDB, "", "x")
	failIf(t, err == nil, "MongoDB has no SQL schema query")
}
