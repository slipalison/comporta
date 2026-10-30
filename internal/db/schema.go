package db

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/slipalison/comporta/internal/model"
)

// DescribeSQL lists tables and views, or the columns of one table.
func DescribeSQL(ctx context.Context, pool *sql.DB, dbType model.DBType, schema, table string) (*Result, error) {
	q, args, err := tablesQuery(dbType, schema)
	if table != "" {
		q, args, err = columnsQuery(dbType, schema, table)
	}
	if err != nil {
		return nil, err
	}
	return RunSQL(ctx, pool, dbType, SQLRequest{Query: q, Args: args, MaxRows: 2000})
}

func tablesQuery(dbType model.DBType, schema string) (string, []any, error) {
	var q string
	var args []any
	switch dbType {
	case model.Postgres:
		q = `SELECT table_schema, table_name, table_type FROM information_schema.tables
			WHERE table_schema NOT IN ('pg_catalog', 'information_schema')`
		if schema != "" {
			q += " AND table_schema = $1"
			args = append(args, schema)
		}
	case model.SQLServer:
		q = "SELECT TABLE_SCHEMA AS table_schema, TABLE_NAME AS table_name, TABLE_TYPE AS table_type FROM INFORMATION_SCHEMA.TABLES"
		if schema != "" {
			q += " WHERE TABLE_SCHEMA = @p1"
			args = append(args, schema)
		}
	case model.MySQL:
		q = `SELECT TABLE_SCHEMA AS table_schema, TABLE_NAME AS table_name, TABLE_TYPE AS table_type
			FROM information_schema.TABLES WHERE TABLE_SCHEMA = COALESCE(?, DATABASE())`
		args = append(args, nullable(schema))
	case model.SQLite:
		q = `SELECT 'main' AS table_schema, name AS table_name, type AS table_type FROM sqlite_master
			WHERE type IN ('table', 'view') AND name NOT LIKE 'sqlite_%'`
	default:
		return "", nil, fmt.Errorf("unsupported type %s", dbType)
	}
	return q + " ORDER BY 1, 2", args, nil
}

func columnsQuery(dbType model.DBType, schema, table string) (string, []any, error) {
	switch dbType {
	case model.Postgres:
		q := `SELECT table_schema, column_name, data_type, is_nullable, column_default
			FROM information_schema.columns WHERE table_name = $1`
		args := []any{table}
		if schema != "" {
			q += " AND table_schema = $2"
			args = append(args, schema)
		}
		return q + " ORDER BY table_schema, ordinal_position", args, nil
	case model.SQLServer:
		q := `SELECT TABLE_SCHEMA AS table_schema, COLUMN_NAME AS column_name, DATA_TYPE AS data_type,
			IS_NULLABLE AS is_nullable, COLUMN_DEFAULT AS column_default
			FROM INFORMATION_SCHEMA.COLUMNS WHERE TABLE_NAME = @p1`
		args := []any{table}
		if schema != "" {
			q += " AND TABLE_SCHEMA = @p2"
			args = append(args, schema)
		}
		return q + " ORDER BY TABLE_SCHEMA, ORDINAL_POSITION", args, nil
	case model.MySQL:
		return `SELECT TABLE_SCHEMA AS table_schema, COLUMN_NAME AS column_name, COLUMN_TYPE AS data_type,
			IS_NULLABLE AS is_nullable, COLUMN_DEFAULT AS column_default, COLUMN_KEY AS column_key
			FROM information_schema.COLUMNS WHERE TABLE_NAME = ? AND TABLE_SCHEMA = COALESCE(?, DATABASE())
			ORDER BY ORDINAL_POSITION`, []any{table, nullable(schema)}, nil
	case model.SQLite:
		return `SELECT 'main' AS table_schema, name AS column_name, type AS data_type,
			CASE "notnull" WHEN 1 THEN 'NO' ELSE 'YES' END AS is_nullable, dflt_value AS column_default, pk
			FROM pragma_table_info(?) ORDER BY cid`, []any{table}, nil
	}
	return "", nil, fmt.Errorf("unsupported type %s", dbType)
}

func nullable(s string) any {
	if s == "" {
		return nil
	}
	return s
}
