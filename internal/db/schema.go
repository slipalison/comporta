package db

import (
	"context"
	"database/sql"
	"fmt"

	"dbmcp/internal/model"
)

// DescribeSQL lists tables and views, or the columns of one table.
func DescribeSQL(ctx context.Context, pool *sql.DB, dbType model.DBType, schema, table string) (*Result, error) {
	var q string
	var args []any
	if table == "" {
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
			return nil, fmt.Errorf("unsupported type %s", dbType)
		}
		q += " ORDER BY 1, 2"
	} else {
		switch dbType {
		case model.Postgres:
			q = `SELECT table_schema, column_name, data_type, is_nullable, column_default
				FROM information_schema.columns WHERE table_name = $1`
			args = append(args, table)
			if schema != "" {
				q += " AND table_schema = $2"
				args = append(args, schema)
			}
			q += " ORDER BY table_schema, ordinal_position"
		case model.SQLServer:
			q = `SELECT TABLE_SCHEMA AS table_schema, COLUMN_NAME AS column_name, DATA_TYPE AS data_type,
				IS_NULLABLE AS is_nullable, COLUMN_DEFAULT AS column_default
				FROM INFORMATION_SCHEMA.COLUMNS WHERE TABLE_NAME = @p1`
			args = append(args, table)
			if schema != "" {
				q += " AND TABLE_SCHEMA = @p2"
				args = append(args, schema)
			}
			q += " ORDER BY TABLE_SCHEMA, ORDINAL_POSITION"
		case model.MySQL:
			q = `SELECT TABLE_SCHEMA AS table_schema, COLUMN_NAME AS column_name, COLUMN_TYPE AS data_type,
				IS_NULLABLE AS is_nullable, COLUMN_DEFAULT AS column_default, COLUMN_KEY AS column_key
				FROM information_schema.COLUMNS WHERE TABLE_NAME = ? AND TABLE_SCHEMA = COALESCE(?, DATABASE())
				ORDER BY ORDINAL_POSITION`
			args = append(args, table, nullable(schema))
		case model.SQLite:
			q = `SELECT 'main' AS table_schema, name AS column_name, type AS data_type,
				CASE "notnull" WHEN 1 THEN 'NO' ELSE 'YES' END AS is_nullable, dflt_value AS column_default, pk
				FROM pragma_table_info(?) ORDER BY cid`
			args = append(args, table)
		default:
			return nil, fmt.Errorf("unsupported type %s", dbType)
		}
	}
	return RunSQL(ctx, pool, dbType, SQLRequest{Query: q, Args: args, MaxRows: 2000})
}

func nullable(s string) any {
	if s == "" {
		return nil
	}
	return s
}
