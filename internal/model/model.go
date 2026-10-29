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
	Postgres  DBType = "postgres"
	SQLServer DBType = "sqlserver"
	MySQL     DBType = "mysql"
	SQLite    DBType = "sqlite"
	MongoDB   DBType = "mongodb"
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
	MongoDB: {
		Label:      "MongoDB",
		Dialect:    "MongoDB query language (NOT SQL): filters, projections and aggregation pipelines as MongoDB Extended JSON, e.g. {\"_id\": {\"$oid\": \"...\"}}, {\"$date\": \"2024-01-01T00:00:00Z\"}",
		DSNExample: "mongodb://user:pass@host:27017/dbname?authSource=admin",
	},
}

func (t DBType) Valid() bool { _, ok := DBTypes[t]; return ok }
func (t DBType) IsSQL() bool { return DBTypes[t].SQL }
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
