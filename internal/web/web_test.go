package web

import (
	"context"
	"database/sql"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	_ "modernc.org/sqlite"

	"github.com/slipalison/comporta/internal/db"
	"github.com/slipalison/comporta/internal/model"
	"github.com/slipalison/comporta/internal/store"
)

type env struct {
	h       http.Handler
	st      *store.Store
	target  string
	changes int
}

func newEnv(t *testing.T) *env {
	t.Helper()
	dir := t.TempDir()
	cipher, err := store.NewCipher(make([]byte, 32))
	failIf(t, err != nil, "%v", err)
	st, err := store.Open(filepath.Join(dir, "comporta.db"), cipher)
	failIf(t, err != nil, "%v", err)
	t.Cleanup(func() { _ = st.Close() })
	target := filepath.Join(dir, "loja.db")
	tdb, err := sql.Open("sqlite", target)
	failIf(t, err != nil, "%v", err)
	if _, err := tdb.ExecContext(context.Background(), "CREATE TABLE t (id INTEGER)"); err != nil {
		t.Fatal(err)
	}
	_ = tdb.Close()
	reg := db.NewRegistry(st.Path)
	t.Cleanup(reg.Close)
	e := &env{st: st, target: target}
	srv := New(st, reg, "admin", "s3nha", Meta{Version: "test", RetentionDays: 30, MaxRowsCap: 500, Clients: []string{"claude-code"}},
		func() { e.changes++ })
	e.h = srv.Handler()
	return e
}

func (e *env) do(t *testing.T, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	var r io.Reader
	if body != "" {
		r = strings.NewReader(body)
	}
	req := httptest.NewRequest(method, path, r)
	req.SetBasicAuth("admin", "s3nha")
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	rec := httptest.NewRecorder()
	e.h.ServeHTTP(rec, req)
	return rec
}

func decodeBody[T any](t *testing.T, rec *httptest.ResponseRecorder) T {
	t.Helper()
	var v T
	if err := json.Unmarshal(rec.Body.Bytes(), &v); err != nil {
		t.Fatalf("decoding %q: %v", rec.Body.String(), err)
	}
	return v
}

func TestAuthAndHeaders(t *testing.T) {
	e := newEnv(t)
	req := httptest.NewRequest(http.MethodGet, "/api/meta", nil)
	rec := httptest.NewRecorder()
	e.h.ServeHTTP(rec, req)
	failIf(t, rec.Code != http.StatusUnauthorized || !strings.Contains(rec.Header().Get("WWW-Authenticate"), "comporta"), "no credentials: %d %v", rec.Code, rec.Header())

	rec = e.do(t, http.MethodGet, "/", "")
	failIf(t, rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "<title>Comporta</title>"), "index: %d", rec.Code)
	csp := rec.Header().Get("Content-Security-Policy")
	failIf(t, strings.Contains(csp, "unsafe-inline") || !strings.Contains(csp, "script-src 'self'"), "CSP must not allow inline code: %q", csp)
	failIf(t, rec.Header().Get("Cache-Control") != "no-cache", "static files must be revalidated")
	for _, f := range []string{"/app.js", "/app.css", "/favicon.svg"} {
		if rec := e.do(t, http.MethodGet, f, ""); rec.Code != http.StatusOK {
			t.Errorf("%s: %d", f, rec.Code)
		}
	}

	meta := decodeBody[Meta](t, e.do(t, http.MethodGet, "/api/meta", ""))
	failIf(t, meta.MaxRowsCap != 500 || len(meta.DBTypes) != 8, "meta: %+v", meta)
}

