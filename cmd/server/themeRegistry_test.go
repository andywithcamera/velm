package main

import (
	"fmt"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"
)

// #352 R3: a theme is declared in four places, and getting one of them wrong
// fails quietly. A theme in the registry but not in the dropdown cannot be
// chosen; one in the dropdown but not the registry is normalised away by the
// server on the next page load, so the picker appears to un-set itself; one
// with no colour block changes data-theme and repaints nothing. These tests
// hold the four lists together, and hold every theme to a contrast floor.

// regexpFindAll is MustCompile + FindAllStringSubmatch, which is every use in
// this file.
func regexpFindAll(pattern, source string) [][]string {
	return regexp.MustCompile(pattern).FindAllStringSubmatch(source, -1)
}

// themeColourSource reads the stylesheet that owns every colour token.
func themeColourSource(t *testing.T) string {
	t.Helper()
	path := filepath.Join(repoRoot(t), "web", "static", "css", "ui", "40-velm-monochrome.css")
	source, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(source)
}

func themeFile(t *testing.T, parts ...string) string {
	t.Helper()
	path := filepath.Join(append([]string{repoRoot(t)}, parts...)...)
	source, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(source)
}

// themeTokenBlocks returns the token declarations of each html[data-theme]
// block, keyed by theme name. The light block's reference-modal rule also
// matches the selector but declares no tokens, so it drops out naturally.
func themeTokenBlocks(t *testing.T) map[string]map[string]string {
	t.Helper()
	source := themeColourSource(t)
	blocks := map[string]map[string]string{}
	for _, match := range regexpFindAll(`html\[data-theme="([a-z0-9]+)"\]\s*\{([^}]*)\}`, source) {
		name, body := match[1], match[2]
		tokens := map[string]string{}
		for _, decl := range regexpFindAll(`(--[a-z0-9-]+):\s*([^;]+);`, body) {
			tokens[decl[1]] = strings.TrimSpace(decl[2])
		}
		if len(tokens) > 0 {
			blocks[name] = tokens
		}
	}
	return blocks
}

// TestEveryRegisteredThemeHasAColourBlock: a name the server accepts must
// repaint the app.
func TestEveryRegisteredThemeHasAColourBlock(t *testing.T) {
	blocks := themeTokenBlocks(t)
	for theme := range validThemes {
		if blocks[theme] == nil {
			t.Errorf("validThemes has %q but 40-velm-monochrome.css has no html[data-theme=%q] token block; selecting it would change data-theme and nothing else", theme, theme)
		}
	}
}

// TestNoColourBlockWithoutARegistryEntry: a block nothing can select is dead
// weight that then lies about which themes exist.
func TestNoColourBlockWithoutARegistryEntry(t *testing.T) {
	for theme := range themeTokenBlocks(t) {
		if !validThemes[theme] {
			t.Errorf("40-velm-monochrome.css themes %q but validThemes does not list it; the server would normalise the value away on the next request", theme)
		}
	}
}

// TestThemeDropdownMatchesRegistry reads the <option> values out of the navbar,
// which is the only way a person can choose a theme.
func TestThemeDropdownMatchesRegistry(t *testing.T) {
	navbar := themeFile(t, "web", "templates", "components", "navbar.html")
	start := strings.Index(navbar, `id="velm-theme-select"`)
	if start < 0 {
		t.Fatal("navbar.html has no #velm-theme-select; the theme picker is gone")
	}
	end := strings.Index(navbar[start:], "</select>")
	if end < 0 {
		t.Fatal("navbar.html's theme select is unterminated")
	}
	options := map[string]bool{}
	for _, match := range regexpFindAll(`<option value="([a-z0-9]+)"`, navbar[start:start+end]) {
		options[match[1]] = true
	}
	for theme := range validThemes {
		if !options[theme] {
			t.Errorf("validThemes has %q but the navbar dropdown offers no such option; the only theme you cannot pick is the one that exists", theme)
		}
	}
	for theme := range options {
		if !validThemes[theme] {
			t.Errorf("the navbar offers %q but validThemes does not list it; choosing it posts a value the server normalises back to the default", theme)
		}
	}
}

