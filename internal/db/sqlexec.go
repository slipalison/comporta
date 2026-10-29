package db

import (
	"context"
	"database/sql"
	"encoding/base64"
	"fmt"
	"math"
	"strings"
	"time"
	"unicode/utf8"

	mssql "github.com/microsoft/go-mssqldb"

	"dbmcp/internal/model"
)

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

// RunSQL executes one statement inside a transaction. Reads are rolled back
// (and use READ ONLY transactions where the engine supports them), so a write
// hidden inside a "read" cannot persist even if the classifier missed it.
func RunSQL(ctx context.Context, pool *sql.DB, dbType model.DBType, req SQLRequest) (*Result, error) {
	opts := &sql.TxOptions{}
	if !req.Write && (dbType == model.Postgres || dbType == model.MySQL) {
		opts.ReadOnly = true
	}
	tx, err := pool.BeginTx(ctx, opts)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()

	var res *Result
	if !req.Write || req.ReturnsRows {
		rows, err := tx.QueryContext(ctx, req.Query, req.Args...)
		if err != nil {
			return nil, err
		}
		res, err = collect(rows, req.MaxRows)
		if err != nil {
			return nil, err
		}
	} else {
		r, err := tx.ExecContext(ctx, req.Query, req.Args...)
		if err != nil {
			return nil, err
		}
		n, _ := r.RowsAffected()
		res = &Result{RowsAffected: &n}
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

func collect(rows *sql.Rows, maxRows int) (*Result, error) {
	defer rows.Close()
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
	case nil, bool, int64, int32, int, uint64:
		return x
	case float64:
		if math.IsNaN(x) || math.IsInf(x, 0) {
			return fmt.Sprint(x)
		}
		return x
	case float32:
		return jsonValue(float64(x), dbTypeName)
	case string:
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
		return truncate(fmt.Sprint(x))
	}
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
