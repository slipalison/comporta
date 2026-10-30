package mcpsrv

import (
	"os"
	"strings"
	"testing"

	"github.com/slipalison/comporta/internal/model"
)

// Integration tests against real servers. Each runs only when its variable
// holds a connection string, e.g. with the containers from the README:
//
//	COMPORTA_TEST_ORACLE=oracle://app:pass@127.0.0.1:1521/FREEPDB1
//	COMPORTA_TEST_CLICKHOUSE=clickhouse://app:pass@127.0.0.1:9000/shop
//	COMPORTA_TEST_REDIS=redis://127.0.0.1:6379/0
//
// They expect the `orders` table (and Oracle's `sneaky` function, which
// deletes rows) created as in docs/TESTING.md.

func liveConn(t *testing.T, env string, dbType model.DBType, writes bool) model.Connection {
	t.Helper()
	dsn := os.Getenv(env)
	if dsn == "" {
		t.Skipf("%s not set", env)
	}
	return model.Connection{Name: "live", Type: dbType, DSN: dsn, Enabled: true, MaxRows: 50, TimeoutSec: 20,
		AllowInsert: writes, AllowUpdate: writes, AllowDelete: writes}
}

func expect(t *testing.T, out string, isErr, wantErr bool, want string) {
	t.Helper()
	if isErr != wantErr || !strings.Contains(out, want) {
		t.Fatalf("got isErr=%v %s\nwant isErr=%v and %q", isErr, out, wantErr, want)
	}
}

func TestLiveOracle(t *testing.T) {
	_, s := newFixture(t, liveConn(t, "COMPORTA_TEST_ORACLE", model.Oracle, true))

	out, isErr := callTool(t, s, "sql_query", map[string]any{"connection": "live",
		"sql": "SELECT id, customer, total FROM orders WHERE total > :1 ORDER BY id FETCH FIRST 5 ROWS ONLY;", "params": []any{5}})
	expect(t, out, isErr, false, `"ana"`)

	// A function that deletes, called from a query: the read-only
	// transaction stops it in the database itself.
	out, isErr = callTool(t, s, "sql_query", map[string]any{"connection": "live", "sql": "SELECT sneaky() FROM dual"})
	expect(t, out, isErr, true, "ORA-")

	out, isErr = callTool(t, s, "sql_execute", map[string]any{"connection": "live",
		"sql": "UPDATE orders SET total = total + 1 WHERE id = :1", "params": []any{1}, "dry_run": true})
	expect(t, out, isErr, false, `"committed":false`)

	out, isErr = callTool(t, s, "sql_query", map[string]any{"connection": "live", "sql": "SELECT q'[it's]' AS t FROM dual"})
	expect(t, out, isErr, false, `it's`)

	out, isErr = callTool(t, s, "describe_schema", map[string]any{"connection": "live"})
	expect(t, out, isErr, false, "ORDERS")
	out, isErr = callTool(t, s, "describe_schema", map[string]any{"connection": "live", "object": "orders"})
	expect(t, out, isErr, false, "CUSTOMER")
}

func TestLiveClickHouse(t *testing.T) {
	_, s := newFixture(t, liveConn(t, "COMPORTA_TEST_CLICKHOUSE", model.ClickHouse, true))

	out, isErr := callTool(t, s, "sql_query", map[string]any{"connection": "live",
		"sql": "SELECT id, customer, tags, note FROM orders WHERE total > ? ORDER BY id", "params": []any{5}})
	expect(t, out, isErr, false, `["vip"]`)

	// readonly=1: the server refuses a write even if it reached it.
	out, isErr = callTool(t, s, "sql_query", map[string]any{"connection": "live",
		"sql": "SELECT * FROM orders SETTINGS readonly = 0"})
	expect(t, out, isErr, true, "readonly")

	out, isErr = callTool(t, s, "sql_execute", map[string]any{"connection": "live",
		"sql": "INSERT INTO orders (id, customer, total) VALUES (?, ?, ?)", "params": []any{3, "caio", 33}, "dry_run": true})
	expect(t, out, isErr, true, "dry_run is not available")

	out, isErr = callTool(t, s, "describe_schema", map[string]any{"connection": "live", "object": "orders"})
	expect(t, out, isErr, false, "customer")
}

func TestLiveRedis(t *testing.T) {
	_, s := newFixture(t, liveConn(t, "COMPORTA_TEST_REDIS", model.Redis, false))

	out, isErr := callTool(t, s, "redis_query", map[string]any{"connection": "live", "command": "GET", "args": []any{"user:1"}})
	expect(t, out, isErr, false, `"value":"ana"`)
	out, isErr = callTool(t, s, "redis_query", map[string]any{"connection": "live", "command": "SET", "args": []any{"user:1", "x"}})
	expect(t, out, isErr, true, "DENIED")
	out, isErr = callTool(t, s, "describe_schema", map[string]any{"connection": "live", "object": "user:*"})
	expect(t, out, isErr, false, `"key":"user:1"`)
}
