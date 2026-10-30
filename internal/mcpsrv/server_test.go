package mcpsrv

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	_ "modernc.org/sqlite"

	"github.com/slipalison/comporta/internal/db"
	"github.com/slipalison/comporta/internal/model"
	"github.com/slipalison/comporta/internal/store"
)

type fixture struct {
	st     *store.Store
	srv    *Server
	target string
}

// newFixture starts a gateway with a SQLite target holding one table, and
// returns a client session connected through an in-memory transport.
func newFixture(t *testing.T, conn model.Connection) (*fixture, *mcp.ClientSession) {
	t.Helper()
	dir := t.TempDir()
	cipher, err := store.NewCipher(make([]byte, 32))
	failIf(t, err != nil, "%v", err)
	st, err := store.Open(filepath.Join(dir, "comporta.db"), cipher)
	failIf(t, err != nil, "%v", err)
	t.Cleanup(func() { _ = st.Close() })

	target := filepath.Join(dir, "loja.db")
	tdb, err := sql.Open("sqlite", target)
	failIf(t, err != nil, "%v", err)
	if _, err := tdb.ExecContext(context.Background(), `CREATE TABLE pedidos (id INTEGER PRIMARY KEY, cliente TEXT, total REAL);
		INSERT INTO pedidos (cliente, total) VALUES ('ana', 10.5), ('bia', 20);`); err != nil {
		t.Fatal(err)
	}
	_ = tdb.Close()

	if conn.Name != "" {
		if conn.DSN == "" {
			conn.DSN = target
		}
		if err := st.CreateConnection(context.Background(), &conn); err != nil {
			t.Fatal(err)
		}
	}
	reg := db.NewRegistry(st.Path)
	t.Cleanup(reg.Close)
	srv := New(st, reg, "test", 50)
	if err := srv.Refresh(context.Background()); err != nil {
		t.Fatal(err)
	}
	ct, sv := mcp.NewInMemoryTransports()
	if _, err := srv.srv.Connect(context.Background(), sv, nil); err != nil {
		t.Fatal(err)
	}
	client := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "1"}, nil)
	session, err := client.Connect(context.Background(), ct, nil)
	failIf(t, err != nil, "%v", err)
	t.Cleanup(func() { _ = session.Close() })
	return &fixture{st: st, srv: srv, target: target}, session
}

func lojaConn() model.Connection {
	return model.Connection{Name: "loja", Type: model.SQLite, Enabled: true, AllowInsert: true, MaxRows: 100, TimeoutSec: 10,
		Description: "pedidos da loja"}
}

func callTool(t *testing.T, s *mcp.ClientSession, tool string, args map[string]any) (string, bool) {
	t.Helper()
	res, err := s.CallTool(context.Background(), &mcp.CallToolParams{Name: tool, Arguments: args})
	failIf(t, err != nil, "%s: %v", tool, err)
	var b strings.Builder
	for _, c := range res.Content {
		if tc, ok := c.(*mcp.TextContent); ok {
			b.WriteString(tc.Text)
		}
	}
	return b.String(), res.IsError
}

func toolNames(t *testing.T, s *mcp.ClientSession) []string {
	t.Helper()
	res, err := s.ListTools(context.Background(), nil)
	failIf(t, err != nil, "%v", err)
	var names []string
	for _, tool := range res.Tools {
		names = append(names, tool.Name)
	}
	slices.Sort(names)
	return names
}

func TestToolsFollowConnections(t *testing.T) {
	_, s := newFixture(t, model.Connection{})
	if got := toolNames(t, s); !slices.Equal(got, []string{"list_connections"}) {
		t.Fatalf("without connections: %v", got)
	}

	_, s = newFixture(t, lojaConn())
	want := []string{"describe_schema", "list_connections", "sql_execute", "sql_query"}
	if got := toolNames(t, s); !slices.Equal(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}

	ro := lojaConn()
	ro.AllowInsert = false
	_, s = newFixture(t, ro)
	if got := toolNames(t, s); slices.Contains(got, "sql_execute") {
		t.Fatalf("read-only connection must not expose sql_execute: %v", got)
	}
}

