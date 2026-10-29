package web

import (
	"math"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// The color tokens of app.css, checked in both themes: text needs 4.5:1
// against what it sits on (WCAG 1.4.3); borders, icons, switches and the
// focus ring need 3:1 (WCAG 1.4.11).
var contrastPairs = []struct {
	fg, bg string
	min    float64
}{
	{"text", "bg", 4.5}, {"text", "surface", 4.5}, {"text", "surface-2", 4.5},
	{"muted", "bg", 4.5}, {"muted", "surface", 4.5}, {"muted", "surface-2", 4.5},
	{"on-accent", "accent", 4.5}, {"on-accent", "accent-hover", 4.5},
	{"accent-soft-text", "accent-soft", 4.5}, {"accent-soft-text", "surface", 4.5},
	{"ok", "ok-soft", 4.5}, {"ok", "surface", 4.5}, {"ok", "surface-2", 4.5},
	{"warn", "warn-soft", 4.5}, {"warn", "surface", 4.5},
	{"danger", "danger-soft", 4.5}, {"danger", "surface", 4.5},
	{"on-danger", "danger", 4.5}, {"on-danger", "danger-hover", 4.5},
	{"muted", "accent-soft", 4.5}, {"text", "accent-soft", 4.5},
	{"border-strong", "surface", 3}, {"border-strong", "bg", 3}, {"border-strong", "surface-2", 3},
	{"focus", "surface", 3}, {"focus", "bg", 3},
	{"accent", "surface", 3}, {"danger", "surface-2", 3},
}

var (
	tokenRe = regexp.MustCompile(`--([a-z0-9-]+):\s*(#[0-9a-fA-F]{6})\s*;`)
	darkRe  = regexp.MustCompile(`(?s)@media \(prefers-color-scheme: dark\)\s*\{\s*:root\s*\{(.*?)\}`)
)

func tokens(block string) map[string]string {
	out := map[string]string{}
	for _, m := range tokenRe.FindAllStringSubmatch(block, -1) {
		out[m[1]] = m[2]
	}
	return out
}

func luminance(hex string) float64 {
	channel := func(s string) float64 {
		v, _ := strconv.ParseUint(s, 16, 8)
		c := float64(v) / 255
		if c <= 0.03928 {
			return c / 12.92
		}
		return math.Pow((c+0.055)/1.055, 2.4)
	}
	h := strings.TrimPrefix(hex, "#")
	return 0.2126*channel(h[0:2]) + 0.7152*channel(h[2:4]) + 0.0722*channel(h[4:6])
}

func ratio(a, b string) float64 {
	la, lb := luminance(a), luminance(b)
	if la < lb {
		la, lb = lb, la
	}
	return (la + 0.05) / (lb + 0.05)
}

func TestColorContrast(t *testing.T) {
	css, err := static.ReadFile("static/app.css")
	if err != nil {
		t.Fatal(err)
	}
	dark := darkRe.FindSubmatch(css)
	if dark == nil {
		t.Fatal("dark theme block not found in app.css")
	}
	light := tokens(string(css[:darkRe.FindIndex(css)[0]]))
	themes := map[string]map[string]string{"light": light, "dark": tokens(string(dark[1]))}
	for name, tk := range themes {
		for _, p := range contrastPairs {
			fg, bg := tk[p.fg], tk[p.bg]
			if fg == "" || bg == "" {
				t.Errorf("%s: token --%s or --%s missing", name, p.fg, p.bg)
				continue
			}
			if r := ratio(fg, bg); r < p.min {
				t.Errorf("%s: --%s %s on --%s %s is %.2f:1, needs %.1f:1", name, p.fg, fg, p.bg, bg, r, p.min)
			}
		}
	}
}

func TestRatio(t *testing.T) {
	if r := ratio("#000000", "#ffffff"); math.Abs(r-21) > 0.01 {
		t.Fatalf("black on white = %.2f, want 21", r)
	}
}
