// Package web serves the admin UI and its JSON API: connection management and
// audit log browsing. Everything here sits behind HTTP basic auth.
package web

import (
	"context"
	"crypto/subtle"
	"embed"
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"net"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/slipalison/comporta/internal/db"
	"github.com/slipalison/comporta/internal/model"
	"github.com/slipalison/comporta/internal/store"
)

//go:embed static
var static embed.FS

type Meta struct {
	Version       string                            `json:"version"`
	RetentionDays int                               `json:"retention_days"`
	MaxRowsCap    int                               `json:"max_rows_cap"`
	Clients       []string                          `json:"mcp_clients"`
	DBTypes       map[model.DBType]model.DBTypeInfo `json:"db_types"`
}

type Server struct {
	store    *store.Store
	reg      *db.Registry
	onChange func()
	user     string
	pass     string
	meta     Meta
	limiter  *loginLimiter
}

func New(st *store.Store, reg *db.Registry, user, pass string, meta Meta, onChange func()) *Server {
	meta.DBTypes = model.DBTypes
	return &Server{store: st, reg: reg, user: user, pass: pass, meta: meta, onChange: onChange, limiter: newLoginLimiter()}
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	assets, _ := fs.Sub(static, "static")
	files := http.FileServerFS(assets)
	mux.Handle("GET /", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Embedded files have no modification time: revalidate on every load
		// so a new release is never served from a stale cache.
		w.Header().Set("Cache-Control", "no-cache")
		files.ServeHTTP(w, r)
	}))
	mux.HandleFunc("GET /api/meta", func(w http.ResponseWriter, _ *http.Request) { writeJSON(w, http.StatusOK, s.meta) })
	mux.HandleFunc("GET /api/connections", s.listConnections)
	mux.HandleFunc("POST /api/connections", s.createConnection)
	mux.HandleFunc("PUT /api/connections/{id}", s.updateConnection)
	mux.HandleFunc("DELETE /api/connections/{id}", s.deleteConnection)
	mux.HandleFunc("POST /api/test", s.testConnection)
	mux.HandleFunc("GET /api/audit", s.listAudit)
	mux.HandleFunc("GET /api/audit/stats", s.auditStats)
	mux.HandleFunc("GET /api/audit/export", s.exportAudit)
	// CrossOriginProtection rejects cross-site POST/PUT/DELETE (CSRF), which
	// matters because browsers resend basic-auth credentials automatically.
	return s.basicAuth(http.NewCrossOriginProtection().Handler(securityHeaders(mux)))
}

func (s *Server) basicAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		host := clientIP(r)
		if wait := s.limiter.blocked(host); wait > 0 {
			w.Header().Set("Retry-After", strconv.Itoa(int(wait.Seconds())+1))
			http.Error(w, "too many failed logins, try again later", http.StatusTooManyRequests)
			return
		}
		u, p, ok := r.BasicAuth()
		if !ok || subtle.ConstantTimeCompare([]byte(u), []byte(s.user)) != 1 ||
			subtle.ConstantTimeCompare([]byte(p), []byte(s.pass)) != 1 {
			if ok {
				s.limiter.fail(host)
			}
			w.Header().Set("WWW-Authenticate", `Basic realm="comporta", charset="UTF-8"`)
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func clientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("Content-Security-Policy", "default-src 'self'; img-src 'self' data:; script-src 'self'; style-src 'self'; connect-src 'self'; base-uri 'none'; form-action 'self'; frame-ancestors 'none'")
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("Referrer-Policy", "no-referrer")
		next.ServeHTTP(w, r)
	})
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(v); err != nil {
		slog.Error("writing response", "err", err)
	}
}

func writeErr(w http.ResponseWriter, status int, err error) {
	body := map[string]string{"error": err.Error()}
	var fe *fieldError
	if errors.As(err, &fe) {
		body["field"] = fe.Field
	}
	writeJSON(w, status, body)
}

