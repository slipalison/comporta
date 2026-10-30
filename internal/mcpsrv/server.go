// Package mcpsrv exposes the configured connections as MCP tools. The tool
// list is rebuilt whenever connections change, so each tool only offers the
// connections (and database types) it can actually be used with.
package mcpsrv

import (
	"bytes"
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/google/jsonschema-go/jsonschema"
	"github.com/modelcontextprotocol/go-sdk/auth"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/slipalison/comporta/internal/db"
	"github.com/slipalison/comporta/internal/model"
	"github.com/slipalison/comporta/internal/store"
)

const instructions = `This server is a permission-controlled gateway to several databases (PostgreSQL, SQL Server, MySQL/MariaDB, SQLite and MongoDB). Every call is audited.

How to work with it:
1. Call list_connections to see each connection's name, database type, SQL dialect, placeholder style and allowed operations. Pick the connection whose description matches the user's request; if more than one could fit, ask the user.
2. Call describe_schema to discover tables/collections and their columns/fields before writing queries.
3. SQL connections (postgres, sqlserver, mysql, sqlite, oracle, clickhouse): use sql_query for reads and sql_execute for writes, written in that connection's dialect. MongoDB connections: use the mongo_* tools with MongoDB Extended JSON, never SQL. Redis connections: use redis_query for reads and redis_execute for writes, one command per call.
4. Operations that are not enabled on a connection are refused with a DENIED message. Do not try to work around a denial; tell the user which permission is missing so they can enable it in the gateway's admin UI.`

const maxAuditText = 20000

type Server struct {
	srv    *mcp.Server
	store  *store.Store
	reg    *db.Registry
	maxCap int

	mu    sync.Mutex
	tools []string
}

func New(st *store.Store, reg *db.Registry, version string, maxCap int) *Server {
	return &Server{
		srv: mcp.NewServer(&mcp.Implementation{Name: "comporta", Title: "Comporta", Version: version},
			&mcp.ServerOptions{Instructions: instructions}),
		store:  st,
		reg:    reg,
		maxCap: maxCap,
	}
}

// Handler serves MCP over streamable HTTP. Each bearer token maps to a client
// label recorded in the audit log.
func (s *Server) Handler(tokens map[string]string) http.Handler {
	h := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return s.srv },
		&mcp.StreamableHTTPOptions{Stateless: true, JSONResponse: true})
	verify := func(_ context.Context, token string, _ *http.Request) (*auth.TokenInfo, error) {
		for label, want := range tokens {
			if subtle.ConstantTimeCompare([]byte(token), []byte(want)) == 1 {
				return &auth.TokenInfo{UserID: label, Expiration: time.Now().Add(time.Hour)}, nil
			}
		}
		return nil, auth.ErrInvalidToken
	}
	return auth.RequireBearerToken(verify, nil)(h)
}

type toolDef struct {
	tool    *mcp.Tool
	handler mcp.ToolHandler
}

// Refresh re-registers the tools from the current connections.
func (s *Server) Refresh(ctx context.Context) error {
	conns, err := s.store.ListConnections(ctx)
	if err != nil {
		return err
	}
	var enabled []model.Connection
	for _, c := range conns {
		if c.Enabled {
			enabled = append(enabled, c)
		}
	}
	defs := s.buildTools(enabled)

	s.mu.Lock()
	defer s.mu.Unlock()
	keep := map[string]bool{}
	var names []string
	for _, d := range defs {
		s.srv.AddTool(d.tool, d.handler)
		keep[d.tool.Name] = true
		names = append(names, d.tool.Name)
	}
	var stale []string
	for _, name := range s.tools {
		if !keep[name] {
			stale = append(stale, name)
		}
	}
	if len(stale) > 0 {
		s.srv.RemoveTools(stale...)
	}
	s.tools = names
	slog.Info("mcp tools refreshed", "connections", len(enabled), "tools", names)
	return nil
}

func filter(conns []model.Connection, keep func(model.Connection) bool) []model.Connection {
	var out []model.Connection
	for _, c := range conns {
		if keep(c) {
			out = append(out, c)
		}
	}
	return out
}

func opsText(ops []model.Op) string {
	parts := make([]string, len(ops))
	for i, op := range ops {
		parts[i] = string(op)
	}
	return strings.Join(parts, ", ")
}

