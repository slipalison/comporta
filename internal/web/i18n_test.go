package web

import (
	"os"
	"regexp"
	"slices"
	"strings"
	"testing"
)

var (
	dictRe   = regexp.MustCompile(`(?s)\nconst (en|pt) = \{(.*?)\n\};`)
	keyRe    = regexp.MustCompile(`(?m)^  "([a-zA-Z0-9_.]+)":`)
	jsKeyRe  = regexp.MustCompile(`\bt\(\s*"([a-zA-Z0-9_.]+)"\s*[,)]`) // whole keys; built keys are listed in usedKeys
	attrRe   = regexp.MustCompile(`data-i18n-attr="([^"]+)"`)
	srvKeyRe = regexp.MustCompile(`Code: "([a-z_]+)"`)
)

func dictKeys(t *testing.T, js, lang string) []string {
	t.Helper()
	for _, m := range dictRe.FindAllStringSubmatch(js, -1) {
		if m[1] == lang {
			var keys []string
			for _, k := range keyRe.FindAllStringSubmatch(m[2], -1) {
				keys = append(keys, k[1])
			}
			return keys
		}
	}
	t.Fatalf("dictionary %s not found", lang)
	return nil
}

func readStatic(t *testing.T, name string) string {
	t.Helper()
	b, err := static.ReadFile("static/" + name)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// missing returns the keys of want that are not in have.
func missing(want, have []string) []string {
	var out []string
	for _, k := range want {
		if !slices.Contains(have, k) {
			out = append(out, k)
		}
	}
	return out
}

// usedKeys collects every key the page, the script and the server's error
// codes refer to.
func usedKeys(t *testing.T) []string {
	html := readStatic(t, "index.html")
	var used []string
	for _, m := range regexp.MustCompile(`data-i18n(?:-html)?="([^"]+)"`).FindAllStringSubmatch(html, -1) {
		used = append(used, m[1])
	}
	for _, m := range attrRe.FindAllStringSubmatch(html, -1) {
		for _, pair := range strings.Split(m[1], ";") {
			if _, key, ok := strings.Cut(pair, ":"); ok {
				used = append(used, strings.TrimSpace(key))
			}
		}
	}
	for _, m := range jsKeyRe.FindAllStringSubmatch(readStatic(t, "app.js"), -1) {
		used = append(used, m[1])
	}
	src, err := os.ReadFile("web.go")
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range srvKeyRe.FindAllStringSubmatch(string(src), -1) {
		used = append(used, "srv."+m[1])
	}
	// Keys app.js builds from data: t("op." + key), t("risk." + key), t(`status.${status}`).
	used = append(used, "op.insert", "op.update", "op.delete", "op.ddl", "risk.delete", "risk.ddl",
		"status.ok", "status.denied", "status.error")
	return used
}

// Both languages have the same keys, and every key the page, the script or
// the server's error codes use exists.
func TestTranslationsComplete(t *testing.T) {
	js := readStatic(t, "i18n.js")
	en, pt := dictKeys(t, js, "en"), dictKeys(t, js, "pt")
	failIf(t, len(en) < 100, "only %d English keys found: is the dictionary regexp still right?", len(en))
	failIf(t, len(missing(en, pt)) > 0, "pt is missing %v", missing(en, pt))
	failIf(t, len(missing(pt, en)) > 0, "en is missing %v", missing(pt, en))
	failIf(t, len(missing(usedKeys(t), en)) > 0, "used but not translated: %v", missing(usedKeys(t), en))
}