type connView struct {
	model.Connection
	DSNMasked string `json:"dsn_masked"`
}

func (s *Server) listConnections(w http.ResponseWriter, r *http.Request) {
	conns, err := s.store.ListConnections(r.Context())
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	out := make([]connView, len(conns))
	for i, c := range conns {
		out[i] = connView{c, db.MaskDSN(c.DSN)}
	}
	writeJSON(w, http.StatusOK, out)
}

type connInput struct {
	Name        string `json:"name"`
	Type        string `json:"type"`
	Description string `json:"description"`
	DSN         string `json:"dsn"`
	Database    string `json:"database"`
	Enabled     bool   `json:"enabled"`
	AllowInsert bool   `json:"allow_insert"`
	AllowUpdate bool   `json:"allow_update"`
	AllowDelete bool   `json:"allow_delete"`
	AllowDDL    bool   `json:"allow_ddl"`
	MaxRows     int    `json:"max_rows"`
	TimeoutSec  int    `json:"timeout_sec"`
}

var (
	nameRe    = regexp.MustCompile(`^[a-z][a-z0-9_]{1,62}$`)
	nameSepRe = regexp.MustCompile(`[\s.-]+`)
)

// fieldError is a validation error the UI shows next to one form field.
type fieldError struct {
	Field string
	Msg   string
}

func (e *fieldError) Error() string { return e.Msg }

// NormalizeName accepts what people type ("Pagamentos Prod", "pagamentos-prod")
// and returns the canonical connection name ("pagamentos_prod").
func NormalizeName(s string) string {
	return nameSepRe.ReplaceAllString(strings.ToLower(strings.TrimSpace(s)), "_")
}

// apply validates the input onto c. An empty DSN keeps the current one.
func (s *Server) apply(in connInput, c *model.Connection) error {
	in.Name = NormalizeName(in.Name)
	if !nameRe.MatchString(in.Name) {
		return &fieldError{"name", "Use de 2 a 63 caracteres: letras sem acento, números e _, começando por letra. Ex.: pagamentos_prod"}
	}
	t := model.DBType(in.Type)
	if !t.Valid() {
		return &fieldError{"type", fmt.Sprintf("Tipo de banco desconhecido: %q", in.Type)}
	}
	if in.DSN = strings.TrimSpace(in.DSN); in.DSN != "" {
		c.DSN = in.DSN
	}
	if c.DSN == "" {
		return &fieldError{"dsn", "Informe a connection string. Ex.: " + t.Info().DSNExample}
	}
	if in.MaxRows <= 0 {
		in.MaxRows = 100
	}
	if in.TimeoutSec <= 0 {
		in.TimeoutSec = 30
	}
	if in.MaxRows > s.meta.MaxRowsCap {
		return &fieldError{"max_rows", fmt.Sprintf("O teto deste servidor é %d linhas por consulta.", s.meta.MaxRowsCap)}
	}
	if in.TimeoutSec > 600 {
		return &fieldError{"timeout_sec", "O timeout máximo é 600 segundos."}
	}
	c.Name, c.Type, c.Description, c.Database = in.Name, t, strings.TrimSpace(in.Description), strings.TrimSpace(in.Database)
	c.Enabled, c.AllowInsert, c.AllowUpdate, c.AllowDelete, c.AllowDDL = in.Enabled, in.AllowInsert, in.AllowUpdate, in.AllowDelete, in.AllowDDL
	c.MaxRows, c.TimeoutSec = in.MaxRows, in.TimeoutSec
	if err := s.reg.Validate(*c); err != nil {
		return &fieldError{"dsn", fmt.Sprintf("Connection string inválida para %s: %v", t.Info().Label, err)}
	}
	return nil
}

func decodeInput(w http.ResponseWriter, r *http.Request) (connInput, error) {
	var in connInput
	err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<16)).Decode(&in)
	return in, err
}

func pathID(r *http.Request) (int64, error) {
	return strconv.ParseInt(r.PathValue("id"), 10, 64)
}

