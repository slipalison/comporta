package guard

import (
	"slices"
	"testing"

	"github.com/slipalison/comporta/internal/model"
)

func TestAnalyzeSQL(t *testing.T) {
	const (
		pg    = model.Postgres
		ms    = model.SQLServer
		my    = model.MySQL
		lite  = model.SQLite
		read  = ""
		ins   = "insert"
		upd   = "update"
		del   = "delete"
		ddl   = "ddl"
		insUp = "insert+update"
	)
	cases := []struct {
		db   model.DBType
		sql  string
		want string
	}{
		{pg, "SELECT * FROM users", read},
		{pg, "select id, \"delete\" from t where name = 'DROP TABLE x' -- update", read},
		{pg, "WITH x AS (SELECT 1) SELECT * FROM x", read},
		{pg, "WITH d AS (DELETE FROM t RETURNING *) SELECT * FROM d", del},
		{pg, "EXPLAIN ANALYZE DELETE FROM t", del},
		{pg, "SELECT $$ DELETE FROM t $$, $tag$ DROP $tag$", read},
		{pg, "SELECT * FROM t WHERE id = $1", read},
		{pg, "SELECT /* outer /* nested */ DELETE */ 1", read},
		{pg, "SELECT * FROM t FOR UPDATE", upd},
		{pg, "INSERT INTO t (a) VALUES (1)", ins},
		{pg, "INSERT INTO t (a) VALUES (1) ON CONFLICT (a) DO UPDATE SET a = 2", insUp},
		{pg, "UPDATE t SET a = 1 WHERE id = 2", upd},
		{pg, "DELETE FROM t WHERE id = 1", del},
		{pg, "TRUNCATE t", "delete+ddl"},
		{pg, "DROP TABLE t", ddl},
		{pg, "COPY t TO PROGRAM 'rm -rf /'", ddl},
		{pg, "SET ROLE postgres", ddl},
		{pg, "BEGIN", ddl},
		{pg, "DO $$ BEGIN DELETE FROM t; END $$", ddl},
		{pg, "CALL do_stuff()", ddl},
		{pg, "SELECT * INTO new_t FROM t", "insert+ddl"},
		{pg, "SELECT t.update, t.delete FROM t", read},
		{pg, "VACUUM t", ddl},
		{pg, "SHOW search_path", read},
		{pg, "TABLE users", read},
		{pg, "(SELECT 1)", read},

		{ms, "SELECT TOP (10) * FROM [dbo].[Users] WHERE [delete] = 1", read},
		{ms, "SELECT 1 DELETE FROM t", del},
		{ms, "SELECT 1 COMMIT", ddl},
		{ms, "SELECT 1 ROLLBACK DELETE FROM t", "delete+ddl"},
		{ms, "EXEC sp_who", ddl},
		{ms, "SELECT * FROM OPENQUERY(srv, 'DELETE FROM t')", ddl},
		{ms, "UPDATE t SET a = @p1 WHERE id = @p2", upd},
		{ms, "SET NOCOUNT ON", ddl},
		{ms, "SELECT @delete = 1", read},
		{ms, "SELECT * INTO #tmp FROM t", "insert+ddl"},
		{ms, "MERGE INTO t USING s ON t.id = s.id WHEN MATCHED THEN UPDATE SET a = 1;", "insert+update+delete"},
		{ms, "INSERT INTO t OUTPUT inserted.id VALUES (1)", ins},
		{ms, "SELECT N'it''s DELETE'", read},

		{my, "SELECT INSERT('abc', 1, 1, 'x'), REPLACE(a, 'b', 'c'), TRUNCATE(1.23, 1) FROM t", read},
		{my, "SELECT 'it\\'s DELETE' FROM t", read},
		{my, "SELECT `delete` FROM t # DROP TABLE", read},
		{my, "REPLACE INTO t VALUES (1)", "insert+delete"},
		{my, "SELECT * FROM t INTO OUTFILE '/tmp/x'", "insert+ddl"},
		{my, "LOAD DATA INFILE 'x' INTO TABLE t", "insert+ddl"},
		{my, "SELECT * FROM t LOCK IN SHARE MODE", read},

		{lite, "SELECT * FROM [order] WHERE `delete` = 1", read},
		{lite, "PRAGMA table_info(t)", ddl},
		{lite, "SELECT t.attach FROM t", read},
		// SQLite does not nest comments: the DELETE below is real code
		{lite, "SELECT /* /* */ 1 DELETE FROM t", del},
	}
	for _, tc := range cases {
		a, err := AnalyzeSQL(tc.db, tc.sql)
		if err != nil {
			t.Errorf("%s %q: unexpected error %v", tc.db, tc.sql, err)
			continue
		}
		got := a.Operation()
		if a.ReadOnly() {
			got = ""
		}
		if got != tc.want {
			t.Errorf("%s %q: got %q, want %q", tc.db, tc.sql, got, tc.want)
		}
	}
}

func TestAnalyzeSQLRejects(t *testing.T) {
	cases := []struct {
		db  model.DBType
		sql string
	}{
		{model.Postgres, ""},
		{model.Postgres, "  ; ;  "},
		{model.Postgres, "SELECT 1; DELETE FROM t"},
		{model.Postgres, "SELECT 1; SELECT 2"},
		{model.Postgres, "SELECT 'unterminated"},
		{model.Postgres, "SELECT /* unterminated"},
		{model.Postgres, "SELECT $x$ unterminated"},
		{model.MySQL, "SELECT /*! DELETE FROM t */ 1"},
		{model.MySQL, "SELECT /*M! DELETE FROM t */ 1"},
		{model.SQLite, "ATTACH DATABASE '/data/comporta.db' AS own"},
		{model.SQLite, "attach '/data/comporta.db' as own"},
		{model.SQLite, "DETACH own"},
	}
	for _, tc := range cases {
		if _, err := AnalyzeSQL(tc.db, tc.sql); err == nil {
			t.Errorf("%s %q: expected error", tc.db, tc.sql)
		}
	}
}

func TestReturnsRows(t *testing.T) {
	a, _ := AnalyzeSQL(model.Postgres, "INSERT INTO t VALUES (1) RETURNING id")
	if !a.ReturnsRows || !slices.Equal(a.Ops, []model.Op{model.OpInsert}) {
		t.Fatalf("RETURNING not detected: %+v", a)
	}
	a, _ = AnalyzeSQL(model.Postgres, "UPDATE t SET a = 1")
	if a.ReturnsRows {
		t.Fatalf("plain UPDATE should not return rows: %+v", a)
	}
}
