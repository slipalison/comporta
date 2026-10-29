package mcpsrv

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"

	"github.com/slipalison/comporta/internal/db"
	"github.com/slipalison/comporta/internal/guard"
	"github.com/slipalison/comporta/internal/model"
)

type describeArgs struct {
	Connection string `json:"connection"`
	Object     string `json:"object"`
	Schema     string `json:"schema"`
	Database   string `json:"database"`
	SampleSize int    `json:"sample_size"`
}

func (s *Server) handleDescribe(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	c := s.begin(req)
	var a describeArgs
	if err := decode(req.Params.Arguments, &a); err != nil {
		return c.fail(model.StatusError, "invalid arguments: "+err.Error())
	}
	c.entry.Operation = "schema"
	c.entry.Statement = strings.Join(strings.Fields("describe "+a.Schema+" "+a.Object), " ")
	c.entry.Connection = a.Connection // replaced by the canonical name once resolved
	conn, err := s.connection(ctx, a.Connection, "")
	if err != nil {
		return c.fail(model.StatusError, err.Error())
	}
	c.setConn(conn)
	ctx, cancel := s.withTimeout(ctx, conn)
	defer cancel()

	if conn.Type != model.MongoDB {
		pool, err := s.reg.SQL(conn)
		if err != nil {
			return c.failErr(ctx, conn, err)
		}
		res, err := db.DescribeSQL(ctx, pool, conn.Type, a.Schema, a.Object)
		if err != nil {
			return c.failErr(ctx, conn, err)
		}
		if a.Object != "" && res.RowCount == 0 {
			return c.fail(model.StatusError, fmt.Sprintf("table %q not found; call describe_schema without object to list tables", a.Object))
		}
		return c.ok(map[string]any{"connection": conn.Name, "db_type": conn.Type, "result": res}, int64(res.RowCount))
	}

	client, err := s.reg.Mongo(conn)
	if err != nil {
		return c.failErr(ctx, conn, err)
	}
	dbName := a.Database
	if dbName == "" {
		dbName = db.MongoDatabase(conn)
	}
	if dbName == "" {
		return c.fail(model.StatusError, "no database: pass `database` or set a default database on the connection")
	}
	if a.Object == "" {
		names, err := client.Database(dbName).ListCollectionNames(ctx, bson.D{})
		if err != nil {
			return c.failErr(ctx, conn, err)
		}
		sort.Strings(names)
		return c.ok(map[string]any{"connection": conn.Name, "db_type": conn.Type, "database": dbName, "collections": names}, int64(len(names)))
	}
	size := a.SampleSize
	if size <= 0 {
		size = 20
	}
	info, err := db.DescribeCollection(ctx, client.Database(dbName).Collection(a.Object), min(size, 200))
	if err != nil {
		return c.failErr(ctx, conn, err)
	}
	return c.ok(map[string]any{"connection": conn.Name, "db_type": conn.Type, "database": dbName, "result": info}, int64(info.Sampled))
}

type sqlArgs struct {
	Connection string `json:"connection"`
	SQL        string `json:"sql"`
	Params     []any  `json:"params"`
	MaxRows    int    `json:"max_rows"`
	DryRun     bool   `json:"dry_run"`
}

// sqlPrelude decodes the arguments, resolves the connection and classifies the
// statement. A non-nil result means the call already failed.
func (s *Server) sqlPrelude(ctx context.Context, req *mcp.CallToolRequest) (*call, sqlArgs, model.Connection, guard.Analysis, *mcp.CallToolResult) {
	c := s.begin(req)
	var a sqlArgs
	var conn model.Connection
	var an guard.Analysis
	if err := decode(req.Params.Arguments, &a); err != nil {
		res, _ := c.fail(model.StatusError, "invalid arguments: "+err.Error())
		return c, a, conn, an, res
	}
	c.entry.Statement = a.SQL
	if len(a.Params) > 0 {
		b, _ := json.Marshal(a.Params)
		c.entry.Params = string(b)
	}
	c.entry.Connection = a.Connection // replaced by the canonical name once resolved
	conn, err := s.connection(ctx, a.Connection, "sql")
	if err != nil {
		res, _ := c.fail(model.StatusError, err.Error())
		return c, a, conn, an, res
	}
	c.setConn(conn)
	an, err = guard.AnalyzeSQL(conn.Type, a.SQL)
	if err != nil {
		c.entry.Operation = "invalid"
		res, _ := c.fail(model.StatusDenied, err.Error())
		return c, a, conn, an, res
	}
	c.entry.Operation = an.Operation()
	return c, a, conn, an, nil
}