// catalog renders one line per connection for tool descriptions.
func catalog(conns []model.Connection) string {
	var b strings.Builder
	for _, c := range conns {
		info := c.Type.Info()
		fmt.Fprintf(&b, "\n- %s: %s (type %s)", c.Name, info.Label, c.Type)
		if info.Placeholder != "" {
			fmt.Fprintf(&b, ", params %s", info.Placeholder)
		}
		if c.Description != "" {
			fmt.Fprintf(&b, " — %s", c.Description)
		}
		fmt.Fprintf(&b, " [allowed: %s]", opsText(c.AllowedOps()))
	}
	return b.String()
}

func connParam(conns []model.Connection, purpose string) *jsonschema.Schema {
	names := make([]any, len(conns))
	for i, c := range conns {
		names[i] = c.Name
	}
	return &jsonschema.Schema{Type: "string", Enum: names, Description: purpose + catalog(conns)}
}

func str(desc string) *jsonschema.Schema {
	return &jsonschema.Schema{Type: "string", Description: desc}
}
func boolean(desc string) *jsonschema.Schema {
	return &jsonschema.Schema{Type: "boolean", Description: desc}
}
func object(desc string) *jsonschema.Schema {
	return &jsonschema.Schema{Type: "object", Description: desc}
}
func integer(desc string, maxValue int) *jsonschema.Schema {
	return &jsonschema.Schema{Type: "integer", Description: desc, Minimum: jsonschema.Ptr(0.0), Maximum: jsonschema.Ptr(float64(maxValue))}
}
func input(required []string, props map[string]*jsonschema.Schema) *jsonschema.Schema {
	return &jsonschema.Schema{Type: "object", Properties: props, Required: required}
}

func readOnly(title string) *mcp.ToolAnnotations {
	return &mcp.ToolAnnotations{Title: title, ReadOnlyHint: true, OpenWorldHint: jsonschema.Ptr(false)}
}
func destructive(title string) *mcp.ToolAnnotations {
	return &mcp.ToolAnnotations{Title: title, DestructiveHint: jsonschema.Ptr(true), OpenWorldHint: jsonschema.Ptr(false)}
}