func TestLoginLimiter(t *testing.T) {
	e := newEnv(t)
	try := func(pass string) int {
		req := httptest.NewRequest(http.MethodGet, "/api/meta", nil)
		req.RemoteAddr = "10.0.0.9:5000"
		req.SetBasicAuth("admin", pass)
		rec := httptest.NewRecorder()
		e.h.ServeHTTP(rec, req)
		return rec.Code
	}
	for range maxFailures {
		if code := try("errada"); code != http.StatusUnauthorized {
			t.Fatalf("wrong password: %d", code)
		}
	}
	if code := try("s3nha"); code != http.StatusTooManyRequests {
		t.Fatalf("after %d failures even the right password waits, got %d", maxFailures, code)
	}

	l := newLoginLimiter()
	now := time.Now()
	l.now = func() time.Time { return now }
	for range maxFailures {
		l.fail("h")
	}
	failIf(t, l.blocked("h") <= 0, "host should be blocked")
	now = now.Add(window + time.Second)
	failIf(t, l.blocked("h") != 0 || len(l.failures) != 0, "failures must expire after the window")
}

func TestConnectionLifecycle(t *testing.T) {
	e := newEnv(t)

	// Postel: the name is normalized, not rejected.
	body := `{"name":" Loja Local ","type":"sqlite","dsn":"` + e.target + `","enabled":true,"allow_insert":true}`
	rec := e.do(t, http.MethodPost, "/api/connections", body)
	failIf(t, rec.Code != http.StatusCreated, "create: %d %s", rec.Code, rec.Body.String())
	created := decodeBody[connView](t, rec)
	failIf(t, created.Name != "loja_local" || created.MaxRows != 100 || created.TimeoutSec != 30, "created: %+v", created)

	errs := []struct{ body, field, code string }{
		{`{"name":"1x","type":"sqlite","dsn":"a.db"}`, "name", "name_invalid"},
		{`{"name":"abc","type":"db2","dsn":"x"}`, "type", "type_invalid"},
		{`{"name":"abc","type":"postgres"}`, "dsn", "dsn_required"},
		{`{"name":"abc","type":"sqlite","dsn":"a.db","max_rows":501}`, "max_rows", "max_rows_cap"},
		{`{"name":"abc","type":"sqlite","dsn":"a.db","timeout_sec":601}`, "timeout_sec", "timeout_max"},
		{`{"name":"abc","type":"mongodb","dsn":"http://x"}`, "dsn", "dsn_invalid"},
		{`{"name":"abc","type":"sqlite","dsn":"` + e.st.Path + `"}`, "dsn", "dsn_invalid"},
	}
	for _, tc := range errs {
		rec := e.do(t, http.MethodPost, "/api/connections", tc.body)
		got := decodeBody[apiError](t, rec)
		if rec.Code != http.StatusBadRequest || got.Field != tc.field || got.Code != tc.code || got.Error == "" {
			t.Errorf("%s: %d %+v, want field %s code %s", tc.body, rec.Code, got, tc.field, tc.code)
		}
	}
	rec = e.do(t, http.MethodPost, "/api/connections", body)
	dup := decodeBody[apiError](t, rec)
	failIf(t, rec.Code != http.StatusConflict || dup.Field != "name" || dup.Code != "name_taken" || dup.Params["name"] != "loja_local",
		"duplicate: %d %+v", rec.Code, dup)

	id := "/api/connections/" + itoa(created.ID)
	rec = e.do(t, http.MethodPut, id, `{"name":"loja_local","type":"sqlite","enabled":false,"allow_ddl":true,"description":"nova"}`)
	failIf(t, rec.Code != http.StatusOK, "update: %d %s", rec.Code, rec.Body.String())
	updated := decodeBody[connView](t, rec)
	failIf(t, updated.Enabled || !updated.AllowDDL || updated.AllowInsert || updated.Description != "nova", "updated: %+v", updated)

	list := decodeBody[[]connView](t, e.do(t, http.MethodGet, "/api/connections", ""))
	failIf(t, len(list) != 1 || list[0].DSNMasked != e.target, "list: %+v", list)

	test := decodeBody[map[string]any](t, e.do(t, http.MethodPost, "/api/test", `{"id":`+itoa(created.ID)+`,"type":"sqlite"}`))
	failIf(t, test["ok"] != true, "test saved connection: %v", test)
	test = decodeBody[map[string]any](t, e.do(t, http.MethodPost, "/api/test", `{"type":"postgres","dsn":"::not a dsn"}`))
	failIf(t, test["ok"] != false, "test bad dsn: %v", test)
	if rec := e.do(t, http.MethodPost, "/api/test", `{"type":"db2","dsn":"x"}`); rec.Code != http.StatusBadRequest {
		t.Fatalf("test bad type: %d", rec.Code)
	}

	if rec := e.do(t, http.MethodDelete, id, ""); rec.Code != http.StatusNoContent {
		t.Fatalf("delete: %d", rec.Code)
	}
	if rec := e.do(t, http.MethodDelete, id, ""); rec.Code != http.StatusNotFound {
		t.Fatalf("delete again: %d", rec.Code)
	}
	if rec := e.do(t, http.MethodPut, id, body); rec.Code != http.StatusNotFound {
		t.Fatalf("update missing: %d", rec.Code)
	}
	failIf(t, e.changes != 3, "onChange called %d times, want 3", e.changes)

	// Every admin change is in the audit log, with what changed.
	entries := decodeBody[[]model.AuditEntry](t, e.do(t, http.MethodGet, "/api/audit?tool=admin", ""))
	failIf(t, len(entries) != 3 || entries[1].Operation != "connection.update" || !strings.Contains(entries[1].Statement, "allow_ddl: false → true"), "admin audit: %+v", entries)
}

