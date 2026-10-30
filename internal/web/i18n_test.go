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
	jsKeyRe  = regexp.MustCompile(`\bt\(\s*"([a-zA-Z0-9_.]+)"`)
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

// Both languages have the same keys, and every key the page, the script or
// the server's error codes use exists.
func TestTranslationsComplete(t *testing.T) {
	read := func(name string) string {
		b, err := static.ReadFile("static/" + name)
		if err != nil {
			t.Fatal(err)
		}
		return string(b)
	}
	js := read("i18n.js")
	en, pt := dictKeys(t, js, "en"), dictKeys(t, js, "pt")
	for _, k := range en {
		if !slices.Contains(pt, k) {
			t.Errorf("pt is missing %q", k)
		}
	}
	for _, k := range pt {
		if !slices.Contains(en, k) {
			t.Errorf("en is missing %q", k)
		}
	}

	var used []string
	html := read("index.html")
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
	for _, m := range jsKeyRe.FindAllStringSubmatch(read("app.js"), -1) {
		used = append(used, m[1])
	}
	src, err := webSource()
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range srvKeyRe.FindAllStringSubmatch(src, -1) {
		used = append(used, "srv."+m[1])
	}
	for _, k := range used {
		if !slices.Contains(en, k) {
			t.Errorf("key %q is used but not translated", k)
		}
	}
}

// webSource is web.go, where the server's validation codes are written.
func webSource() (string, error) {
	b, err := os.ReadFile("web.go")
	return string(b), err
}