func TestSQLQueryAndExecute(t *testing.T) {
	f, s := newFixture(t, lojaConn())

	out, isErr := callTool(t, s, "sql_query", map[string]any{"connection": "loja", "sql": "SELECT cliente FROM pedidos WHERE total > ? ORDER BY id", "params": []any{5}})
	failIf(t, isErr || !strings.Contains(out, `"ana"`) || !strings.Contains(out, `"row_count":2`), "query: %s", out)

	out, isErr = callTool(t, s, "sql_query", map[string]any{"connection": "loja", "sql": "DELETE FROM pedidos"})
	failIf(t, !isErr || !strings.HasPrefix(out, "DENIED"), "a write through sql_query must be denied: %s", out)

	out, isErr = callTool(t, s, "sql_execute", map[string]any{"connection": "loja", "sql": "DELETE FROM pedidos WHERE id = 1"})
	failIf(t, !isErr || !strings.Contains(out, "does not allow delete"), "delete without permission: %s", out)

	out, isErr = callTool(t, s, "sql_execute", map[string]any{"connection": "loja", "sql": "INSERT INTO pedidos (cliente, total) VALUES (?, ?)", "params": []any{"caio", 33}, "dry_run": true})
	failIf(t, isErr || !strings.Contains(out, `"committed":false`), "dry run: %s", out)
	out, isErr = callTool(t, s, "sql_execute", map[string]any{"connection": "loja", "sql": "INSERT INTO pedidos (cliente, total) VALUES (?, ?)", "params": []any{"caio", 33}})
	failIf(t, isErr || !strings.Contains(out, `"committed":true`) || !strings.Contains(out, `"rows_affected":1`), "insert: %s", out)
	out, _ = callTool(t, s, "sql_query", map[string]any{"connection": "loja", "sql": "SELECT COUNT(*) AS n FROM pedidos"})
	failIf(t, !strings.Contains(out, "[[3]]"), "dry run must not persist and the insert must: %s", out)

	out, isErr = callTool(t, s, "sql_execute", map[string]any{"connection": "loja", "sql": "ATTACH DATABASE '" + f.st.Path + "' AS own"})
	failIf(t, !isErr || !strings.Contains(out, "ATTACH is not allowed"), "ATTACH must be refused: %s", out)

	out, isErr = callTool(t, s, "sql_query", map[string]any{"connection": "nada", "sql": "SELECT 1"})
	failIf(t, !isErr || !strings.Contains(out, "Available: loja (sqlite)"), "unknown connection: %s", out)

	entries, err := f.st.ListAudit(context.Background(), store.AuditFilter{Limit: 100})
	failIf(t, err != nil, "%v", err)
	statuses := map[string]int{}
	for _, e := range entries {
		statuses[e.Status]++
	}
	failIf(t, statuses[model.StatusOK] != 4 || statuses[model.StatusDenied] != 3 || statuses[model.StatusError] != 1, "audit statuses = %v", statuses)
}

func TestDescribeAndList(t *testing.T) {
	_, s := newFixture(t, lojaConn())

	out, isErr := callTool(t, s, "list_connections", nil)
	var listed struct {
		Connections []struct {
			Name   string   `json:"name"`
			Tools  []string `json:"use_tools"`
			Denied []string `json:"denied_operations"`
		} `json:"connections"`
	}
	failIf(t, isErr || json.Unmarshal([]byte(out), &listed) != nil || len(listed.Connections) != 1, "list_connections: %s", out)
	if c := listed.Connections[0]; !slices.Contains(c.Tools, "sql_execute") || !slices.Equal(c.Denied, []string{"update", "delete", "ddl"}) {
		t.Fatalf("connection view: %+v", c)
	}

	out, isErr = callTool(t, s, "describe_schema", map[string]any{"connection": "loja"})
	failIf(t, isErr || !strings.Contains(out, "pedidos"), "describe tables: %s", out)
	out, isErr = callTool(t, s, "describe_schema", map[string]any{"connection": "loja", "object": "pedidos"})
	failIf(t, isErr || !strings.Contains(out, "cliente"), "describe columns: %s", out)
	out, isErr = callTool(t, s, "describe_schema", map[string]any{"connection": "loja", "object": "nada"})
	failIf(t, !isErr || !strings.Contains(out, "not found"), "describe missing table: %s", out)
}

