package db

import (
	"strings"
	"testing"

	"github.com/slipalison/comporta/internal/model"
)

func TestMaskDSN(t *testing.T) {
	cases := map[string]string{
		"postgres://app:s3cr3t@db:5432/pay?sslmode=disable":  "postgres://app:****@db:5432/pay?sslmode=disable",
		"mongodb://u:p@ss@h1:27017,h2:27017/db?authSource=a": "mongodb://u:****@h1:27017,h2:27017/db?authSource=a",
		"sqlserver://sa:Pa$$w0rd@sql:1433?database=x":        "sqlserver://sa:****@sql:1433?database=x",
		"app:s3cr3t@tcp(db:3306)/pay?parseTime=true":         "app:****@tcp(db:3306)/pay?parseTime=true",
		"host=db user=app password=s3cr3t dbname=pay":        "host=db user=app password=**** dbname=pay",
		"server=sql;user id=sa;Password=s3cr3t;database=x":   "server=sql;user id=sa;Password=****;database=x",
		"/data/files/app.db":                                 "/data/files/app.db",
	}
	for in, want := range cases {
		if got := MaskDSN(in); got != want {
			t.Errorf("MaskDSN(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestSQLDSN(t *testing.T) {
	r := NewRegistry("/data/comporta.db")
	_, dsn, err := r.sqlDSN(model.Connection{Type: model.MySQL, DSN: "mysql://app:p%40ss@db/pay?multiStatements=true"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(dsn, "app:p@ss@tcp(db:3306)/pay") || strings.Contains(dsn, "multiStatements=true") {
		t.Errorf("unexpected mysql DSN %q", dsn)
	}
	if _, _, err := r.sqlDSN(model.Connection{Type: model.SQLite, DSN: "/data/comporta.db"}); err == nil {
		t.Error("the gateway's own database must be rejected")
	}
	if _, _, err := r.sqlDSN(model.Connection{Type: model.SQLite, DSN: "sqlite:///data/comporta.db-wal"}); err == nil {
		t.Error("the gateway's WAL file must be rejected")
	}
}
