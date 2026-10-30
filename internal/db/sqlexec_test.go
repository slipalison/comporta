package db

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"path/filepath"
	"slices"
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
		{math.Inf(1), "+Inf"}, {now, "2026-01-02T03:04:05Z"}, {[]byte("oi"), "oi"}, {uint8(7), uint8(7)}, {(*string)(nil), nil}, {ptr("x"), "x"},
	}
	for _, tc := range cases {
		if got := jsonValue(tc.in, ""); got != tc.want {
			t.Errorf("jsonValue(%#v) = %#v, want %#v", tc.in, got, tc.want)
		}
	}
	if got := jsonValue("10.50", "NUMBER"); got != json.Number("10.50") {
		t.Errorf("Oracle NUMBER: %#v", got)
	}
	if got := jsonValue(".5", "NUMBER"); got != ".5" {
		t.Errorf("text that is not a JSON number stays text: %#v", got)
	}
	if got := jsonValue("abc", "NUMBER"); got != "abc" {
		t.Errorf("non-numeric text in a NUMBER column: %#v", got)
	}
	if got := jsonValue([]string{"a", "b"}, ""); string(got.(json.RawMessage)) != `["a","b"]` {
		t.Errorf("array cell: %#v", got)
	}
	if got := jsonValue(struct{ A int }{1}, ""); string(got.(json.RawMessage)) != `{"A":1}` {
		t.Errorf("struct cell: %#v", got)
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
	for _, dbType := range []model.DBType{model.Postgres, model.SQLServer, model.MySQL, model.SQLite, model.Oracle, model.ClickHouse} {
		for _, schema := range []string{"", "vendas"} {
			q, args, err := tablesQuery(dbType, schema)
			failIf(t, err != nil || strings.Contains(q, "vendas"), "%s tables: %v %q", dbType, err, q)
			failIf(t, schema != "" && dbType != model.SQLite && !slices.Contains(args, any(schema)), "%s tables args: %v", dbType, args)
			q, args, err = columnsQuery(dbType, schema, "pedidos")
			failIf(t, err != nil || strings.Contains(q, "pedidos") || len(args) == 0, "%s columns: %v %q", dbType, err, q)
		}
	}
	_, _, err := tablesQuery(model.Redis, "")
	failIf(t, err == nil, "Redis has no SQL schema query")
	_, _, err = tablesQuery(model.MongoDB, "")
	failIf(t, err == nil, "MongoDB has no SQL schema query")
	_, _, err = columnsQuery(model.MongoDB, "", "x")
	failIf(t, err == nil, "MongoDB has no SQL schema query")
}

func ptr[T any](v T) *T { return &v }

// ClickHouse has no transactions to roll back, so dry_run is refused before
// anything reaches the server.
func TestClickHouseNoDryRun(t *testing.T) {
	_, err := RunSQL(context.Background(), nil, model.ClickHouse, SQLRequest{Query: "INSERT INTO t VALUES (1)", Write: true, DryRun: true})
	failIf(t, !errors.Is(err, ErrNoDryRun), "got %v", err)
	failIf(t, model.ClickHouse.Transactional() || !model.Oracle.Transactional() || model.Redis.Transactional(), "Transactional")
}

func TestValidateNewTypes(t *testing.T) {
	r := NewRegistry("/data/comporta.db")
	for _, c := range []model.Connection{
		{Type: model.Oracle, DSN: "oracle://u:p@db:1521/FREEPDB1"},
		{Type: model.ClickHouse, DSN: "clickhouse://u:p@ch:9000/shop"},
		{Type: model.Redis, DSN: "redis://cache:6379/0"},
	} {
		failIf(t, r.Validate(c) != nil, "%s: %v", c.Type, r.Validate(c))
	}
	for _, c := range []model.Connection{
		{Type: model.ClickHouse, DSN: "clickhouse://u:p@ch:9000/shop?dial_timeout=abc"},
		{Type: model.Redis, DSN: "http://cache"},
	} {
		failIf(t, r.Validate(c) == nil, "%s %q must be invalid", c.Type, c.DSN)
	}
	_, err := r.Redis(model.Connection{Type: model.Postgres})
	failIf(t, err == nil, "Postgres is not Redis")
	failIf(t, MaskDSN("oracle://app:s3cr3t@db:1521/x") != "oracle://app:****@db:1521/x", "oracle DSN mask")
}
