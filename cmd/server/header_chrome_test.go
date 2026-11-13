package main

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// Issue #323: the header fades into the body, and the two monochrome themes
// invert it against the page. The inversion only works if every colour in the
// bar reads the --header-* tokens: one rule left on --text-primary renders black
// on black in light mode and disappears silently.
//
// These tests pin both halves of that contract — the palette inverts, and the
// chrome uses it — plus the geometric coupling that keeps the fade from covering
// the top of the page.

var (
	themeBlock   = regexp.MustCompile(`(?is)html\[data-theme="([a-z-]+)"\]\s*\{([^}]*)\}`)
	customProp   = regexp.MustCompile(`(?m)^\s*(--[a-z0-9-]+)\s*:\s*([^;]+);`)
	cssBlock     = regexp.MustCompile(`(?is)([^{}]+)\{([^}]*)\}`)
	defaultHeaderBlock = regexp.MustCompile(`(?im)^html\s*\{\s*((?:--header-[a-z-]+\s*:[^;]+;\s*)+)\}`)
	colourDeclRe = regexp.MustCompile(`(?im)^\s*(?:color|background|background-color|border-color|border-top-color|border-bottom-color|fill|stroke)\s*:\s*([^;]+);`)
)

func monochromeCSS(t *testing.T) string {
	t.Helper()
	t.Chdir("../..")
	source, err := os.ReadFile(filepath.Join("web", "static", "css", "ui", "40-velm-monochrome.css"))
	if err != nil {
		t.Fatalf("read theme stylesheet: %v", err)
	}
	return string(source)
}

// themeTokens returns the custom properties declared inside one theme block.
func themeTokens(t *testing.T, source, theme string) map[string]string {
	t.Helper()
	for _, m := range themeBlock.FindAllStringSubmatch(source, -1) {
		if m[1] != theme {
			continue
		}
		tokens := map[string]string{}
		// Defaults on html first, so a theme that only overrides part of the
		// header palette still resolves the rest.
		if defaults := defaultHeaderBlock.FindStringSubmatch(source); defaults != nil {
			for _, prop := range customProp.FindAllStringSubmatch(defaults[1], -1) {
				tokens[strings.TrimSpace(prop[1])] = strings.TrimSpace(prop[2])
			}
		}
		for _, prop := range customProp.FindAllStringSubmatch(m[2], -1) {
			tokens[strings.TrimSpace(prop[1])] = strings.TrimSpace(prop[2])
		}
		return tokens
	}
	t.Fatalf("no token block for data-theme=%q", theme)
	return nil
}

// TestMonochromeHeaderInvertsAgainstThePage is the decision Andy asked for:
// black and white themes invert the bar, the chromatic ones keep their colour.
func TestMonochromeHeaderInvertsAgainstThePage(t *testing.T) {
	source := monochromeCSS(t)

	for _, theme := range []string{"light", "dark"} {
		tokens := themeTokens(t, source, theme)
		page, header := tokens["--bg-base"], tokens["--header-bg"]
		if page == "" || header == "" {
			t.Fatalf("%s: --bg-base=%q --header-bg=%q, both must be declared", theme, page, header)
		}
		if page == header {
			t.Errorf("%s: header background %q matches the page; the monochrome themes must invert it", theme, header)
		}
		// Inverted means opposite ends of the scale, not merely different.
		if !((theme == "light" && header == "#000" && page == "#fff") ||
			(theme == "dark" && header == "#fff" && page == "#000")) {
			t.Errorf("%s: expected the bar inverted against the page, got header %q on page %q", theme, header, page)
		}
		ink := tokens["--header-fg"]
		if ink == "" || ink == header {
			t.Errorf("%s: --header-fg %q must contrast with --header-bg %q", theme, ink, header)
		}
	}

	// The chromatic themes keep their own colour in the bar: they must not
	// override the header palette at all, so they inherit the page's.
	for _, theme := range []string{"yellow", "purple", "crimson", "mint"} {
		for _, m := range themeBlock.FindAllStringSubmatch(source, -1) {
			if m[1] != theme {
				continue
			}
			if strings.Contains(m[2], "--header-") {
				t.Errorf("%s: declares a --header-* token; the chromatic themes must keep the bar the colour of the page", theme)
			}
		}
	}

	// The default is what those four inherit.
	defaults := regexp.MustCompile(`(?is)html\s*\{\s*((?:--header-[a-z-]+\s*:[^;]+;\s*)+)\}`)
	m := defaults.FindStringSubmatch(source)
	if m == nil {
		t.Fatal("no default --header-* block on html; the chromatic themes would have no header colour at all")
	}
	for _, want := range []string{"--header-bg", "--header-fg", "--header-h", "--header-fade"} {
		if !strings.Contains(m[1], want) {
			t.Errorf("default header block is missing %s", want)
		}
	}
}