func TestHandlerRequiresToken(t *testing.T) {
	f, _ := newFixture(t, lojaConn())
	h := f.srv.Handler(map[string]string{"claude-code": "0123456789abcdef"})
	body := `{"jsonrpc":"2.0","id":1,"method":"tools/list"}`
	for _, tc := range []struct {
		auth string
		want int
	}{{"", http.StatusUnauthorized}, {"Bearer wrong-token-0000000", http.StatusUnauthorized}, {"Bearer 0123456789abcdef", http.StatusOK}} {
		req := httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Accept", "application/json, text/event-stream")
		if tc.auth != "" {
			req.Header.Set("Authorization", tc.auth)
		}
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code != tc.want {
			t.Errorf("auth %q: status %d, want %d (%s)", tc.auth, rec.Code, tc.want, rec.Body.String())
		}
	}
}

func TestLimitKeepsUTF8(t *testing.T) {
	got := limit("ação", 2) // "a" + first byte of "ç"
	failIf(t, got != "a…", "limit cut inside a character: %q", got)
	failIf(t, limit("abc", 5) != "abc", "short strings must be unchanged")
}

func TestSQLParams(t *testing.T) {
	got := sqlParams([]any{json.Number("3"), json.Number("2.5"), map[string]any{"a": 1}, "x", nil})
	if got[0] != int64(3) || got[1] != 2.5 || got[2] != `{"a":1}` || got[3] != "x" || got[4] != nil {
		t.Fatalf("sqlParams = %#v", got)
	}
}

// The MongoDB tools check arguments and permissions before the first round
// trip, so these run without a server: the DSN points at a closed port, and
// a case that reached it would time out instead of returning its error.
func TestMongoGuards(t *testing.T) {
	conn := model.Connection{Name: "eventos", Type: model.MongoDB, DSN: "mongodb://127.0.0.1:1/app",
		Enabled: true, AllowUpdate: true, AllowDelete: true, MaxRows: 10, TimeoutSec: 5}
	_, s := newFixture(t, conn)

	want := []string{"describe_schema", "list_connections", "mongo_aggregate", "mongo_count", "mongo_delete", "mongo_find", "mongo_update"}
	if got := toolNames(t, s); !slices.Equal(got, want) {
		t.Fatalf("tools: %v", got)
	}
	cases := []struct {
		tool string
		args map[string]any
		want string
	}{
		{"mongo_find", map[string]any{"connection": "eventos"}, "collection is required"},
		{"mongo_find", map[string]any{"connection": "nada", "collection": "c"}, "unknown or disabled connection"},
		{"mongo_find", map[string]any{"connection": "eventos", "collection": "c", "filter": "não é objeto"}, "filter:"},
		{"mongo_find", map[string]any{"connection": "eventos", "collection": "c", "sort": map[string]any{"a": map[string]any{"$oid": "zz"}}}, "sort:"},
		{"mongo_aggregate", map[string]any{"connection": "eventos", "collection": "c", "pipeline": []any{map[string]any{"$out": "x"}}}, "DENIED"},
		{"mongo_aggregate", map[string]any{"connection": "eventos", "collection": "c", "pipeline": "não é lista"}, "pipeline:"},
		{"mongo_count", map[string]any{"connection": "eventos", "collection": "c", "filter": map[string]any{"_id": map[string]any{"$oid": "zz"}}}, "filter:"},
		{"mongo_update", map[string]any{"connection": "eventos", "collection": "c", "filter": map[string]any{}, "update": map[string]any{"$set": map[string]any{"a": 1}}}, "empty filter"},
		{"mongo_update", map[string]any{"connection": "eventos", "collection": "c", "filter": map[string]any{"a": 1}, "update": map[string]any{"$set": map[string]any{"a": 2}}, "upsert": true}, "DENIED"},
		{"mongo_update", map[string]any{"connection": "eventos", "collection": "c", "filter": map[string]any{"a": 1}, "update": map[string]any{}}, "non-empty object"},
		{"mongo_delete", map[string]any{"connection": "eventos", "collection": "c", "filter": map[string]any{}}, "empty filter"},
	}
	for _, tc := range cases {
		out, isErr := callTool(t, s, tc.tool, tc.args)
		if !isErr || !strings.Contains(out, tc.want) {
			t.Errorf("%s %v: got %q, want error containing %q", tc.tool, tc.args, out, tc.want)
		}
	}
}

// failIf stops the test with the message when bad is true.
func failIf(t *testing.T, bad bool, format string, args ...any) {
	t.Helper()
	if bad {
		t.Fatalf(format, args...)
	}
}