func (s *Server) handleSQLQuery(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	c, a, conn, an, failed := s.sqlPrelude(ctx, req)
	if failed != nil {
		return failed, nil
	}
	if !an.ReadOnly() {
		return c.fail(model.StatusDenied, fmt.Sprintf("sql_query only runs read-only statements and this one performs %s. Use sql_execute if connection %q allows it (allowed: %s).",
			opsText(an.Ops), conn.Name, opsText(conn.AllowedOps())))
	}
	return s.runSQL(ctx, c, conn, a, an, false)
}

func (s *Server) handleSQLExecute(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	c, a, conn, an, failed := s.sqlPrelude(ctx, req)
	if failed != nil {
		return failed, nil
	}
	if missing := conn.Missing(an.Ops); len(missing) > 0 {
		return c.fail(model.StatusDenied, denyMsg(conn, missing))
	}
	return s.runSQL(ctx, c, conn, a, an, !an.ReadOnly())
}

func (s *Server) runSQL(ctx context.Context, c *call, conn model.Connection, a sqlArgs, an guard.Analysis, write bool) (*mcp.CallToolResult, error) {
	pool, err := s.reg.SQL(conn)
	if err != nil {
		return c.failErr(ctx, conn, err)
	}
	ctx, cancel := s.withTimeout(ctx, conn)
	defer cancel()
	if write && a.DryRun {
		c.entry.Operation += " (dry-run)"
	}
	res, err := db.RunSQL(ctx, pool, conn.Type, db.SQLRequest{
		Query:       a.SQL,
		Args:        sqlParams(a.Params),
		Write:       write,
		ReturnsRows: an.ReturnsRows,
		DryRun:      a.DryRun,
		MaxRows:     s.maxRows(conn, a.MaxRows),
	})
	if err != nil {
		return c.failErr(ctx, conn, err)
	}
	rows := int64(res.RowCount)
	if res.RowsAffected != nil {
		rows = *res.RowsAffected
	}
	return c.ok(struct {
		Connection string `json:"connection"`
		DBType     string `json:"db_type"`
		Operation  string `json:"operation"`
		*db.Result
	}{conn.Name, string(conn.Type), an.Operation(), res}, rows)
}

// sqlParams turns JSON values into driver arguments: integers stay integers,
// objects and arrays are passed as JSON text.
func sqlParams(params []any) []any {
	out := make([]any, len(params))
	for i, p := range params {
		switch v := p.(type) {
		case json.Number:
			if n, err := v.Int64(); err == nil {
				out[i] = n
			} else if f, err := v.Float64(); err == nil {
				out[i] = f
			} else {
				out[i] = v.String()
			}
		case map[string]any, []any:
			b, _ := json.Marshal(v)
			out[i] = string(b)
		default:
			out[i] = v
		}
	}
	return out
}

type mongoArgs struct {
	Connection   string          `json:"connection"`
	Database     string          `json:"database"`
	Collection   string          `json:"collection"`
	Filter       json.RawMessage `json:"filter"`
	Projection   json.RawMessage `json:"projection"`
	Sort         json.RawMessage `json:"sort"`
	Pipeline     json.RawMessage `json:"pipeline"`
	Documents    json.RawMessage `json:"documents"`
	Update       json.RawMessage `json:"update"`
	Skip         int64           `json:"skip"`
	Limit        int             `json:"limit"`
	Many         bool            `json:"many"`
	Upsert       bool            `json:"upsert"`
	AllDocuments bool            `json:"all_documents"`
}

type mongoOp func(ctx context.Context, c *call, a mongoArgs, conn model.Connection, coll *mongo.Collection) (*mcp.CallToolResult, error)

// compact renders a JSON argument on one line for the audit log.
func compact(raw json.RawMessage) string {
	if len(raw) == 0 {
		return "{}"
	}
	var b bytes.Buffer
	if err := json.Compact(&b, raw); err != nil {
		return string(raw)
	}
	return b.String()
}