// TestHeaderChromeUsesHeaderTokens walks every rule whose selector targets the
// bar's own controls and requires its colours to come from --header-*. A page
// token inside the bar is exactly the bug this catches: black text on the black
// inverted bar, invisible in production but fine in every other theme.
func TestHeaderChromeUsesHeaderTokens(t *testing.T) {
	source := monochromeCSS(t)

	// Selectors that only ever appear inside the bar.
	chromeOnly := []string{
		".ui-topnav-search",
		".ui-topnav-icon",
		".ui-topnav-user-trigger",
		".ui-topnav-mobile-search",
		".ui-topnav-brand",
		".ui-theme-select",
		"#progress-bar",
	}
	// Page tokens that must not appear in a colour declaration for chrome.
	forbidden := []string{"--text-primary", "--text-secondary", "--text-dim", "--text-ghost", "--bg-base", "--bg-elevated", "--border", "--border-mid", "--border-bright", "--surface-soft"}

	checked := 0
	for _, block := range cssBlock.FindAllStringSubmatch(source, -1) {
		selector, body := strings.TrimSpace(block[1]), block[2]
		if strings.HasPrefix(selector, "@") {
			continue
		}
		isChrome := false
		for _, sel := range chromeOnly {
			if strings.Contains(selector, sel) {
				isChrome = true
				break
			}
		}
		if !isChrome {
			continue
		}
		// A block that shares a selector with page controls is not chrome-only;
		// .ui-topnav-search, .ui-input, ... is the page rule and is checked by
		// the later chrome-only block instead.
		if sharedSelector(selector) {
			continue
		}
		for _, decl := range colourDeclRe.FindAllStringSubmatch(body, -1) {
			checked++
			value := decl[1]
			if strings.Contains(value, "var(--header-") || strings.Contains(value, "transparent") || strings.Contains(value, "none") {
				continue
			}
			for _, token := range forbidden {
				if strings.Contains(value, "var("+token+")") {
					t.Errorf("selector %q: colour %q uses %s; header chrome must read --header-* or it vanishes when the bar inverts", selector, value, token)
				}
			}
		}
	}
	if checked < 6 {
		t.Fatalf("checked only %d chrome colour declarations; the parser is not matching the bar's rules", checked)
	}
}

// sharedSelector reports selectors that mix the bar with page controls, such as
// ".ui-topnav-search,\n.ui-input,\n.ui-select". Those set the page defaults and
// the bar's own values are layered on top by a chrome-only rule.
func sharedSelector(selector string) bool {
	parts := strings.Split(selector, ",")
	if len(parts) < 2 {
		return false
	}
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if !strings.Contains(p, "ui-topnav") && !strings.Contains(p, "progress-bar") {
			return true
		}
	}
	return false
}

// TestContentClearsTheHeaderFade is the geometric half. The fade paints an opaque
// gradient from the bar's colour down to transparent over --header-fade pixels;
// if the content offset ignores it, the first line of every page sits under an
// opaque wash and is simply invisible at rest.
func TestContentClearsTheHeaderFade(t *testing.T) {
	mono := monochromeCSS(t) // chdirs to the repo root
	tokens := themeTokens(t, mono, "light")
	if tokens["--header-fade"] == "" {
		t.Fatal("--header-fade is not declared; without it the fade height and the content offset cannot be kept in step")
	}

	source, err := os.ReadFile(filepath.Join("web", "static", "css", "ui", "10-core.css"))
	if err != nil {
		t.Fatalf("read core stylesheet: %v", err)
	}
	shell := regexp.MustCompile(`(?is)\.ui-main-shell\s*\{([^}]*)\}`).FindStringSubmatch(string(source))
	if shell == nil {
		t.Fatal("no .ui-main-shell rule found")
	}
	margin := regexp.MustCompile(`(?im)^\s*margin-top\s*:\s*([^;]+);`).FindStringSubmatch(shell[1])
	if margin == nil {
		t.Fatal(".ui-main-shell has no margin-top; it must clear the bar")
	}
	if !strings.Contains(margin[1], "--header-fade") {
		t.Errorf(".ui-main-shell margin-top = %q, which ignores --header-fade: the gradient would cover the top of every page", strings.TrimSpace(margin[1]))
	}

	theme := mono
	if !strings.Contains(theme, ".ui-topnav::after") {
		t.Error("no .ui-topnav::after rule: the header has no fade")
	}
	fade := regexp.MustCompile(`(?is)\.ui-topnav::after\s*\{([^}]*)\}`).FindStringSubmatch(theme)
	if fade == nil {
		t.Fatal("could not parse .ui-topnav::after")
	}
	if !strings.Contains(fade[1], "height: var(--header-fade)") {
		t.Error("the fade's height must come from --header-fade so it stays in step with the content offset")
	}
	if !strings.Contains(fade[1], "linear-gradient") {
		t.Error("the fade must be a gradient, not a flat block")
	}
	if !strings.Contains(fade[1], "pointer-events: none") {
		t.Error("the fade must not swallow clicks in the strip below the bar")
	}
}
