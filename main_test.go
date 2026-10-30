package main

import (
	"context"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestLoadConfig(t *testing.T) {
	t.Setenv("COMPORTA_ADMIN_PASSWORD", "x")
	t.Setenv("COMPORTA_MCP_TOKENS", "claude-code:0123456789abcdef, copilot:fedcba9876543210")
	t.Setenv("DBMCP_LOG_RETENTION_DAYS", "7") // the former prefix still works
	cfg, err := loadConfig()
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.tokens) != 2 || cfg.retentionDays != 7 || cfg.maxRowsCap != 1000 || cfg.addr != ":8080" {
		t.Fatalf("config: %+v", cfg)
	}
}

func TestLoadConfigErrors(t *testing.T) {
	cases := []map[string]string{
		{"COMPORTA_MCP_TOKEN": "0123456789abcdef"},
		{"COMPORTA_ADMIN_PASSWORD": "x"},
		{"COMPORTA_ADMIN_PASSWORD": "x", "COMPORTA_MCP_TOKENS": "curto:123"},
		{"COMPORTA_ADMIN_PASSWORD": "x", "COMPORTA_MCP_TOKEN": "0123456789abcdef", "COMPORTA_MAX_ROWS_CAP": "-1"},
		{"COMPORTA_ADMIN_PASSWORD": "x", "COMPORTA_MCP_TOKEN": "0123456789abcdef", "COMPORTA_LOG_RETENTION_DAYS": "abc"},
	}
	for _, vars := range cases {
		t.Run("", func(t *testing.T) {
			for _, k := range []string{"ADMIN_PASSWORD", "MCP_TOKEN", "MCP_TOKENS", "MAX_ROWS_CAP", "LOG_RETENTION_DAYS"} {
				t.Setenv(envPrefix+k, "")
				t.Setenv(legacyPrefix+k, "")
			}
			for k, v := range vars {
				t.Setenv(k, v)
			}
			if _, err := loadConfig(); err == nil {
				t.Fatalf("%v: expected an error", vars)
			}
		})
	}
}

func TestHealthcheckFailsWithoutServer(t *testing.T) {
	t.Setenv("COMPORTA_ADDR", "127.0.0.1:1")
	if healthcheck() == 0 {
		t.Fatal("healthcheck must fail when nothing listens")
	}
}

// run starts the whole server, answers on /healthz, the admin UI and /mcp,
// and returns cleanly when its context is cancelled.
func TestRunServesAndStops(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	_ = ln.Close()
	t.Setenv("COMPORTA_ADDR", addr)
	t.Setenv("COMPORTA_DATA_DIR", t.TempDir())
	t.Setenv("COMPORTA_ADMIN_PASSWORD", "x")
	t.Setenv("COMPORTA_MCP_TOKEN", "0123456789abcdef")
	t.Setenv("COMPORTA_SECRET_KEY", "frase de teste")

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- run(ctx) }()

	base := "http://" + addr
	deadline := time.Now().Add(10 * time.Second)
	for healthcheck() != 0 {
		if time.Now().After(deadline) {
			t.Fatal("server did not become healthy")
		}
		time.Sleep(50 * time.Millisecond)
	}
	for _, tc := range []struct {
		path, user string
		want       int
	}{{"/api/meta", "admin", http.StatusOK}, {"/api/meta", "", http.StatusUnauthorized}, {"/mcp", "", http.StatusUnauthorized}} {
		req, _ := http.NewRequestWithContext(ctx, http.MethodGet, base+tc.path, nil)
		if tc.user != "" {
			req.SetBasicAuth(tc.user, "x")
		}
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		_ = res.Body.Close()
		if res.StatusCode != tc.want {
			t.Errorf("%s as %q: %d, want %d", tc.path, tc.user, res.StatusCode, tc.want)
		}
	}

	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("run returned %v", err)
		}
	case <-time.After(15 * time.Second):
		t.Fatal("run did not stop after cancel")
	}
}

func TestRunRejectsBadConfig(t *testing.T) {
	t.Setenv("COMPORTA_ADMIN_PASSWORD", "")
	t.Setenv("DBMCP_ADMIN_PASSWORD", "")
	if err := run(context.Background()); err == nil {
		t.Fatal("run must refuse to start without an admin password")
	}
}

func TestRunStopsOnUnwritableDataDir(t *testing.T) {
	if os.Getuid() == 0 {
		t.Skip("root writes anywhere")
	}
	ro := filepath.Join(t.TempDir(), "ro")
	if err := os.Mkdir(ro, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(ro, 0o700) })
	t.Setenv("COMPORTA_ADMIN_PASSWORD", "x")
	t.Setenv("COMPORTA_MCP_TOKEN", "0123456789abcdef")
	t.Setenv("COMPORTA_DATA_DIR", ro)
	err := run(context.Background())
	if err == nil || !strings.Contains(err.Error(), "chown") {
		t.Fatalf("got %v, want the chown hint", err)
	}
}
