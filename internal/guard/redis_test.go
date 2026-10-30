package guard

import (
	"strings"
	"testing"
)

func TestAnalyzeRedis(t *testing.T) {
	cases := map[string]string{
		"get":      "",
		"HGETALL":  "",
		"scan":     "",
		"JSON.GET": "",
		"SADD":     "insert",
		"INCR":     "update",
		"SET":      "insert+update",
		"hset":     "insert+update",
		"DEL":      "delete",
		"LPOP":     "delete",
		"FLUSHALL": "ddl",
		"CONFIG":   "ddl",
		"EVAL":     "ddl",
		"WHATEVER": "ddl",
	}
	for cmd, want := range cases {
		a, err := AnalyzeRedis(cmd)
		if err != nil {
			t.Errorf("%s: %v", cmd, err)
			continue
		}
		got := a.Operation()
		if a.ReadOnly() {
			got = ""
		}
		if got != want {
			t.Errorf("%s: got %q, want %q", cmd, got, want)
		}
	}
	for cmd, wantMsg := range map[string]string{
		"KEYS": "use SCAN", "select": "connection string", "MULTI": "one command", "BLPOP": "blocking",
		"SUBSCRIBE": "stream", "": "empty", "GET key": "only the command name",
	} {
		if _, err := AnalyzeRedis(cmd); err == nil || !strings.Contains(err.Error(), wantMsg) {
			t.Errorf("%q: got %v, want an error about %q", cmd, err, wantMsg)
		}
	}
}
