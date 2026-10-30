package db

import (
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"reflect"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/ClickHouse/clickhouse-go/v2"
	mssql "github.com/microsoft/go-mssqldb"

	"github.com/slipalison/comporta/internal/model"
)

// jsonNumberRe is the JSON grammar for a number.
var jsonNumberRe = regexp.MustCompile(`^-?(0|[1-9][0-9]*)(\.[0-9]+)?([eE][+-]?[0-9]+)?$`)

// MaxCellChars truncates very large text values so one row cannot flood the
// LLM context.
const MaxCellChars = 4000

type Column struct {
	Name string `json:"name"`
	Type string `json:"type,omitempty"`
}

type Result struct {
	Columns      []Column `json:"columns,omitempty"`
	Rows         [][]any  `json:"rows,omitempty"`
	RowCount     int      `json:"row_count"`
	Truncated    bool     `json:"truncated,omitempty"`
	RowsAffected *int64   `json:"rows_affected,omitempty"`
	Committed    *bool    `json:"committed,omitempty"`
}

type SQLRequest struct {
	Query       string
	Args        []any
	Write       bool // false: read-only transaction, always rolled back
	ReturnsRows bool
	DryRun      bool // run the write and roll it back
	MaxRows     int
}

// ErrNoDryRun is returned for dry_run on an engine without transactions.
var ErrNoDryRun = errors.New("dry_run is not available: this database has no transactions to roll back")

// querier is what RunSQL needs from a *sql.Tx or a *sql.DB.
type querier interface {
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
}

// RunSQL executes one statement. On transactional engines it runs inside a
// transaction, and reads are always rolled back (READ ONLY where the engine
// offers it), so a write hidden inside a "read" cannot persist even if the
// classifier missed it. ClickHouse has no transactions: its reads run with
// the server setting readonly=1 instead.
func RunSQL(ctx context.Context, pool *sql.DB, dbType model.DBType, req SQLRequest) (*Result, error) {
	if !dbType.Transactional() {
		return runWithoutTx(ctx, pool, req)
	}
	opts := &sql.TxOptions{}
	if !req.Write && (dbType == model.Postgres || dbType == model.MySQL) {
		opts.ReadOnly = true
	}
	tx, err := pool.BeginTx(ctx, opts)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }() // no-op after a commit

	// Oracle's driver refuses TxOptions.ReadOnly; the statement does the same.
	if !req.Write && dbType == model.Oracle {
		if _, err := tx.ExecContext(ctx, "SET TRANSACTION READ ONLY"); err != nil {
			return nil, err
		}
	}
	res, err := execute(ctx, tx, req)
	if err != nil {
		return nil, err
	}
	if req.Write {
		committed := false
		if !req.DryRun {
			if err := tx.Commit(); err != nil {
				return nil, err
			}
			committed = true
		}
		res.Committed = &committed
	}
	return res, nil
}

func runWithoutTx(ctx context.Context, pool *sql.DB, req SQLRequest) (*Result, error) {
	if req.DryRun {
		return nil, ErrNoDryRun
	}
	if !req.Write {
		ctx = clickhouse.Context(ctx, clickhouse.WithSettings(clickhouse.Settings{"readonly": 1}))
	}
	res, err := execute(ctx, pool, req)
	if err != nil {
		return nil, err
	}
	if req.Write {
		committed := true
		res.Committed = &committed
	}
	return res, nil
}

func execute(ctx context.Context, q querier, req SQLRequest) (*Result, error) {
	if !req.Write || req.ReturnsRows {
		rows, err := q.QueryContext(ctx, req.Query, req.Args...)
		if err != nil {
			return nil, err
		}
		return collect(rows, req.MaxRows)
	}
	r, err := q.ExecContext(ctx, req.Query, req.Args...)
	if err != nil {
		return nil, err
	}
	n, _ := r.RowsAffected()
	return &Result{RowsAffected: &n}, nil
}

func collect(rows *sql.Rows, maxRows int) (*Result, error) {
	defer func() { _ = rows.Close() }()
	types, err := rows.ColumnTypes()
	if err != nil {
		return nil, err
	}
	res := &Result{Columns: make([]Column, len(types)), Rows: [][]any{}}
	for i, t := range types {
		res.Columns[i] = Column{Name: t.Name(), Type: strings.ToLower(t.DatabaseTypeName())}
	}
	for rows.Next() {
		if len(res.Rows) >= maxRows {
			res.Truncated = true
			break
		}
		vals := make([]any, len(types))
		ptrs := make([]any, len(types))
		for i := range vals {
			ptrs[i] = &vals[i]
		}
		if err := rows.Scan(ptrs...); err != nil {
			return nil, err
		}
		for i, v := range vals {
			vals[i] = jsonValue(v, types[i].DatabaseTypeName())
		}
		res.Rows = append(res.Rows, vals)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	res.RowCount = len(res.Rows)
	return res, nil
}

// jsonValue turns driver values into something encoding/json renders well.
func jsonValue(v any, dbTypeName string) any {
	switch x := v.(type) {
	case nil, bool, int, int8, int16, int32, int64, uint, uint8, uint16, uint32, uint64:
		return x
	case float64:
		if math.IsNaN(x) || math.IsInf(x, 0) {
			return fmt.Sprint(x)
		}
		return x
	case float32:
		return jsonValue(float64(x), dbTypeName)
	case string:
		// Oracle's driver returns NUMBER as text; keep every digit, as a number.
		if strings.EqualFold(dbTypeName, "NUMBER") && jsonNumberRe.MatchString(x) {
			return json.Number(x)
		}
		return truncate(x)
	case []byte:
		if strings.EqualFold(dbTypeName, "UNIQUEIDENTIFIER") && len(x) == 16 {
			var u mssql.UniqueIdentifier
			if u.Scan(x) == nil {
				return u.String()
			}
		}
		if utf8.Valid(x) {
			return truncate(string(x))
		}
		return truncate("base64:" + base64.StdEncoding.EncodeToString(x))
	case time.Time:
		return x.Format(time.RFC3339Nano)
	default:
		return otherValue(v, dbTypeName)
	}
}

// otherValue handles what drivers like ClickHouse's return besides the
// database/sql basics: pointers for Nullable columns, arrays, maps, decimals.
func otherValue(v any, dbTypeName string) any {
	rv := reflect.ValueOf(v)
	if rv.Kind() == reflect.Pointer {
		if rv.IsNil() {
			return nil
		}
		return jsonValue(rv.Elem().Interface(), dbTypeName)
	}
	switch rv.Kind() {
	case reflect.Slice, reflect.Array, reflect.Map, reflect.Struct:
		if b, err := json.Marshal(v); err == nil && len(b) <= MaxCellChars {
			return json.RawMessage(b)
		}
	}
	if s, ok := v.(fmt.Stringer); ok {
		return truncate(s.String())
	}
	return truncate(fmt.Sprint(v))
}

func truncate(s string) string {
	if len(s) <= MaxCellChars {
		return s
	}
	cut := MaxCellChars
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	return s[:cut] + fmt.Sprintf("…[truncated %d bytes]", len(s)-cut)
}