func (s *Server) buildTools(conns []model.Connection) []toolDef {
	defs := []toolDef{{&mcp.Tool{
		Name: "list_connections",
		Description: "List the database connections this gateway exposes: name, database type (postgres, sqlserver, mysql, sqlite, mongodb), " +
			"SQL dialect hints, parameter placeholder style, description, allowed/denied operations and which tools to use with each. " +
			"Call this first when unsure which connection or tool fits the request.",
		InputSchema: input(nil, map[string]*jsonschema.Schema{}),
		Annotations: readOnly("List database connections"),
	}, s.handleListConnections}}
	if len(conns) == 0 {
		return defs
	}

	defs = append(defs, toolDef{&mcp.Tool{
		Name: "describe_schema",
		Description: "Describe the structure of a connection. Without `object`: lists tables and views (SQL), collections (MongoDB) or a sample of keys (Redis). " +
			"With `object`: lists the columns of that table (SQL) or infers fields and types from sampled documents plus indexes (MongoDB). " +
			"Use it before writing queries.",
		InputSchema: input([]string{"connection"}, map[string]*jsonschema.Schema{
			"connection":  connParam(conns, "Connection to describe. One of:"),
			"object":      str("Optional table name (SQL), collection name (MongoDB) or key pattern such as user:* (Redis)."),
			"schema":      str("Optional SQL schema to filter by (e.g. public, dbo). Ignored for MongoDB."),
			"database":    str("MongoDB only: database name, when different from the connection default."),
			"sample_size": integer("MongoDB: documents to sample when describing a collection (default 20). Redis: keys to sample (default 50).", 200),
		}),
		Annotations: readOnly("Describe database schema"),
	}, s.handleDescribe})

	sqlConns := filter(conns, func(c model.Connection) bool { return c.Type.IsSQL() })
	if len(sqlConns) > 0 {
		defs = append(defs, toolDef{&mcp.Tool{
			Name: "sql_query",
			Description: "Run ONE read-only SQL statement (SELECT, WITH, SHOW, EXPLAIN, ...) on a SQL connection and get the rows back as JSON " +
				"{columns, rows, row_count, truncated}. Write the SQL in the dialect of the connection's database type " +
				"(e.g. SELECT TOP (10) on sqlserver, LIMIT 10 on postgres/mysql/sqlite/clickhouse, FETCH FIRST 10 ROWS ONLY on oracle). " +
				"Pass values through `params` using the connection's placeholder style instead of concatenating them into the SQL. " +
				"The statement runs in a read-only transaction that is always rolled back (on clickhouse, with readonly=1), so it cannot " +
				"change data: use sql_execute for INSERT/UPDATE/DELETE. Not for MongoDB or Redis connections.",
			InputSchema: input([]string{"connection", "sql"}, map[string]*jsonschema.Schema{
				"connection": connParam(sqlConns, "SQL connection to query. One of:"),
				"sql":        str("A single SQL statement in the connection's dialect, no trailing second statement."),
				"params":     {Type: "array", Description: "Positional parameter values bound to the placeholders ($1 / @p1 / ?).", Items: &jsonschema.Schema{}},
				"max_rows":   integer("Maximum rows to return (capped by the connection's limit).", s.maxCap),
			}),
			Annotations: readOnly("Query a SQL database (read-only)"),
		}, s.handleSQLQuery})
	}

	sqlWrite := filter(sqlConns, func(c model.Connection) bool { return len(c.AllowedOps()) > 1 })
	if len(sqlWrite) > 0 {
		defs = append(defs, toolDef{&mcp.Tool{
			Name: "sql_execute",
			Description: "Run ONE data-modifying SQL statement (INSERT, UPDATE, DELETE, MERGE or DDL) on a SQL connection that allows it. " +
				"The gateway classifies the statement and refuses it unless every operation it performs is enabled on that connection " +
				"(allowed operations are listed per connection below). Set dry_run=true to execute it in a transaction that is rolled back " +
				"and see how many rows would be affected (not on clickhouse, which has no transactions). Always use a WHERE clause on UPDATE/DELETE. Use RETURNING (postgres/sqlite) " +
				"or OUTPUT (sqlserver) to get the affected rows back.",
			InputSchema: input([]string{"connection", "sql"}, map[string]*jsonschema.Schema{
				"connection": connParam(sqlWrite, "SQL connection with write permissions. One of:"),
				"sql":        str("A single SQL statement in the connection's dialect."),
				"params":     {Type: "array", Description: "Positional parameter values bound to the placeholders ($1 / @p1 / ?).", Items: &jsonschema.Schema{}},
				"dry_run":    boolean("Execute and roll back, reporting affected rows without changing data."),
			}),
			Annotations: destructive("Modify a SQL database"),
		}, s.handleSQLExecute})
	}

	defs = append(defs, s.redisTools(filter(conns, func(c model.Connection) bool { return c.Type == model.Redis }))...)

	mongoConns := filter(conns, func(c model.Connection) bool { return c.Type == model.MongoDB })
	if len(mongoConns) == 0 {
		return defs
	}
	mongoCommon := func(conns []model.Connection, purpose string) map[string]*jsonschema.Schema {
		return map[string]*jsonschema.Schema{
			"connection": connParam(conns, purpose),
			"database":   str("Database name, only when different from the connection default."),
			"collection": str("Collection name."),
		}
	}
	with := func(base, extra map[string]*jsonschema.Schema) map[string]*jsonschema.Schema {
		for k, v := range extra {
			base[k] = v
		}
		return base
	}
	const mongoPurpose = "MongoDB connection. One of:"
	filterDoc := object(`MongoDB Extended JSON filter, e.g. {"status": "paid", "_id": {"$oid": "65f0..."}, "createdAt": {"$gte": {"$date": "2024-01-01T00:00:00Z"}}}. Empty object matches all.`)

	defs = append(defs,
		toolDef{&mcp.Tool{
			Name: "mongo_find",
			Description: "Find documents in a MongoDB collection. `filter`, `projection` and `sort` are MongoDB Extended JSON objects. " +
				"Returns {documents, count, truncated}. MongoDB connections only: never send SQL here.",
			InputSchema: input([]string{"connection", "collection"}, with(mongoCommon(mongoConns, mongoPurpose), map[string]*jsonschema.Schema{
				"filter":     filterDoc,
				"projection": object(`Fields to include/exclude, e.g. {"name": 1, "_id": 0}.`),
				"sort":       object(`Sort order, e.g. {"createdAt": -1}.`),
				"skip":       integer("Documents to skip.", 1_000_000),
				"limit":      integer("Maximum documents to return (capped by the connection's limit).", s.maxCap),
			})),
			Annotations: readOnly("Find MongoDB documents"),
		}, s.mongoTool("mongo_find", s.mongoFind)},
		toolDef{&mcp.Tool{
			Name: "mongo_aggregate",
			Description: "Run an aggregation pipeline (JSON array of stage objects, Extended JSON) on a MongoDB collection and return the resulting documents. " +
				"Read-only unless the pipeline uses $merge (needs insert+update) or $out (needs insert+delete+ddl).",
			InputSchema: input([]string{"connection", "collection", "pipeline"}, with(mongoCommon(mongoConns, mongoPurpose), map[string]*jsonschema.Schema{
				"pipeline": {Type: "array", Description: `Stages, e.g. [{"$match": {"status": "paid"}}, {"$group": {"_id": "$customerId", "total": {"$sum": "$amount"}}}].`, Items: &jsonschema.Schema{Type: "object"}},
				"limit":    integer("Maximum documents to return (capped by the connection's limit).", s.maxCap),
			})),
			Annotations: &mcp.ToolAnnotations{Title: "Aggregate MongoDB documents", OpenWorldHint: jsonschema.Ptr(false)},
		}, s.mongoTool("mongo_aggregate", s.mongoAggregate)},
		toolDef{&mcp.Tool{
			Name:        "mongo_count",
			Description: "Count the documents matching a filter (Extended JSON) in a MongoDB collection.",
			InputSchema: input([]string{"connection", "collection"}, with(mongoCommon(mongoConns, mongoPurpose), map[string]*jsonschema.Schema{
				"filter": filterDoc,
			})),
			Annotations: readOnly("Count MongoDB documents"),
		}, s.mongoTool("mongo_count", s.mongoCount)},
	)

	if ins := filter(mongoConns, func(c model.Connection) bool { return c.AllowInsert }); len(ins) > 0 {
		defs = append(defs, toolDef{&mcp.Tool{
			Name:        "mongo_insert",
			Description: "Insert one or more documents (JSON array of Extended JSON objects) into a MongoDB collection. Returns the inserted ids.",
			InputSchema: input([]string{"connection", "collection", "documents"}, with(mongoCommon(ins, "MongoDB connection with insert enabled. One of:"), map[string]*jsonschema.Schema{
				"documents": {Type: "array", Description: "Documents to insert.", Items: &jsonschema.Schema{Type: "object"}},
			})),
			Annotations: destructive("Insert MongoDB documents"),
		}, s.mongoTool("mongo_insert", s.mongoInsert)})
	}
	if upd := filter(mongoConns, func(c model.Connection) bool { return c.AllowUpdate }); len(upd) > 0 {
		defs = append(defs, toolDef{&mcp.Tool{
			Name: "mongo_update",
			Description: "Update documents matching `filter` with update operators, e.g. {\"$set\": {\"status\": \"paid\"}}, {\"$inc\": {\"retries\": 1}}. " +
				"many=false (default) updates only the first match. upsert=true also needs insert permission. An empty filter requires all_documents=true.",
			InputSchema: input([]string{"connection", "collection", "filter", "update"}, with(mongoCommon(upd, "MongoDB connection with update enabled. One of:"), map[string]*jsonschema.Schema{
				"filter":        filterDoc,
				"update":        object("Update document with $-operators."),
				"many":          boolean("Update every matching document instead of the first one."),
				"upsert":        boolean("Insert a document when nothing matches (requires insert permission)."),
				"all_documents": boolean("Confirm that an empty filter should touch every document."),
			})),
			Annotations: destructive("Update MongoDB documents"),
		}, s.mongoTool("mongo_update", s.mongoUpdate)})
	}
	if del := filter(mongoConns, func(c model.Connection) bool { return c.AllowDelete }); len(del) > 0 {
		defs = append(defs, toolDef{&mcp.Tool{
			Name:        "mongo_delete",
			Description: "Delete documents matching `filter`. many=false (default) deletes only the first match. An empty filter requires all_documents=true.",
			InputSchema: input([]string{"connection", "collection", "filter"}, with(mongoCommon(del, "MongoDB connection with delete enabled. One of:"), map[string]*jsonschema.Schema{
				"filter":        filterDoc,
				"many":          boolean("Delete every matching document instead of the first one."),
				"all_documents": boolean("Confirm that an empty filter should delete every document."),
			})),
			Annotations: destructive("Delete MongoDB documents"),
		}, s.mongoTool("mongo_delete", s.mongoDelete)})
	}
	return defs
}

