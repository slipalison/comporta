package main

import (
	"testing"
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
