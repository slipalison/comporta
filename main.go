// Command comporta is an MCP gateway to PostgreSQL, SQL Server, MySQL, SQLite
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
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/slipalison/comporta/internal/db"
	"github.com/slipalison/comporta/internal/mcpsrv"
	"github.com/slipalison/comporta/internal/store"
	"github.com/slipalison/comporta/internal/web"
)

var version = "dev"

type config struct {
	addr          string
	dataDir       string
	adminUser     string
	adminPassword string
	tokens        map[string]string
	secretKey     string
	legacyKey     string
	retentionDays int
	maxRowsCap    int
}

// envPrefix is the current prefix; legacyPrefix is read as a fallback so a
// deployment of the former db-mcp keeps working after the rename.
const (
	envPrefix    = "COMPORTA_"
	legacyPrefix = "DBMCP_"
)

// env reads COMPORTA_<name>, falling back to DBMCP_<name>.
func env(name, def string) string {
	if v := strings.TrimSpace(os.Getenv(envPrefix + name)); v != "" {
		return v
	}
	if v := strings.TrimSpace(os.Getenv(legacyPrefix + name)); v != "" {
		slog.Warn("deprecated environment variable, rename it", "old", legacyPrefix+name, "new", envPrefix+name)
		return v
	}
	return def
}

func envInt(name string, def int) (int, error) {
	v := env(name, "")
	if v == "" {
		return def, nil
	}
	n, err := strconv.Atoi(v)
	if err != nil || n <= 0 {
		return 0, fmt.Errorf("%s%s must be a positive integer", envPrefix, name)
	}
	return n, nil
}

// parseTokens reads COMPORTA_MCP_TOKENS ("label:token,label2:token2") and the
// single-token shortcut COMPORTA_MCP_TOKEN.
func parseTokens() (map[string]string, error) {
	tokens := map[string]string{}
	if t := env("MCP_TOKEN", ""); t != "" {
		tokens["default"] = t
	}
	for _, pair := range strings.Split(env("MCP_TOKENS", ""), ",") {
		if pair = strings.TrimSpace(pair); pair == "" {
			continue
		}
		label, token, ok := strings.Cut(pair, ":")
		if !ok || label == "" || len(token) < 16 {
			return nil, fmt.Errorf("COMPORTA_MCP_TOKENS entry %q must be label:token with a token of at least 16 characters", label)
		}
		tokens[label] = token
	}
	if len(tokens) == 0 {
		return nil, errors.New("set COMPORTA_MCP_TOKENS (label:token,...) or COMPORTA_MCP_TOKEN so MCP clients must authenticate")
	}
	return tokens, nil
}

func loadConfig() (config, error) {
	c := config{
		addr:          env("ADDR", ":8080"),
		dataDir:       env("DATA_DIR", "./data"),
		adminUser:     env("ADMIN_USER", "admin"),
		adminPassword: env("ADMIN_PASSWORD", ""),
		secretKey:     env("SECRET_KEY", ""),
		legacyKey:     env("LEGACY_KEY", ""),
	}
	var err error
	if c.adminPassword == "" {
		return c, errors.New("COMPORTA_ADMIN_PASSWORD is required: the admin UI controls what the LLM may do")
	}
	if c.tokens, err = parseTokens(); err != nil {
		return c, err
	}
	if c.retentionDays, err = envInt("LOG_RETENTION_DAYS", 30); err != nil {
		return c, err
	}
	if c.maxRowsCap, err = envInt("MAX_ROWS_CAP", 1000); err != nil {
		return c, err
	}
	return c, nil
}

// healthcheck lets the distroless image (no shell, no curl) report health.
func healthcheck() int {
	_, port, err := net.SplitHostPort(env("ADDR", ":8080"))
	if err != nil {
		return 1
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://127.0.0.1:"+port+"/healthz", nil)
	if err != nil {
		return 1
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		return 1
	}
	defer func() { _ = res.Body.Close() }()
	if res.StatusCode != http.StatusOK {
		return 1
	}
	return 0
}

func main() {
	if len(os.Args) > 1 && os.Args[1] == "healthcheck" {
		os.Exit(healthcheck())
	}
	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stdout, nil)))
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	err := run(ctx)
	stop()
	if err != nil {
		slog.Error("fatal", "err", err)
		os.Exit(1)
	}
}

// run serves until ctx is cancelled, then shuts down gracefully.
func run(ctx context.Context) error {
	cfg, err := loadConfig()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(cfg.dataDir, 0o700); err != nil {
		return err
	}
	if err := store.CheckWritable(cfg.dataDir); err != nil {
		return err
	}
	storage := store.DetectStorage(cfg.dataDir)
	if !storage.Persistent {
		slog.Warn("data directory is not a volume: connections and the audit log are lost when this container is removed or recreated for a new image; "+
			"mount one to keep them, e.g. -v comporta-data:/data", "path", storage.Path)
	}
	if cfg.secretKey == "" {
		slog.Warn("COMPORTA_SECRET_KEY not set: using a generated key in the data directory; connection strings are only as safe as that volume")
	}
	keys, err := store.LoadKeys(cfg.secretKey, cfg.legacyKey, cfg.dataDir)
	if err != nil {
		return err
	}
	cipher, err := store.NewCipherKeys(keys)
	if err != nil {
		return err
	}
	st, err := store.Open(store.DBPath(cfg.dataDir), cipher)
	if err != nil {
		return err
	}
	defer func() { _ = st.Close() }()
	if m := st.Migration; m.From != m.To {
		slog.Info("database schema migrated", "path", st.Path, "from", m.From, "to", m.To, "backup", m.Backup)
	}

	reg := db.NewRegistry(st.Path)
	defer reg.Close()

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
		web.Meta{Version: version, RetentionDays: cfg.retentionDays, MaxRowsCap: cfg.maxRowsCap, Clients: labels, Storage: storage},
		func() {
			if err := mcpServer.Refresh(context.Background()); err != nil {
				slog.Error("refreshing mcp tools", "err", err)
			}
		})

	mux := http.NewServeMux()
	mux.Handle("/mcp", mcpServer.Handler(cfg.tokens))
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("ok")) })
	mux.Handle("/", admin.Handler())

	go purgeLoop(ctx, st, cfg.retentionDays)

	srv := &http.Server{Addr: cfg.addr, Handler: mux, ReadHeaderTimeout: 10 * time.Second, IdleTimeout: 2 * time.Minute}
	errCh := make(chan error, 1)
	go func() { errCh <- srv.ListenAndServe() }()
	slog.Info("comporta listening", "addr", cfg.addr, "version", version, "mcp_clients", labels, "retention_days", cfg.retentionDays)

	select {
	case err := <-errCh:
		return err
	case <-ctx.Done():
	}
	// ctx is already cancelled here: the shutdown gets its own deadline.
	shutdown, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
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