// call tracks one tool invocation and writes its audit entry.
type call struct {
	s     *Server
	start time.Time
	entry model.AuditEntry
}

func (s *Server) begin(req *mcp.CallToolRequest) *call {
	return &call{s: s, start: time.Now(), entry: model.AuditEntry{Tool: req.Params.Name, Client: clientOf(req)}}
}

func clientOf(req *mcp.CallToolRequest) string {
	label := "unknown"
	if req.Extra != nil && req.Extra.TokenInfo != nil {
		label = req.Extra.TokenInfo.UserID
	}
	if req.Extra != nil && req.Extra.Header != nil {
		if ua := req.Extra.Header.Get("User-Agent"); ua != "" {
			label += " · " + limit(ua, 60)
		}
	}
	return label
}

func (c *call) setConn(conn model.Connection) {
	c.entry.Connection, c.entry.DBType = conn.Name, string(conn.Type)
}

func (c *call) record(status, msg string, rows int64) {
	c.entry.Status, c.entry.Message, c.entry.Rows = status, limit(msg, maxAuditText), rows
	c.entry.DurationMS = time.Since(c.start).Milliseconds()
	c.entry.Statement, c.entry.Params = limit(c.entry.Statement, maxAuditText), limit(c.entry.Params, maxAuditText)
	// The audit write must not be cancelled with the request.
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := c.s.store.InsertAudit(ctx, c.entry); err != nil {
		slog.Error("audit write failed", "err", err, "tool", c.entry.Tool)
	}
}

