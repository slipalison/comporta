// Package db keeps one connection pool per configured connection and runs
// SQL and MongoDB operations against them.
package db

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/ClickHouse/clickhouse-go/v2"
	"github.com/go-sql-driver/mysql"
	_ "github.com/jackc/pgx/v5/stdlib"  // registers the "pgx" database/sql driver
	_ "github.com/microsoft/go-mssqldb" // registers the "sqlserver" driver
	"github.com/redis/go-redis/v9"
	goora "github.com/sijms/go-ora/v2" // registers the "oracle" driver (pure Go)
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
	"go.mongodb.org/mongo-driver/v2/x/mongo/driver/connstring"
	_ "modernc.org/sqlite" // registers the "sqlite" driver (pure Go, no cgo)

	"github.com/slipalison/comporta/internal/model"
)

type handle struct {
	fingerprint string
	sql         *sql.DB
	mongo       *mongo.Client
	redis       *redis.Client
}

func (h *handle) close() {
	if h.sql != nil {
		_ = h.sql.Close()
	}
	if h.mongo != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = h.mongo.Disconnect(ctx)
	}
	if h.redis != nil {
		_ = h.redis.Close()
	}
}

// Registry lazily opens pools and reuses them across calls. A pool is
// replaced when the connection's type, DSN or database changes.
type Registry struct {
	mu        sync.Mutex
	handles   map[int64]*handle
	storePath string // the gateway's own SQLite file, never exposed as a target
}

func NewRegistry(storePath string) *Registry {
	return &Registry{handles: map[int64]*handle{}, storePath: storePath}
}

func fingerprint(c model.Connection) string {
	return string(c.Type) + "\x00" + c.DSN + "\x00" + c.Database
}

func (r *Registry) get(c model.Connection) (*handle, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	fp := fingerprint(c)
	if h, ok := r.handles[c.ID]; ok {
		if h.fingerprint == fp {
			return h, nil
		}
		h.close()
		delete(r.handles, c.ID)
	}
	h, err := r.open(c)
	if err != nil {
		return nil, err
	}
	h.fingerprint = fp
	r.handles[c.ID] = h
	return h, nil
}

func (r *Registry) open(c model.Connection) (*handle, error) {
	switch c.Type {
	case model.MongoDB:
		client, err := mongo.Connect(options.Client().ApplyURI(c.DSN).
			SetMaxPoolSize(10).
			SetServerSelectionTimeout(10 * time.Second).
			SetAppName("comporta"))
		if err != nil {
			return nil, err
		}
		return &handle{mongo: client}, nil
	case model.Redis:
		opts, err := redis.ParseURL(c.DSN)
		if err != nil {
			return nil, err
		}
		opts.PoolSize = 5
		opts.ClientName = "comporta"
		return &handle{redis: redis.NewClient(opts)}, nil
	}
	driver, dsn, err := r.sqlDSN(c)
	if err != nil {
		return nil, err
	}
	pool, err := sql.Open(driver, dsn)
	if err != nil {
		return nil, err
	}
	pool.SetMaxOpenConns(5)
	pool.SetMaxIdleConns(2)
	pool.SetConnMaxIdleTime(5 * time.Minute)
	pool.SetConnMaxLifetime(30 * time.Minute)
	return &handle{sql: pool}, nil
}

func (r *Registry) SQL(c model.Connection) (*sql.DB, error) {
	if !c.Type.IsSQL() {
		return nil, fmt.Errorf("connection %q is %s, not SQL", c.Name, c.Type)
	}
	h, err := r.get(c)
	if err != nil {
		return nil, err
	}
	return h.sql, nil
}

func (r *Registry) Mongo(c model.Connection) (*mongo.Client, error) {
	if c.Type != model.MongoDB {
		return nil, fmt.Errorf("connection %q is %s, not MongoDB", c.Name, c.Type)
	}
	h, err := r.get(c)
	if err != nil {
		return nil, err
	}
	return h.mongo, nil
}

func (r *Registry) Redis(c model.Connection) (*redis.Client, error) {
	if c.Type != model.Redis {
		return nil, fmt.Errorf("connection %q is %s, not Redis", c.Name, c.Type)
	}
	h, err := r.get(c)
	if err != nil {
		return nil, err
	}
	return h.redis, nil
}

// Forget closes the pool of a connection that was edited or deleted.
func (r *Registry) Forget(id int64) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if h, ok := r.handles[id]; ok {
		h.close()
		delete(r.handles, id)
	}
}

func (r *Registry) Close() {
	r.mu.Lock()
	defer r.mu.Unlock()
	for id, h := range r.handles {
		h.close()
		delete(r.handles, id)
	}
}

