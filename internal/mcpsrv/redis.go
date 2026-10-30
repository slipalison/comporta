package mcpsrv

import (
	"context"
	"fmt"
	"strings"

	"github.com/google/jsonschema-go/jsonschema"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/slipalison/comporta/internal/db"
	"github.com/slipalison/comporta/internal/guard"
	"github.com/slipalison/comporta/internal/model"
)

// redisTools returns redis_query, and redis_execute when some Redis
// connection allows a write.
func (s *Server) redisTools(conns []model.Connection) []toolDef {
	if len(conns) == 0 {
		return nil
	}
	args := func(conns []model.Connection, purpose string) *jsonschema.Schema {
		return input([]string{"connection", "command"}, map[string]*jsonschema.Schema{
			"connection": connParam(conns, purpose),
			"command":    str("The command name only, e.g. GET, HGETALL, SCAN, ZRANGE."),
			"args": {Type: "array", Description: `The command's arguments as strings, e.g. ["user:1"] or ["0", "MATCH", "order:*", "COUNT", "100"].`,
				Items: &jsonschema.Schema{Type: "string"}},
		})
	}
	defs := []toolDef{{&mcp.Tool{
		Name: "redis_query",
		Description: "Run ONE read-only Redis command (GET, MGET, HGETALL, LRANGE, SMEMBERS, ZRANGE, SCAN, TYPE, TTL, ...) and get its reply as JSON " +
			"{value, items, truncated}. Use SCAN with MATCH and COUNT to find keys: KEYS is not available. Writes are refused here: use " +
			"redis_execute when the connection allows them. Redis connections only: never send SQL here.",
		InputSchema: args(conns, "Redis connection. One of:"),
		Annotations: readOnly("Query Redis (read-only)"),
	}, s.handleRedisQuery}}

	writable := filter(conns, func(c model.Connection) bool { return len(c.AllowedOps()) > 1 })
	if len(writable) > 0 {
		defs = append(defs, toolDef{&mcp.Tool{
			Name: "redis_execute",
			Description: "Run ONE Redis command that changes data, on a connection that allows it. The gateway classifies the command: " +
				"SADD/LPUSH/ZADD need insert; INCR/EXPIRE/RENAME need update; SET/HSET/MSET need insert and update; DEL/HDEL/LPOP need " +
				"delete; FLUSHDB, CONFIG, EVAL and anything unknown need ddl. KEYS, SELECT, MULTI/EXEC, SUBSCRIBE and blocking " +
				"commands are never available.",
			InputSchema: args(writable, "Redis connection with write permissions. One of:"),
			Annotations: destructive("Modify Redis data"),
		}, s.handleRedisExecute})
	}
	return defs
}

type redisArgs struct {
	Connection string   `json:"connection"`
	Command    string   `json:"command"`
	Args       []string `json:"args"`
}

func (s *Server) handleRedisQuery(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	return s.runRedis(ctx, req, false)
}

func (s *Server) handleRedisExecute(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	return s.runRedis(ctx, req, true)
}

func (s *Server) runRedis(ctx context.Context, req *mcp.CallToolRequest, write bool) (*mcp.CallToolResult, error) {
	c := s.begin(req)
	var a redisArgs
	if err := decode(req.Params.Arguments, &a); err != nil {
		return c.fail(model.StatusError, errInvalidArgs+err.Error())
	}
	c.entry.Connection = a.Connection
	c.entry.Statement = strings.TrimSpace(strings.Join(append([]string{a.Command}, a.Args...), " "))
	conn, err := s.connection(ctx, a.Connection, "redis")
	if err != nil {
		return c.fail(model.StatusError, err.Error())
	}
	c.setConn(conn)
	an, err := guard.AnalyzeRedis(a.Command)
	if err != nil {
		c.entry.Operation = "invalid"
		return c.fail(model.StatusDenied, err.Error())
	}
	c.entry.Operation = an.Operation()
	if !write && !an.ReadOnly() {
		return c.fail(model.StatusDenied, fmt.Sprintf("redis_query only runs read-only commands and %s performs %s. Use redis_execute if connection %q allows it (allowed: %s).",
			an.Leading, opsText(an.Ops), conn.Name, opsText(conn.AllowedOps())))
	}
	if missing := conn.Missing(an.Ops); len(missing) > 0 {
		return c.fail(model.StatusDenied, denyMsg(conn, missing))
	}
	client, err := s.reg.Redis(conn)
	if err != nil {
		return c.failErr(ctx, conn, err)
	}
	ctx, cancel := s.withTimeout(ctx, conn)
	defer cancel()
	res, err := db.RunRedis(ctx, client, an.Leading, a.Args, s.maxRows(conn, 0))
	if err != nil {
		return c.failErr(ctx, conn, err)
	}
	return c.ok(map[string]any{"connection": conn.Name, "db_type": conn.Type, "command": an.Leading,
		"operation": an.Operation(), "result": res}, int64(max(res.Items, 1)))
}

// describeRedis samples keys for describe_schema; object is a key pattern.
func (s *Server) describeRedis(ctx context.Context, c *call, conn model.Connection, a describeArgs) (*mcp.CallToolResult, error) {
	client, err := s.reg.Redis(conn)
	if err != nil {
		return c.failErr(ctx, conn, err)
	}
	size := a.SampleSize
	if size <= 0 {
		size = 50
	}
	info, err := db.DescribeRedis(ctx, client, a.Object, min(size, 200))
	if err != nil {
		return c.failErr(ctx, conn, err)
	}
	return c.ok(map[string]any{"connection": conn.Name, "db_type": conn.Type, "result": info}, int64(len(info.Sampled)))
}