func (s *Server) adminAudit(r *http.Request, op string, c model.Connection, detail string) {
	host := clientIP(r)
	err := s.store.InsertAudit(context.WithoutCancel(r.Context()), model.AuditEntry{
		Client: "admin-ui (" + s.user + "@" + host + ")", Connection: c.Name, DBType: string(c.Type),
		Tool: "admin", Operation: op, Statement: detail, Status: model.StatusOK,
	})
	if err != nil {
		slog.Error("audit write failed", "err", err)
	}
}

func (s *Server) changed() {
	if s.onChange != nil {
		s.onChange()
	}
}

func (s *Server) createConnection(w http.ResponseWriter, r *http.Request) {
	in, err := decodeInput(w, r)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	var c model.Connection
	if err := s.apply(in, &c); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	if err := s.store.CreateConnection(r.Context(), &c); err != nil {
		writeErr(w, http.StatusConflict, conflictErr(err))
		return
	}
	s.adminAudit(r, "connection.create", c, fmt.Sprintf("created %s connection; enabled=%t; allowed: %s",
		c.Type, c.Enabled, joinOps(c.AllowedOps())))
	s.changed()
	writeJSON(w, http.StatusCreated, connView{c, db.MaskDSN(c.DSN)})
}

func (s *Server) updateConnection(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	old, err := s.store.GetConnection(r.Context(), id)
	if err != nil {
		writeErr(w, http.StatusNotFound, err)
		return
	}
	in, err := decodeInput(w, r)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	c := old
	if err := s.apply(in, &c); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	if err := s.store.UpdateConnection(r.Context(), &c); err != nil {
		writeErr(w, http.StatusConflict, conflictErr(err))
		return
	}
	s.reg.Forget(id)
	if d := diff(old, c); d != "" {
		s.adminAudit(r, "connection.update", c, d)
	}
	s.changed()
	writeJSON(w, http.StatusOK, connView{c, db.MaskDSN(c.DSN)})
}

func (s *Server) deleteConnection(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	c, err := s.store.GetConnection(r.Context(), id)
	if err != nil {
		writeErr(w, http.StatusNotFound, err)
		return
	}
	if err := s.store.DeleteConnection(r.Context(), id); err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	s.reg.Forget(id)
	s.adminAudit(r, "connection.delete", c, "deleted")
	s.changed()
	w.WriteHeader(http.StatusNoContent)
}

// conflictErr points a duplicate-name error at the name field.
func conflictErr(err error) error {
	if errors.Is(err, store.ErrDuplicateName) {
		return &fieldError{"name", err.Error()}
	}
	return err
}

func joinOps(ops []model.Op) string {
	parts := make([]string, len(ops))
	for i, op := range ops {
		parts[i] = string(op)
	}
	return strings.Join(parts, ", ")
}

func diff(a, b model.Connection) string {
	var ch []string
	add := func(field string, x, y any) {
		if x != y {
			ch = append(ch, fmt.Sprintf("%s: %v → %v", field, x, y))
		}
	}
	add("name", a.Name, b.Name)
	add("type", a.Type, b.Type)
	add("description", a.Description, b.Description)
	add("database", a.Database, b.Database)
	add("enabled", a.Enabled, b.Enabled)
	add("allow_insert", a.AllowInsert, b.AllowInsert)
	add("allow_update", a.AllowUpdate, b.AllowUpdate)
	add("allow_delete", a.AllowDelete, b.AllowDelete)
	add("allow_ddl", a.AllowDDL, b.AllowDDL)
	add("max_rows", a.MaxRows, b.MaxRows)
	add("timeout_sec", a.TimeoutSec, b.TimeoutSec)
	if a.DSN != b.DSN {
		ch = append(ch, "dsn: changed")
	}
	return strings.Join(ch, "; ")
}

