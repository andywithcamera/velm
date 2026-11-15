package main

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// #352 R2: the style axis. A style sets geometry and typography; a theme sets
// colour. These tests hold the three places a pack is declared — the registry,
// the stylesheet and the dropdown — in agreement, because a pack missing from
// any one of them fails silently: an option that sets data-style with no CSS
// block looks like the picker doing nothing.

func stylePackSource(t *testing.T) string {
	t.Helper()
	path := filepath.Join(repoRoot(t), "web", "static", "css", "ui", "60-style-packs.css")
	source, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(source)
}

func TestNormalizeStyleFallsBackToDefault(t *testing.T) {
	if got := normalizeStyle(""); got != defaultStyle {
		t.Errorf("normalizeStyle(\"\") = %q; want %q", got, defaultStyle)
	}
	if got := normalizeStyle("comic-sans"); got != defaultStyle {
		t.Errorf("normalizeStyle(unknown) = %q; want %q — an unknown value must not reach data-style, where it would match no block and leave the app with the token set of no pack", got, defaultStyle)
	}
	for style := range validStyles {
		if got := normalizeStyle(style); got != style {
			t.Errorf("normalizeStyle(%q) = %q; a registered style must survive normalisation", style, got)
		}
	}
}

// TestEveryRegisteredStyleHasAPack is the registry/stylesheet pair. "modern" is
// the default and is declared by the token block in 10-core.css, so it is the
// only legitimate absence.
func TestEveryRegisteredStyleHasAPack(t *testing.T) {
	source := stylePackSource(t)
	for style := range validStyles {
		if style == defaultStyle {
			continue
		}
		block := regexp.MustCompile(`html\[data-style="` + regexp.QuoteMeta(style) + `"\]\s*\{`)
		if !block.MatchString(source) {
			t.Errorf("validStyles has %q but 60-style-packs.css has no html[data-style=%q] block; selecting it would change nothing on screen", style, style)
		}
	}
}

// TestNoPackWithoutARegistryEntry is the mirror: a block nothing can select is
// dead weight that accumulates and then lies about which styles exist.
func TestNoPackWithoutARegistryEntry(t *testing.T) {
	source := stylePackSource(t)
	for _, m := range regexp.MustCompile(`html\[data-style="([a-z0-9]+)"\]`).FindAllStringSubmatch(source, -1) {
		if !validStyles[m[1]] {
			t.Errorf("60-style-packs.css styles %q but validStyles does not list it; the server would normalise it away, so the block is unreachable", m[1])
		}
	}
}

// TestStylePacksDeclareNoColour holds the line that makes themes x styles
// unnecessary. If a pack sets a colour, every theme needs reviewing against
// every style, and a new style silently restyles colours it never saw.
//
// The one exception is a shadow: a pack may offset one, and black is neutral on
// both light and dark themes.
func TestStylePacksDeclareNoColour(t *testing.T) {
	// Comments explain the exception and must not be read as declarations —
	// the shadow's rgba() is documented in prose right beside it.
	source := regexp.MustCompile(`(?s)/\*.*?\*/`).ReplaceAllString(stylePackSource(t), "")
	colourProperty := regexp.MustCompile(`(?i)(^|[;{\s])(color|background|background-color|border-color|fill|stroke|--bg-[a-z0-9-]+|--text-[a-z0-9-]+|--border[a-z0-9-]*|--status-[a-z0-9-]+|--blue-[a-z0-9-]+)\s*:`)
	for _, m := range regexp.MustCompile(`(?s)([^{}]*)\{([^}]*)\}`).FindAllStringSubmatch(source, -1) {
		selector, decls := strings.TrimSpace(m[1]), m[2]
		if colourProperty.MatchString(decls) {
			t.Errorf("style pack %q declares colour; a style must set geometry and type only — colour belongs to the theme axis", selector)
		}
		for _, decl := range strings.Split(decls, ";") {
			decl = strings.TrimSpace(decl)
			if decl == "" || !regexp.MustCompile(`(?i)#[0-9a-f]{3,8}\b|rgba?\(|\bhsla?\(`).MatchString(decl) {
				continue
			}
			if !strings.HasPrefix(decl, "--shadow-") {
				t.Errorf("style pack %q has a colour literal outside --shadow-*: %q", selector, decl)
			}
		}
	}
}

// TestStyleSelectMatchesRegistry keeps the dropdown, the client-side whitelist
// and the server registry in one piece. The three have to name the same styles:
// an option the client does not know is rewritten to the default on click, and
// an option the server does not know is rewritten on persist, so either way the
// picker appears broken while every layer reports success.
func TestStyleSelectMatchesRegistry(t *testing.T) {
	root := repoRoot(t)
	navbar, err := os.ReadFile(filepath.Join(root, "web", "templates", "components", "navbar.html"))
	if err != nil {
		t.Fatalf("read navbar: %v", err)
	}
	selectBlock := regexp.MustCompile(`(?s)<select id="velm-style-select".*?</select>`).FindString(string(navbar))
	if selectBlock == "" {
		t.Fatal("navbar has no #velm-style-select; the style axis is unreachable from the UI")
	}
	offered := map[string]bool{}
	for _, m := range regexp.MustCompile(`<option value="([^"]+)"`).FindAllStringSubmatch(selectBlock, -1) {
		offered[m[1]] = true
	}
	for style := range validStyles {
		if !offered[style] {
			t.Errorf("validStyles has %q but the dropdown does not offer it", style)
		}
	}
	for style := range offered {
		if !validStyles[style] {
			t.Errorf("the dropdown offers %q but validStyles does not accept it", style)
		}
	}

	layout, err := os.ReadFile(filepath.Join(root, "web", "templates", "layout.html"))
	if err != nil {
		t.Fatalf("read layout: %v", err)
	}
	if !strings.Contains(string(layout), `data-style="{{.Style}}"`) {
		t.Error(`layout.html does not render data-style from .Style; the pack would only apply after a client-side change, so every page load would flash the default shape`)
	}
	if !strings.Contains(string(layout), `"/api/preferences/style"`) {
		t.Error("layout.html never posts to /api/preferences/style; a style choice would be forgotten on reload")
	}
}

// TestStylePreferenceIsItsOwnKey is the copy-paste guard: the style handler is
// modelled on the theme handler, and writing the theme key from the style
// endpoint would make the two pickers overwrite each other.
func TestStylePreferenceIsItsOwnKey(t *testing.T) {
	keys := map[string]string{
		"theme":   themePreferenceKey,
		"overlay": overlayPreferenceKey,
		"style":   stylePreferenceKey,
	}
	seen := map[string]string{}
	for name, key := range keys {
		if other, clash := seen[key]; clash {
			t.Errorf("%s and %s both store to %q; changing one would overwrite the other", name, other, key)
		}
		seen[key] = name
	}
}