// mongoTool wraps the common part of every MongoDB tool: argument decoding,
// connection lookup, database selection and timeout.
func (s *Server) mongoTool(name string, op mongoOp) mcp.ToolHandler {
	return func(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		c := s.begin(req)
		var a mongoArgs
		if err := decode(req.Params.Arguments, &a); err != nil {
			return c.fail(model.StatusError, "invalid arguments: "+err.Error())
		}
		// fallbacks for calls that fail before the operation describes itself
		c.entry.Connection, c.entry.Statement = a.Connection, compact(req.Params.Arguments)
		conn, err := s.connection(ctx, a.Connection, "mongo")
		if err != nil {
			return c.fail(model.StatusError, err.Error())
		}
		c.setConn(conn)
		if a.Collection == "" {
			return c.fail(model.StatusError, "collection is required")
		}
		dbName := a.Database
		if dbName == "" {
			dbName = db.MongoDatabase(conn)
		}
		if dbName == "" {
			return c.fail(model.StatusError, "no database: pass `database` or set a default database on the connection")
		}
		client, err := s.reg.Mongo(conn)
		if err != nil {
			return c.failErr(ctx, conn, err)
		}
		ctx, cancel := s.withTimeout(ctx, conn)
		defer cancel()
		return op(ctx, c, a, conn, client.Database(dbName).Collection(a.Collection))
	}
}

func ns(coll *mongo.Collection) string { return coll.Database().Name() + "." + coll.Name() }

func envelope(conn model.Connection, coll *mongo.Collection, op string, v any) map[string]any {
	return map[string]any{"connection": conn.Name, "db_type": conn.Type, "namespace": ns(coll), "operation": op, "result": v}
}

func (s *Server) mongoFind(ctx context.Context, c *call, a mongoArgs, conn model.Connection, coll *mongo.Collection) (*mcp.CallToolResult, error) {
	c.entry.Operation = string(model.OpSelect)
	c.entry.Statement = fmt.Sprintf("db.%s.find(%s, %s).sort(%s).skip(%d)", ns(coll), compact(a.Filter), compact(a.Projection), compact(a.Sort), a.Skip)
	var req db.FindRequest
	var err error
	if req.Filter, err = db.ParseDoc(a.Filter); err != nil {
		return c.fail(model.StatusError, "filter: "+err.Error())
	}
	if req.Projection, err = db.ParseDoc(a.Projection); err != nil {
		return c.fail(model.StatusError, "projection: "+err.Error())
	}
	if req.Sort, err = db.ParseDoc(a.Sort); err != nil {
		return c.fail(model.StatusError, "sort: "+err.Error())
	}
	req.Skip, req.MaxRows = a.Skip, s.maxRows(conn, a.Limit)
	res, err := db.MongoFind(ctx, coll, req)
	if err != nil {
		return c.failErr(ctx, conn, err)
	}
	return c.ok(envelope(conn, coll, "find", res), int64(res.Count))
}

func (s *Server) mongoAggregate(ctx context.Context, c *call, a mongoArgs, conn model.Connection, coll *mongo.Collection) (*mcp.CallToolResult, error) {
	c.entry.Operation = string(model.OpSelect)
	c.entry.Statement = fmt.Sprintf("db.%s.aggregate(%s)", ns(coll), compact(a.Pipeline))
	pipeline, err := db.ParseDocs(a.Pipeline)
	if err != nil {
		return c.fail(model.StatusError, "pipeline: "+err.Error())
	}
	if ops := db.PipelineOps(pipeline); len(ops) > 0 {
		c.entry.Operation = (guard.Analysis{Ops: ops}).Operation()
		if missing := conn.Missing(ops); len(missing) > 0 {
			return c.fail(model.StatusDenied, denyMsg(conn, missing))
		}
	}
	res, err := db.MongoAggregate(ctx, coll, pipeline, s.maxRows(conn, a.Limit))
	if err != nil {
		return c.failErr(ctx, conn, err)
	}
	return c.ok(envelope(conn, coll, "aggregate", res), int64(res.Count))
}

func (s *Server) mongoCount(ctx context.Context, c *call, a mongoArgs, conn model.Connection, coll *mongo.Collection) (*mcp.CallToolResult, error) {
	c.entry.Operation = string(model.OpSelect)
	c.entry.Statement = fmt.Sprintf("db.%s.countDocuments(%s)", ns(coll), compact(a.Filter))
	f, err := db.ParseDoc(a.Filter)
	if err != nil {
		return c.fail(model.StatusError, "filter: "+err.Error())
	}
	n, err := coll.CountDocuments(ctx, f)
	if err != nil {
		return c.failErr(ctx, conn, err)
	}
	return c.ok(envelope(conn, coll, "count", map[string]int64{"count": n}), n)
}

