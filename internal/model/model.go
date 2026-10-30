// Package model holds the domain types shared by every layer: database types,
// operations that can be allowed per connection, connections and audit entries.
package model

import (
	"slices"
	"time"
)

// Op is a class of operation a statement performs. Select is always allowed;
// the others are toggled per connection.
type Op string

const (
	OpSelect Op = "select"
	OpInsert Op = "insert"
	OpUpdate Op = "update"
	OpDelete Op = "delete"
	OpDDL    Op = "ddl" // schema changes and admin commands: CREATE, DROP, EXEC, GRANT, SET, ...
)

type DBType string

const (
	Postgres   DBType = "postgres"
	SQLServer  DBType = "sqlserver"
	MySQL      DBType = "mysql"
	SQLite     DBType = "sqlite"
	Oracle     DBType = "oracle"
	ClickHouse DBType = "clickhouse"
	MongoDB    DBType = "mongodb"
	Redis      DBType = "redis"
)

// DBTypeInfo describes a database type for both the admin UI and the LLM.
type DBTypeInfo struct {
	Label       string `json:"label"`
	SQL         bool   `json:"sql"`
	Dialect     string `json:"dialect"`
	Placeholder string `json:"placeholder,omitempty"`
	DSNExample  string `json:"dsn_example"`
}

var DBTypes = map[DBType]DBTypeInfo{
	Postgres: {
		Label:       "PostgreSQL",
		SQL:         true,
		Dialect:     `PostgreSQL SQL: "double quotes" for identifiers, LIMIT n, ILIKE, ::type casts, schemas (default public)`,
		Placeholder: "$1, $2, ...",
		DSNExample:  "postgres://user:pass@host:5432/dbname?sslmode=disable",
	},
	SQLServer: {
		Label:       "Microsoft SQL Server",
		SQL:         true,
		Dialect:     "T-SQL: SELECT TOP (n) instead of LIMIT, [brackets] for identifiers, GETDATE(), schemas (default dbo)",
		Placeholder: "@p1, @p2, ...",
		DSNExample:  "sqlserver://user:pass@host:1433?database=dbname&encrypt=disable",
	},
	MySQL: {
		Label:       "MySQL / MariaDB",
		SQL:         true,
		Dialect:     "MySQL SQL: `backticks` for identifiers, LIMIT n, NOW()",
		Placeholder: "?",
		DSNExample:  "mysql://user:pass@host:3306/dbname",
	},
	SQLite: {
		Label:       "SQLite",
		SQL:         true,
		Dialect:     "SQLite SQL: LIMIT n, datetime('now'), loose typing",
		Placeholder: "?",
		DSNExample:  "/data/files/app.db",
	},
	Oracle: {
		Label:       "Oracle Database",
		SQL:         true,
		Dialect:     `Oracle SQL: FETCH FIRST n ROWS ONLY (or ROWNUM), "double quotes" for case-sensitive identifiers, unquoted names are upper case, SYSDATE, DUAL, schemas are users. No trailing semicolon needed`,
		Placeholder: ":1, :2, ...",
		DSNExample:  "oracle://user:pass@host:1521/service_name",
	},
	ClickHouse: {
		Label:       "ClickHouse",
		SQL:         true,
		Dialect:     "ClickHouse SQL: LIMIT n, `backticks` or \"double quotes\" for identifiers, now(), toDate(), arrays and maps, databases instead of schemas. Columnar and append-oriented: prefer aggregations, avoid row-by-row updates",
		Placeholder: "?",
		DSNExample:  "clickhouse://user:pass@host:9000/dbname",
	},
	Redis: {
		Label:      "Redis / Valkey",
		Dialect:    "Redis commands (NOT SQL): pass the command name and its arguments, e.g. GET user:1, HGETALL session:42, SCAN 0 MATCH order:* COUNT 100. KEYS, SELECT, MULTI, SUBSCRIBE and blocking commands are not available",
		DSNExample: "redis://user:pass@host:6379/0",
	},
	MongoDB: {
		Label:      "MongoDB",
		Dialect:    "MongoDB query language (NOT SQL): filters, projections and aggregation pipelines as MongoDB Extended JSON, e.g. {\"_id\": {\"$oid\": \"...\"}}, {\"$date\": \"2024-01-01T00:00:00Z\"}",
		DSNExample: "mongodb://user:pass@host:27017/dbname?authSource=admin",
	},
}

func (t DBType) Valid() bool { _, ok := DBTypes[t]; return ok }
func (t DBType) IsSQL() bool { return DBTypes[t].SQL }

// Transactional reports whether writes can run in a transaction that is
// rolled back (dry_run) and reads in one that never commits. ClickHouse has
// no transactions; its reads are protected by readonly=1 instead.
func (t DBType) Transactional() bool { return t.IsSQL() && t != ClickHouse }
func (t DBType) Info() DBTypeInfo {
	return DBTypes[t]
}

type Connection struct {
	ID          int64     `json:"id"`
	Name        string    `json:"name"`
	Type        DBType    `json:"type"`
	Description string    `json:"description"`
	DSN         string    `json:"-"`
	Database    string    `json:"database"`
	Enabled     bool      `json:"enabled"`
	AllowInsert bool      `json:"allow_insert"`
	AllowUpdate bool      `json:"allow_update"`
	AllowDelete bool      `json:"allow_delete"`
	AllowDDL    bool      `json:"allow_ddl"`
	MaxRows     int       `json:"max_rows"`
	TimeoutSec  int       `json:"timeout_sec"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

func (c Connection) Allows(op Op) bool {
	switch op {
	case OpSelect:
		return true
	case OpInsert:
		return c.AllowInsert
	case OpUpdate:
		return c.AllowUpdate
	case OpDelete:
		return c.AllowDelete
	case OpDDL:
		return c.AllowDDL
	}
	return false
}

// AllowedOps lists every op enabled on the connection, select first.
func (c Connection) AllowedOps() []Op {
	ops := []Op{OpSelect}
	for _, op := range []Op{OpInsert, OpUpdate, OpDelete, OpDDL} {
		if c.Allows(op) {
			ops = append(ops, op)
		}
	}
	return ops
}

// Missing returns the ops in required that the connection does not allow.
func (c Connection) Missing(required []Op) []Op {
	var missing []Op
	for _, op := range required {
		if !c.Allows(op) && !slices.Contains(missing, op) {
			missing = append(missing, op)
		}
	}
	return missing
}

// Audit statuses.
const (
	StatusOK     = "ok"
	StatusDenied = "denied"
	StatusError  = "error"
)

type AuditEntry struct {
	ID         int64     `json:"id"`
	Time       time.Time `json:"ts"`
	Client     string    `json:"client"`
	Connection string    `json:"connection"`
	DBType     string    `json:"db_type"`
	Tool       string    `json:"tool"`
	Operation  string    `json:"operation"`
	Statement  string    `json:"statement"`
	Params     string    `json:"params"`
	Status     string    `json:"status"`
	Message    string    `json:"message"`
	Rows       int64     `json:"rows"`
	DurationMS int64     `json:"duration_ms"`
}