func TestAuditEndpoints(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	now := time.Now()
	for i, st := range []string{model.StatusOK, model.StatusDenied, model.StatusError, model.StatusOK} {
		err := e.st.InsertAudit(ctx, model.AuditEntry{Time: now.Add(time.Duration(i) * time.Second), Client: "claude-code", Connection: "loja",
			Tool: "sql_query", Operation: "select", Statement: "=SELECT " + st, Status: st})
		failIf(t, err != nil, "%v", err)
	}
	stats := decodeBody[struct {
		store.AuditStats
		Connections map[string]store.Activity `json:"connections"`
	}](t, e.do(t, http.MethodGet, "/api/audit/stats", ""))
	failIf(t, stats.Total != 4 || stats.Denied != 1 || stats.Connections["loja"].Calls != 4 || stats.Connections["loja"].Errors != 1, "stats: %+v", stats)

	denied := decodeBody[[]model.AuditEntry](t, e.do(t, http.MethodGet, "/api/audit?status=denied&q=SELECT&connection=loja", ""))
	failIf(t, len(denied) != 1, "filtered: %+v", denied)
	since := now.Add(2500 * time.Millisecond).UTC().Format(time.RFC3339Nano)
	if got := decodeBody[[]model.AuditEntry](t, e.do(t, http.MethodGet, "/api/audit?since="+since, "")); len(got) != 1 {
		t.Fatalf("since: %+v", got)
	}
	page := decodeBody[[]model.AuditEntry](t, e.do(t, http.MethodGet, "/api/audit?limit=2", ""))
	next := decodeBody[[]model.AuditEntry](t, e.do(t, http.MethodGet, "/api/audit?limit=2&before_id="+itoa(page[1].ID), ""))
	failIf(t, len(page) != 2 || len(next) != 2 || next[0].ID >= page[1].ID, "pagination: %v %v", page, next)

	rec := e.do(t, http.MethodGet, "/api/audit/export", "")
	if !strings.HasPrefix(rec.Header().Get("Content-Disposition"), `attachment; filename="comporta-audit-`) {
		t.Fatalf("export headers: %v", rec.Header())
	}
	failIf(t, !strings.Contains(rec.Body.String(), "'=SELECT ok"), "export must defuse formulas: %s", rec.Body.String())
}

func TestNormalizeName(t *testing.T) {
	for in, want := range map[string]string{"Pagamentos Prod": "pagamentos_prod", "a-b.c  d": "a_b_c_d", "ok_1": "ok_1"} {
		if got := NormalizeName(in); got != want {
			t.Errorf("NormalizeName(%q) = %q, want %q", in, got, want)
		}
	}
}

func itoa(n int64) string { return strconv.FormatInt(n, 10) }

// failIf stops the test with the message when bad is true.
func failIf(t *testing.T, bad bool, format string, args ...any) {
	t.Helper()
	if bad {
		t.Fatalf(format, args...)
	}
}