func (c *call) fail(status, msg string) (*mcp.CallToolResult, error) {
	c.record(status, msg, 0)
	if status == model.StatusDenied {
		msg = "DENIED: " + msg
	}
	return &mcp.CallToolResult{IsError: true, Content: []mcp.Content{&mcp.TextContent{Text: msg}}}, nil
}

func (c *call) failErr(ctx context.Context, conn model.Connection, err error) (*mcp.CallToolResult, error) {
	if errors.Is(ctx.Err(), context.DeadlineExceeded) || errors.Is(err, context.DeadlineExceeded) {
		return c.fail(model.StatusError, fmt.Sprintf("timed out after %ds (connection limit): %v", conn.TimeoutSec, err))
	}
	return c.fail(model.StatusError, err.Error())
}

func (c *call) ok(v any, rows int64) (*mcp.CallToolResult, error) {
	b, err := json.Marshal(v)
	if err != nil {
		return c.fail(model.StatusError, "encoding result: "+err.Error())
	}
	c.record(model.StatusOK, "", rows)
	return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: string(b)}}}, nil
}

// limit cuts s to at most n bytes without splitting a UTF-8 character.
func limit(s string, n int) string {
	if len(s) <= n {
		return s
	}
	for n > 0 && !utf8.RuneStart(s[n]) {
		n--
	}
	return s[:n] + "…"
}