func (s *Server) testConnection(w http.ResponseWriter, r *http.Request) {
	var in struct {
		ID       int64  `json:"id"`
		Type     string `json:"type"`
		DSN      string `json:"dsn"`
		Database string `json:"database"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<16)).Decode(&in); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	c := model.Connection{Type: model.DBType(in.Type), DSN: strings.TrimSpace(in.DSN), Database: in.Database}
	if c.DSN == "" && in.ID > 0 {
		old, err := s.store.GetConnection(r.Context(), in.ID)
		if err != nil {
			writeErr(w, http.StatusNotFound, err)
			return
		}
		c.DSN = old.DSN
	}
	if !c.Type.Valid() {
		writeErr(w, http.StatusBadRequest, fmt.Errorf("tipo inválido %q", in.Type))
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	start := time.Now()
	if err := s.reg.Test(ctx, c); err != nil {
		writeJSON(w, http.StatusOK, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "ms": time.Since(start).Milliseconds()})
}

func auditFilter(r *http.Request, defLimit, maxLimit int) store.AuditFilter {
	q := r.URL.Query()
	f := store.AuditFilter{
		Connection: q.Get("connection"),
		Status:     q.Get("status"),
		Operation:  q.Get("operation"),
		Tool:       q.Get("tool"),
		Client:     q.Get("client"),
		Search:     q.Get("q"),
		Since:      parseTime(q.Get("since")),
		Until:      parseTime(q.Get("until")),
		Limit:      defLimit,
	}
	f.BeforeID, _ = strconv.ParseInt(q.Get("before_id"), 10, 64)
	if n, err := strconv.Atoi(q.Get("limit")); err == nil && n > 0 {
		f.Limit = min(n, maxLimit)
	}
	return f
}

func parseTime(s string) time.Time {
	for _, layout := range []string{time.RFC3339, "2006-01-02T15:04", "2006-01-02"} {
		if t, err := time.ParseInLocation(layout, s, time.Local); err == nil {
			return t
		}
	}
	return time.Time{}
}

func (s *Server) listAudit(w http.ResponseWriter, r *http.Request) {
	entries, err := s.store.ListAudit(r.Context(), auditFilter(r, 100, 500))
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, entries)
}

func (s *Server) auditStats(w http.ResponseWriter, r *http.Request) {
	since := time.Now().Add(-24 * time.Hour)
	st, err := s.store.Stats(r.Context(), since)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	act, err := s.store.ConnectionActivity(r.Context(), since)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, struct {
		store.AuditStats
		Connections map[string]store.Activity `json:"connections"`
	}{st, act})
}

func (s *Server) exportAudit(w http.ResponseWriter, r *http.Request) {
	entries, err := s.store.ListAudit(r.Context(), auditFilter(r, 50000, 50000))
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="comporta-audit-%s.csv"`, time.Now().Format("20060102-150405")))
	cw := csv.NewWriter(w)
	_ = cw.Write([]string{"id", "timestamp", "client", "connection", "db_type", "tool", "operation", "status", "rows", "duration_ms", "statement", "params", "message"})
	for _, e := range entries {
		// csv.Writer keeps the first error; it is reported by cw.Error below.
		_ = cw.Write([]string{strconv.FormatInt(e.ID, 10), e.Time.Format(time.RFC3339), csvSafe(e.Client), csvSafe(e.Connection), e.DBType, e.Tool,
			e.Operation, e.Status, strconv.FormatInt(e.Rows, 10), strconv.FormatInt(e.DurationMS, 10),
			csvSafe(e.Statement), csvSafe(e.Params), csvSafe(e.Message)})
	}
	cw.Flush()
	if err := cw.Error(); err != nil {
		slog.Error("audit export interrupted", "err", err)
	}
}

// csvSafe defuses spreadsheet formula injection in exported text.
func csvSafe(s string) string {
	if s != "" && strings.ContainsRune("=+-@\t\r", rune(s[0])) {
		return "'" + s
	}
	return s
}
