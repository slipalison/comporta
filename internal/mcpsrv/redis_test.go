package mcpsrv

import (
	"strings"
	"testing"

	"github.com/alicebob/miniredis/v2"

	"github.com/slipalison/comporta/internal/model"
)

func redisConn(t *testing.T, ops ...model.Op) (model.Connection, *miniredis.Miniredis) {
	t.Helper()
	mr := miniredis.RunT(t)
	_ = mr.Set("user:1", "ana")
	_ = mr.Set("user:2", "bia")
	mr.HSet("session:42", "user", "1", "ttl", "30")
	for i := range 30 {
		_, _ = mr.RPush("events", strings.Repeat("e", i+1))
	}
	c := model.Connection{Name: "cache", Type: model.Redis, DSN: "redis://" + mr.Addr() + "/0", Enabled: true, MaxRows: 10, TimeoutSec: 5}
	for _, op := range ops {
		switch op {
		case model.OpInsert:
			c.AllowInsert = true
		case model.OpUpdate:
			c.AllowUpdate = true
		case model.OpDelete:
			c.AllowDelete = true
		case model.OpDDL:
			c.AllowDDL = true
		}
	}
	return c, mr
}

func TestRedisReadOnly(t *testing.T) {
	c, _ := redisConn(t)
	_, s := newFixture(t, c)

	want := []string{"describe_schema", "list_connections", "redis_query"}
	if got := toolNames(t, s); strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("tools: %v", got)
	}
	cases := []struct {
		cmd     string
		args    []any
		wantErr bool
		want    string
	}{
		{"get", []any{"user:1"}, false, `"value":"ana"`},
		{"GET", []any{"missing"}, false, `"value":null`},
		{"HGETALL", []any{"session:42"}, false, `"user":"1"`},
		{"LRANGE", []any{"events", "0", "-1"}, false, `"truncated":true`},
		{"SCAN", []any{"0", "MATCH", "user:*", "COUNT", "100"}, false, "user:1"},
		{"SET", []any{"user:1", "x"}, true, "DENIED: redis_query only runs read-only commands"},
		{"KEYS", []any{"*"}, true, "use SCAN"},
		{"GET user:1", nil, true, "only the command name"},
		{"HGET", []any{"user:1", "x"}, true, "WRONGTYPE"},
	}
	for _, tc := range cases {
		out, isErr := callTool(t, s, "redis_query", map[string]any{"connection": "cache", "command": tc.cmd, "args": tc.args})
		if isErr != tc.wantErr || !strings.Contains(out, tc.want) {
			t.Errorf("%s %v: isErr=%v %s, want %q", tc.cmd, tc.args, isErr, out, tc.want)
		}
	}

	out, isErr := callTool(t, s, "describe_schema", map[string]any{"connection": "cache", "object": "user:*"})
	if isErr || !strings.Contains(out, `"total_keys":4`) || !strings.Contains(out, `"key":"user:2","type":"string"`) {
		t.Fatalf("describe: %s", out)
	}
	out, isErr = callTool(t, s, "list_connections", nil)
	if isErr || !strings.Contains(out, `"use_tools":["describe_schema","redis_query"]`) {
		t.Fatalf("list: %s", out)
	}
}

func TestRedisWrites(t *testing.T) {
	c, mr := redisConn(t, model.OpInsert, model.OpUpdate)
	_, s := newFixture(t, c)

	out, isErr := callTool(t, s, "redis_execute", map[string]any{"connection": "cache", "command": "SET", "args": []any{"user:3", "caio"}})
	if isErr || !strings.Contains(out, `"value":"OK"`) {
		t.Fatalf("SET: %s", out)
	}
	if v, _ := mr.Get("user:3"); v != "caio" {
		t.Fatalf("user:3 = %q", v)
	}
	out, isErr = callTool(t, s, "redis_execute", map[string]any{"connection": "cache", "command": "DEL", "args": []any{"user:1"}})
	if !isErr || !strings.Contains(out, "does not allow delete") {
		t.Fatalf("DEL without permission: %s", out)
	}
	out, isErr = callTool(t, s, "redis_execute", map[string]any{"connection": "cache", "command": "FLUSHALL"})
	if !isErr || !strings.Contains(out, "does not allow ddl") || !mr.Exists("user:1") {
		t.Fatalf("FLUSHALL without permission: %s", out)
	}
	out, isErr = callTool(t, s, "redis_execute", map[string]any{"connection": "cache", "command": "INCR", "args": []any{"counter"}})
	if isErr || !strings.Contains(out, `"value":1`) {
		t.Fatalf("INCR: %s", out)
	}
}

func TestToolHints(t *testing.T) {
	for typ, want := range map[model.DBType]string{
		model.Oracle: "sql_query", model.ClickHouse: "sql_query", model.Redis: "redis_query", model.MongoDB: "mongo_find",
	} {
		if got := toolHint(model.Connection{Type: typ}); !strings.Contains(got, want) {
			t.Errorf("%s: %q", typ, got)
		}
	}
	if got := toolsFor(model.Connection{Type: model.Redis, AllowDelete: true}); strings.Join(got, ",") != "describe_schema,redis_query,redis_execute" {
		t.Errorf("toolsFor redis: %v", got)
	}
	if got := trimStatement(" SELECT 1 FROM dual;; \n"); got != "SELECT 1 FROM dual" {
		t.Errorf("trimStatement: %q", got)
	}
}
