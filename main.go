// Command dbmcp is an MCP gateway to PostgreSQL, SQL Server, MySQL, SQLite
// and MongoDB with per-connection permissions, an admin UI and an audit log.
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"

	"dbmcp/internal/db"
	"dbmcp/internal/mcpsrv"
	"dbmcp/internal/store"
	"dbmcp/internal/web"
)

var version = "dev"

type config struct {
	addr          string
	dataDir       string
	adminUser     string
	adminPassword string
	tokens        map[string]string
	secretKey     string
	retentionDays int
	maxRowsCap    int
}

func env(key, def string) string {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		return v
	}
	return def
}

func envInt(key string, def int) (int, error) {
	v := env(key, "")
	if v == "" {
		return def, nil
	}
	n, err := strconv.Atoi(v)
	if err != nil || n <= 0 {
		return 0, fmt.Errorf("%s must be a positive integer", key)
	}
	return n, nil
}

// parseTokens reads DBMCP_MCP_TOKENS ("label:token,label2:token2") and the
// single-token shortcut DBMCP_MCP_TOKEN.
func parseTokens() (map[string]string, error) {
	tokens := map[string]string{}
	if t := env("DBMCP_MCP_TOKEN", ""); t != "" {
		tokens["default"] = t
	}
	for _, pair := range strings.Split(env("DBMCP_MCP_TOKENS", ""), ",") {
		if pair = strings.TrimSpace(pair); pair == "" {
			continue
		}
		label, token, ok := strings.Cut(pair, ":")
		if !ok || label == "" || len(token) < 16 {
			return nil, fmt.Errorf("DBMCP_MCP_TOKENS entry %q must be label:token with a token of at least 16 characters", label)
		}
		tokens[label] = token
	}
	if len(tokens) == 0 {
		return nil, errors.New("set DBMCP_MCP_TOKENS (label:token,...) or DBMCP_MCP_TOKEN so MCP clients must authenticate")
	}
	return tokens, nil
}

func loadConfig() (config, error) {
	c := config{
		addr:          env("DBMCP_ADDR", ":8080"),
		dataDir:       env("DBMCP_DATA_DIR", "./data"),
		adminUser:     env("DBMCP_ADMIN_USER", "admin"),
		adminPassword: env("DBMCP_ADMIN_PASSWORD", ""),
		secretKey:     os.Getenv("DBMCP_SECRET_KEY"),
	}
	var err error
	if c.adminPassword == "" {
		return c, errors.New("DBMCP_ADMIN_PASSWORD is required: the admin UI controls what the LLM may do")
	}
	if c.tokens, err = parseTokens(); err != nil {
		return c, err
	}
	if c.retentionDays, err = envInt("DBMCP_LOG_RETENTION_DAYS", 30); err != nil {
		return c, err
	}
	if c.maxRowsCap, err = envInt("DBMCP_MAX_ROWS_CAP", 1000); err != nil {
		return c, err
	}
	return c, nil
}

// healthcheck lets the distroless image (no shell, no curl) report health.
func healthcheck() int {
	_, port, err := net.SplitHostPort(env("DBMCP_ADDR", ":8080"))
	if err != nil {
		return 1
	}
	client := http.Client{Timeout: 3 * time.Second}
	res, err := client.Get("http://127.0.0.1:" + port + "/healthz")
	if err != nil || res.StatusCode != http.StatusOK {
		return 1
	}
	return 0
}

func main() {
	if len(os.Args) > 1 && os.Args[1] == "healthcheck" {
		os.Exit(healthcheck())
	}
	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stdout, nil)))
	if err := run(); err != nil {
		slog.Error("fatal", "err", err)
		os.Exit(1)
	}
}

func run() error {
	cfg, err := loadConfig()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(cfg.dataDir, 0o700); err != nil {
		return err
	}
	if cfg.secretKey == "" {
		slog.Warn("DBMCP_SECRET_KEY not set: using a generated key in the data directory; connection strings are only as safe as that volume")
	}
	key, err := store.LoadKey(cfg.secretKey, cfg.dataDir)
	if err != nil {
		return err
	}
	cipher, err := store.NewCipher(key)
	if err != nil {
		return err
	}
	st, err := store.Open(filepath.Join(cfg.dataDir, "dbmcp.db"), cipher)
	if err != nil {
		return err
	}
	defer st.Close()

	reg := db.NewRegistry(st.Path)
	defer reg.Close()

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	mcpServer := mcpsrv.New(st, reg, version, cfg.maxRowsCap)
	if err := mcpServer.Refresh(ctx); err != nil {
		return fmt.Errorf("loading connections: %w", err)
	}

	labels := make([]string, 0, len(cfg.tokens))
	for l := range cfg.tokens {
		labels = append(labels, l)
	}
	sort.Strings(labels)
	admin := web.New(st, reg, cfg.adminUser, cfg.adminPassword,
		web.Meta{Version: version, RetentionDays: cfg.retentionDays, MaxRowsCap: cfg.maxRowsCap, Clients: labels},
		func() {
			if err := mcpServer.Refresh(context.Background()); err != nil {
				slog.Error("refreshing mcp tools", "err", err)
			}
		})

	mux := http.NewServeMux()
	mux.Handle("/mcp", mcpServer.Handler(cfg.tokens))
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) { w.Write([]byte("ok")) })
	mux.Handle("/", admin.Handler())

	go purgeLoop(ctx, st, cfg.retentionDays)

	srv := &http.Server{Addr: cfg.addr, Handler: mux, ReadHeaderTimeout: 10 * time.Second}
	errCh := make(chan error, 1)
	go func() { errCh <- srv.ListenAndServe() }()
	slog.Info("dbmcp listening", "addr", cfg.addr, "version", version, "mcp_clients", labels, "retention_days", cfg.retentionDays)

	select {
	case err := <-errCh:
		return err
	case <-ctx.Done():
	}
	shutdown, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	return srv.Shutdown(shutdown)
}

func purgeLoop(ctx context.Context, st *store.Store, days int) {
	purge := func() {
		n, err := st.PurgeAudit(ctx, time.Now().AddDate(0, 0, -days))
		if err != nil {
			slog.Error("audit purge failed", "err", err)
		} else if n > 0 {
			slog.Info("audit purged", "removed", n, "retention_days", days)
		}
	}
	purge()
	t := time.NewTicker(time.Hour)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			purge()
		}
	}
}