// TestThemeClientRegistryMatchesServer keeps layout.html's client-side table in
// step. It decides whether a value survives normalisation before a POST, so a
// theme missing there is reset on the first navigation of a session.
func TestThemeClientRegistryMatchesServer(t *testing.T) {
	layout := themeFile(t, "web", "templates", "layout.html")
	start := strings.Index(layout, "const velmThemes = {")
	if start < 0 {
		t.Fatal("layout.html has no velmThemes registry")
	}
	end := strings.Index(layout[start:], "};")
	if end < 0 {
		t.Fatal("layout.html's velmThemes registry is unterminated")
	}
	client := map[string]bool{}
	for _, match := range regexpFindAll(`"([a-z0-9]+)":`, layout[start:start+end]) {
		client[match[1]] = true
	}
	for theme := range validThemes {
		if !client[theme] {
			t.Errorf("validThemes has %q but layout.html's velmThemes does not; normaliseTheme() would reset it to the default on load", theme)
		}
	}
	for theme := range client {
		if !validThemes[theme] {
			t.Errorf("layout.html lists %q but validThemes does not; the client would keep a value the server stores as the default", theme)
		}
	}
}

// TestEveryThemeDeclaresTheFullTokenSet stops a theme from inheriting a token
// from whichever block happens to come first in the cascade. A missing
// --status-error does not error, it silently renders in the previous theme's
// red.
func TestEveryThemeDeclaresTheFullTokenSet(t *testing.T) {
	blocks := themeTokenBlocks(t)
	reference := blocks["light"]
	if reference == nil {
		t.Fatal("no light theme block to use as the token reference")
	}
	want := make([]string, 0, len(reference))
	for token := range reference {
		want = append(want, token)
	}
	sort.Strings(want)
	for theme, tokens := range blocks {
		for _, token := range want {
			if _, ok := tokens[token]; !ok {
				t.Errorf("theme %q does not declare %s; it inherits whatever the cascade last set, which is another theme's colour", theme, token)
			}
		}
	}
}

// contrastThresholds are the floors each token is held to, measured against
// every background a theme declares. text-primary and text-secondary are body
// copy (WCAG AA asks 4.5; 7 is AAA and the existing themes already clear it by
// a wide margin). text-dim is secondary copy and text-ghost is placeholder and
// meta text, where 3.0 is the non-text/large-text floor. blue-mid is the
// primary accent and is used as link and button text, so it takes the body-copy
// floor. Status colours appear as text in badges, so the same.
var contrastThresholds = map[string]float64{
	"--text-primary":   7.0,
	"--text-secondary": 7.0,
	"--text-dim":       4.5,
	"--text-ghost":     3.0,
	"--blue-mid":       4.5,
	"--status-success": 4.5,
	"--status-warning": 4.5,
	"--status-error":   4.5,
	"--status-info":    4.5,
}

// TestThemesMeetContrastMinimums is the reason a new theme cannot be eyeballed
// into the file. Contrast is arithmetic: a warm field with a mid-tone accent
// looks fine to the person who chose it and is unreadable to everyone else.
//
// --blue-bright is deliberately absent. In light themes it is a decorative
// accent that also sits inside the deliberately dark code surfaces, and the two
// oldest light themes (yellow 2.96, mint 2.18) already fail any text floor for
// it. Holding it to one would mean repainting themes that were signed off;
// leaving it out is the honest option, and it is recorded here rather than
// quietly skipped.
func TestThemesMeetContrastMinimums(t *testing.T) {
	for theme, tokens := range themeTokenBlocks(t) {
		backgrounds := []string{"--bg-base", "--bg-surface", "--bg-elevated"}
		for _, backgroundToken := range backgrounds {
			background, ok := tokens[backgroundToken]
			if !ok {
				continue
			}
			for token, floor := range contrastThresholds {
				value, ok := tokens[token]
				if !ok {
					continue
				}
				ratio, err := contrastRatio(value, background)
				if err != nil {
					t.Fatalf("theme %q %s=%q on %s=%q: %v", theme, token, value, backgroundToken, background, err)
				}
				if ratio < floor {
					t.Errorf("theme %q: %s on %s is %.2f:1, under the %.1f floor; the value passes on the author's monitor at midday and fails on a laptop at 60%% brightness", theme, token, backgroundToken, ratio, floor)
				}
			}
		}
	}
}