func decode(raw json.RawMessage, v any) error {
	if len(bytes.TrimSpace(raw)) == 0 {
		raw = []byte("{}")
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	return dec.Decode(v)
}

// kindOf groups database types by the tools that serve them.
func kindOf(t model.DBType) string {
	switch {
	case t.IsSQL():
		return "sql"
	case t == model.Redis:
		return "redis"
	}
	return "mongo"
}

// toolHint says which tools serve a connection of the given type.
func toolHint(c model.Connection) string {
	switch kindOf(c.Type) {
	case "sql":
		return fmt.Sprintf("use sql_query / sql_execute with %s SQL", c.Type.Info().Label)
	case "redis":
		return "use redis_query (and redis_execute when allowed) with Redis commands"
	}
	return "use mongo_find, mongo_aggregate or mongo_count (and mongo_insert/update/delete when allowed)"
}

// connection resolves an enabled connection by name; kind is "sql", "mongo",
// "redis" or "" for any. Errors explain which connections and tools are valid.
func (s *Server) connection(ctx context.Context, name, kind string) (model.Connection, error) {
	conns, err := s.store.ListConnections(ctx)
	if err != nil {
		return model.Connection{}, err
	}
	var avail []string
	for _, c := range conns {
		if !c.Enabled {
			continue
		}
		if strings.EqualFold(c.Name, name) {
			if kind != "" && kindOf(c.Type) != kind {
				return c, fmt.Errorf("connection %q is %s: %s", c.Name, c.Type.Info().Label, toolHint(c))
			}
			return c, nil
		}
		if kind == "" || kindOf(c.Type) == kind {
			avail = append(avail, fmt.Sprintf("%s (%s)", c.Name, c.Type))
		}
	}
	return model.Connection{}, fmt.Errorf("unknown or disabled connection %q. Available: %s. Call list_connections for details",
		name, strings.Join(avail, ", "))
}

func (s *Server) maxRows(conn model.Connection, requested int) int {
	n := conn.MaxRows
	if requested > 0 && requested < n {
		n = requested
	}
	if n <= 0 {
		n = 100
	}
	return min(n, s.maxCap)
}

func denyMsg(conn model.Connection, missing []model.Op) string {
	return fmt.Sprintf("connection %q (%s) does not allow %s. Allowed operations: %s. An administrator can enable it in the gateway's web UI; do not try to work around this restriction.",
		conn.Name, conn.Type, opsText(missing), opsText(conn.AllowedOps()))
}

func (s *Server) withTimeout(ctx context.Context, conn model.Connection) (context.Context, context.CancelFunc) {
	return context.WithTimeout(ctx, time.Duration(conn.TimeoutSec)*time.Second)
}

func toolsFor(c model.Connection) []string {
	if c.Type == model.Redis {
		tools := []string{"describe_schema", "redis_query"}
		if len(c.AllowedOps()) > 1 {
			tools = append(tools, "redis_execute")
		}
		return tools
	}
	if c.Type.IsSQL() {
		tools := []string{"describe_schema", "sql_query"}
		if len(c.AllowedOps()) > 1 {
			tools = append(tools, "sql_execute")
		}
		return tools
	}
	tools := []string{"describe_schema", "mongo_find", "mongo_aggregate", "mongo_count"}
	if c.AllowInsert {
		tools = append(tools, "mongo_insert")
	}
	if c.AllowUpdate {
		tools = append(tools, "mongo_update")
	}
	if c.AllowDelete {
		tools = append(tools, "mongo_delete")
	}
	return tools
}

func (s *Server) handleListConnections(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	conns, err := s.store.ListConnections(ctx)
	if err != nil {
		return nil, err
	}
	type view struct {
		Name        string     `json:"name"`
		Type        string     `json:"type"`
		TypeLabel   string     `json:"type_label"`
		Dialect     string     `json:"dialect"`
		Placeholder string     `json:"param_placeholder,omitempty"`
		Description string     `json:"description,omitempty"`
		Database    string     `json:"default_database,omitempty"`
		Allowed     []model.Op `json:"allowed_operations"`
		Denied      []model.Op `json:"denied_operations"`
		Tools       []string   `json:"use_tools"`
		MaxRows     int        `json:"max_rows"`
	}
	out := []view{}
	for _, c := range conns {
		if !c.Enabled {
			continue
		}
		info := c.Type.Info()
		v := view{
			Name: c.Name, Type: string(c.Type), TypeLabel: info.Label, Dialect: info.Dialect, Placeholder: info.Placeholder,
			Description: c.Description, Allowed: c.AllowedOps(), Tools: toolsFor(c), MaxRows: min(c.MaxRows, s.maxCap),
			Denied: c.Missing([]model.Op{model.OpInsert, model.OpUpdate, model.OpDelete, model.OpDDL}),
		}
		if c.Type == model.MongoDB {
			v.Database = db.MongoDatabase(c)
		}
		out = append(out, v)
	}
	b, _ := json.Marshal(map[string]any{"connections": out})
	return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: string(b)}}}, nil
}