// Test opens a throwaway pool and pings it, without touching the registry.
func (r *Registry) Test(ctx context.Context, c model.Connection) error {
	if err := r.Validate(c); err != nil {
		return err
	}
	h, err := r.open(c)
	if err != nil {
		return err
	}
	defer h.close()
	switch {
	case h.mongo != nil:
		return h.mongo.Ping(ctx, nil)
	case h.redis != nil:
		return h.redis.Ping(ctx).Err()
	}
	return h.sql.PingContext(ctx)
}

// Validate checks that the DSN can be parsed for its type.
func (r *Registry) Validate(c model.Connection) error {
	switch c.Type {
	case model.MongoDB:
		_, err := connstring.ParseAndValidate(c.DSN)
		return err
	case model.Redis:
		_, err := redis.ParseURL(c.DSN)
		return err
	}
	_, _, err := r.sqlDSN(c)
	return err
}

// MongoDatabase is the database used when a tool call does not name one.
func MongoDatabase(c model.Connection) string {
	if c.Database != "" {
		return c.Database
	}
	if cs, err := connstring.ParseAndValidate(c.DSN); err == nil {
		return cs.Database
	}
	return ""
}

func (r *Registry) sqlDSN(c model.Connection) (driver, dsn string, err error) {
	switch c.Type {
	case model.Postgres:
		return "pgx", c.DSN, nil
	case model.SQLServer:
		return "sqlserver", c.DSN, nil
	case model.Oracle:
		if _, err := goora.ParseConfig(c.DSN); err != nil {
			return "", "", err
		}
		return "oracle", c.DSN, nil
	case model.ClickHouse:
		if _, err := clickhouse.ParseDSN(c.DSN); err != nil {
			return "", "", err
		}
		return "clickhouse", c.DSN, nil
	case model.MySQL:
		dsn := c.DSN
		if strings.HasPrefix(dsn, "mysql://") {
			if dsn, err = mysqlURLToDSN(dsn); err != nil {
				return "", "", err
			}
		}
		cfg, err := mysql.ParseDSN(dsn)
		if err != nil {
			return "", "", err
		}
		// One statement per call and no LOAD DATA LOCAL, whatever the DSN says.
		cfg.MultiStatements = false
		cfg.AllowAllFiles = false
		cfg.ParseTime = true
		return "mysql", cfg.FormatDSN(), nil
	case model.SQLite:
		path := strings.TrimPrefix(strings.TrimPrefix(c.DSN, "sqlite://"), "file:")
		path, query, _ := strings.Cut(path, "?")
		if r.isOwnStore(path) {
			return "", "", errors.New("the gateway's own audit database cannot be used as a connection")
		}
		dsn := "file:" + path + "?_pragma=busy_timeout(5000)"
		if query != "" {
			dsn += "&" + query
		}
		return "sqlite", dsn, nil
	}
	return "", "", fmt.Errorf("unsupported database type %q", c.Type)
}

// isOwnStore reports whether path is the gateway's store or one of its WAL
// files, also when reached through a symlinked directory.
func (r *Registry) isOwnStore(path string) bool {
	own := resolve(r.storePath)
	return own != "" && strings.HasPrefix(resolve(path), own)
}

// resolve makes path absolute and resolves symlinks in its directory; the
// file itself may not exist yet.
func resolve(path string) string {
	abs, err := filepath.Abs(path)
	if err != nil {
		return ""
	}
	if dir, err := filepath.EvalSymlinks(filepath.Dir(abs)); err == nil {
		abs = filepath.Join(dir, filepath.Base(abs))
	}
	return abs
}

func mysqlURLToDSN(raw string) (string, error) {
	u, err := url.Parse(raw)
	if err != nil {
		return "", err
	}
	host := u.Host
	if u.Port() == "" {
		host += ":3306"
	}
	var cred string
	if u.User != nil {
		pass, _ := u.User.Password()
		cred = u.User.Username() + ":" + pass + "@"
	}
	dsn := cred + "tcp(" + host + ")/" + strings.TrimPrefix(u.Path, "/")
	if u.RawQuery != "" {
		dsn += "?" + u.RawQuery
	}
	return dsn, nil
}

var (
	urlPassword = regexp.MustCompile(`^([a-zA-Z][a-zA-Z0-9+.-]*://[^:/@]*:)(.*)(@[^@]*)$`)
	kvPassword  = regexp.MustCompile(`(?i)\b(password|pwd)(\s*=\s*)("[^"]*"|'[^']*'|\{[^}]*\}|[^;&\s]*)`)
	myPassword  = regexp.MustCompile(`^([^:@/]+:)(.*)(@(tcp|unix)\()`)
)

// MaskDSN hides passwords so connection strings can be shown in the UI.
func MaskDSN(dsn string) string {
	if m := urlPassword.FindStringSubmatch(dsn); m != nil {
		return m[1] + "****" + m[3]
	}
	if m := myPassword.FindStringSubmatch(dsn); m != nil {
		return m[1] + "****" + dsn[len(m[1])+len(m[2]):]
	}
	return kvPassword.ReplaceAllString(dsn, "${1}${2}****")
}