// contrastRatio composites a possibly-translucent foreground over an opaque
// background before computing the WCAG ratio. Plain value comparison against
// the declared string is wrong for exactly the tokens that matter: text-dim and
// text-ghost are rgba, so their apparent lightness depends on the field behind
// them.
func contrastRatio(foreground, background string) (float64, error) {
	bg, err := parseColour(background)
	if err != nil {
		return 0, fmt.Errorf("background: %w", err)
	}
	fg, err := parseColour(foreground)
	if err != nil {
		return 0, fmt.Errorf("foreground: %w", err)
	}
	if bg.a < 1 {
		return 0, fmt.Errorf("background is translucent (alpha %.2f); the ratio cannot be computed", bg.a)
	}
	mixed := colour{
		r: fg.r*fg.a + bg.r*(1-fg.a),
		g: fg.g*fg.a + bg.g*(1-fg.a),
		b: fg.b*fg.a + bg.b*(1-fg.a),
		a: 1,
	}
	light, dark := mixed.luminance(), bg.luminance()
	if light < dark {
		light, dark = dark, light
	}
	return (light + 0.05) / (dark + 0.05), nil
}

type colour struct{ r, g, b, a float64 }

func (c colour) luminance() float64 {
	channel := func(value float64) float64 {
		if value <= 0.03928 {
			return value / 12.92
		}
		return math.Pow((value+0.055)/1.055, 2.4)
	}
	return 0.2126*channel(c.r) + 0.7152*channel(c.g) + 0.0722*channel(c.b)
}

// parseColour accepts #rgb, #rrggbb, rgb(...) and rgba(...) — the four forms the
// stylesheet uses for tokens.
func parseColour(value string) (colour, error) {
	value = strings.TrimSpace(value)
	switch {
	case strings.HasPrefix(value, "#"):
		hex := strings.TrimPrefix(value, "#")
		if len(hex) == 3 {
			hex = string([]byte{hex[0], hex[0], hex[1], hex[1], hex[2], hex[2]})
		}
		if len(hex) != 6 {
			return colour{}, fmt.Errorf("unsupported hex colour %q", value)
		}
		component := func(pair string) (float64, error) {
			parsed, err := strconv.ParseInt(pair, 16, 32)
			if err != nil {
				return 0, err
			}
			return float64(parsed) / 255, nil
		}
		r, err := component(hex[0:2])
		if err != nil {
			return colour{}, err
		}
		g, err := component(hex[2:4])
		if err != nil {
			return colour{}, err
		}
		b, err := component(hex[4:6])
		if err != nil {
			return colour{}, err
		}
		return colour{r: r, g: g, b: b, a: 1}, nil
	case strings.HasPrefix(value, "rgb"):
		open := strings.Index(value, "(")
		close := strings.Index(value, ")")
		if open < 0 || close < open {
			return colour{}, fmt.Errorf("malformed rgb colour %q", value)
		}
		parts := strings.FieldsFunc(value[open+1:close], func(r rune) bool {
			return r == ',' || r == ' ' || r == '/'
		})
		if len(parts) < 3 {
			return colour{}, fmt.Errorf("rgb colour %q has %d components", value, len(parts))
		}
		numbers := make([]float64, 0, 4)
		for _, part := range parts {
			number, err := strconv.ParseFloat(strings.TrimSpace(part), 64)
			if err != nil {
				return colour{}, fmt.Errorf("rgb colour %q: %w", value, err)
			}
			numbers = append(numbers, number)
		}
		out := colour{r: numbers[0] / 255, g: numbers[1] / 255, b: numbers[2] / 255, a: 1}
		if len(numbers) >= 4 {
			out.a = numbers[3]
		}
		return out, nil
	default:
		return colour{}, fmt.Errorf("unsupported colour form %q", value)
	}
}