func (s *Server) mongoInsert(ctx context.Context, c *call, a mongoArgs, conn model.Connection, coll *mongo.Collection) (*mcp.CallToolResult, error) {
	c.entry.Operation = string(model.OpInsert)
	c.entry.Statement = fmt.Sprintf("db.%s.insertMany(%s)", ns(coll), compact(a.Documents))
	if missing := conn.Missing([]model.Op{model.OpInsert}); len(missing) > 0 {
		return c.fail(model.StatusDenied, denyMsg(conn, missing))
	}
	docs, err := db.ParseDocs(a.Documents)
	if err != nil {
		return c.fail(model.StatusError, "documents: "+err.Error())
	}
	if len(docs) == 0 {
		return c.fail(model.StatusError, "documents is empty")
	}
	items := make([]any, len(docs))
	for i, d := range docs {
		items[i] = d
	}
	res, err := coll.InsertMany(ctx, items)
	if err != nil {
		return c.failErr(ctx, conn, err)
	}
	ids := make([]json.RawMessage, 0, len(res.InsertedIDs))
	for _, id := range res.InsertedIDs {
		if b, err := bson.MarshalExtJSON(bson.D{{Key: "_id", Value: id}}, false, false); err == nil {
			ids = append(ids, b)
		}
	}
	return c.ok(envelope(conn, coll, "insert", map[string]any{"inserted_count": len(ids), "inserted_ids": ids}), int64(len(ids)))
}

func (s *Server) mongoUpdate(ctx context.Context, c *call, a mongoArgs, conn model.Connection, coll *mongo.Collection) (*mcp.CallToolResult, error) {
	method := "updateOne"
	if a.Many {
		method = "updateMany"
	}
	c.entry.Operation = string(model.OpUpdate)
	required := []model.Op{model.OpUpdate}
	if a.Upsert {
		c.entry.Operation = "insert+update"
		required = append(required, model.OpInsert)
	}
	c.entry.Statement = fmt.Sprintf("db.%s.%s(%s, %s, {upsert: %t})", ns(coll), method, compact(a.Filter), compact(a.Update), a.Upsert)
	if missing := conn.Missing(required); len(missing) > 0 {
		return c.fail(model.StatusDenied, denyMsg(conn, missing))
	}
	f, err := db.ParseDoc(a.Filter)
	if err != nil {
		return c.fail(model.StatusError, "filter: "+err.Error())
	}
	if len(f) == 0 && !a.AllDocuments {
		return c.fail(model.StatusError, db.ErrEmptyFilter.Error())
	}
	u, err := db.ParseDoc(a.Update)
	if err != nil || len(u) == 0 {
		return c.fail(model.StatusError, fmt.Sprintf("update must be a non-empty object with $-operators (%v)", err))
	}
	var res *mongo.UpdateResult
	if a.Many {
		res, err = coll.UpdateMany(ctx, f, u, options.UpdateMany().SetUpsert(a.Upsert))
	} else {
		res, err = coll.UpdateOne(ctx, f, u, options.UpdateOne().SetUpsert(a.Upsert))
	}
	if err != nil {
		return c.failErr(ctx, conn, err)
	}
	out := map[string]any{"matched_count": res.MatchedCount, "modified_count": res.ModifiedCount, "upserted_count": res.UpsertedCount}
	if res.UpsertedID != nil {
		if b, err := bson.MarshalExtJSON(bson.D{{Key: "_id", Value: res.UpsertedID}}, false, false); err == nil {
			out["upserted_id"] = json.RawMessage(b)
		}
	}
	return c.ok(envelope(conn, coll, method, out), res.ModifiedCount+res.UpsertedCount)
}

func (s *Server) mongoDelete(ctx context.Context, c *call, a mongoArgs, conn model.Connection, coll *mongo.Collection) (*mcp.CallToolResult, error) {
	method := "deleteOne"
	if a.Many {
		method = "deleteMany"
	}
	c.entry.Operation = string(model.OpDelete)
	c.entry.Statement = fmt.Sprintf("db.%s.%s(%s)", ns(coll), method, compact(a.Filter))
	if missing := conn.Missing([]model.Op{model.OpDelete}); len(missing) > 0 {
		return c.fail(model.StatusDenied, denyMsg(conn, missing))
	}
	f, err := db.ParseDoc(a.Filter)
	if err != nil {
		return c.fail(model.StatusError, "filter: "+err.Error())
	}
	if len(f) == 0 && !a.AllDocuments {
		return c.fail(model.StatusError, db.ErrEmptyFilter.Error())
	}
	var res *mongo.DeleteResult
	if a.Many {
		res, err = coll.DeleteMany(ctx, f)
	} else {
		res, err = coll.DeleteOne(ctx, f)
	}
	if err != nil {
		return c.failErr(ctx, conn, err)
	}
	return c.ok(envelope(conn, coll, method, map[string]int64{"deleted_count": res.DeletedCount}), res.DeletedCount)
}
