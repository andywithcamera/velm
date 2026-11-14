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
	themeBlock         = regexp.MustCompile(`(?is)html\[data-theme="([a-z-]+)"\]\s*\{([^}]*)\}`)
	customProp         = regexp.MustCompile(`(?m)^\s*(--[a-z0-9-]+)\s*:\s*([^;]+);`)
	cssBlock           = regexp.MustCompile(`(?is)([^{}]+)\{([^}]*)\}`)
	defaultHeaderBlock = regexp.MustCompile(`(?im)^html\s*\{\s*((?:--header-[a-z-]+\s*:[^;]+;\s*)+)\}`)
	colourDeclRe       = regexp.MustCompile(`(?im)^\s*(?:color|background|background-color|border-color|border-top-color|border-bottom-color|fill|stroke)\s*:\s*([^;]+);`)
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

// TestHeaderIsNotInvertedOrFaded pins the revert (#331). The bar is the page
// colour with a hairline under it; the black/white inversion and the gradient
// fade were tried in #324 and Andy called them a mistake. Both are cheap to
// reintroduce by accident, so they are asserted against.
func TestHeaderIsNotInvertedOrFaded(t *testing.T) {
	source := monochromeCSS(t) // chdirs to the repo root
	chrome := source

	for _, theme := range []string{"light", "dark"} {
		tokens := themeTokens(t, source, theme)
		matched, want := tokens["--header-bg"], tokens["--bg-base"]
		if matched != "var(--bg-base)" && matched != want {
			t.Errorf("theme %q sets --header-bg %q; the bar must match the page (%q)", theme, matched, want)
		}
		matchedFg, wantFg := tokens["--header-fg"], tokens["--text-primary"]
		if matchedFg != "var(--text-primary)" && matchedFg != wantFg {
			t.Errorf("theme %q sets --header-fg %q; the bar must not invert (%q)", theme, matchedFg, wantFg)
		}
	}

	if !strings.Contains(source, "--header-fade: 0px") {
		t.Error("--header-fade is not 0; the fade was reverted in #331")
	}
	if regexp.MustCompile(`(?s)\.ui-topnav::after`).MatchString(chrome) {
		t.Error(".ui-topnav::after still draws the fade gradient")
	}
	bar := ""
	for _, block := range cssBlock.FindAllStringSubmatch(chrome, -1) {
		if strings.TrimSpace(block[1]) == ".ui-topnav" {
			bar = block[2]
		}
	}
	if bar == "" {
		t.Fatal("no .ui-topnav rule in the shell stylesheet")
	}
	if !strings.Contains(bar, "border-bottom: 1px solid") {
		t.Errorf(".ui-topnav has no hairline under it; got %q", bar)
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

// TestHeaderChromeIsNeverTheLastWordOfASharedRule closes the hole the previous
// test leaves open, and it is how a real bug got through: a shared group
//
//	.ui-topnav-search::placeholder,
//	.ui-input::placeholder { color: var(--text-ghost); }
//
// parsed cleanly, because sharedSelector skipped it, and it was declared *after*
// the chrome-only rule that set --header-fg-ghost. The placeholder therefore
// resolved to --text-ghost, which in the light theme is black — on a black bar.
// The field looked empty in production and still passed every other test.
//
// The invariant: whatever a shared group sets on a header selector must be
// overridden later by a rule that is chrome-only and reads --header-*. Cascade
// order is the thing being pinned, so the test has to keep it.
func TestHeaderChromeIsNeverTheLastWordOfASharedRule(t *testing.T) {
	source := stripCSSComments(monochromeCSS(t))

	// Panels that float inside the bar are page surfaces by design (nav drawer,
	// user menu, search results), so their page tokens are correct, not a bug.
	pageSurface := func(sel string) bool {
		for _, panel := range []string{"search-results", "drawer", "user-menu", "nav-panel", "menu"} {
			if strings.Contains(sel, panel) {
				return true
			}
		}
		return false
	}
	headerSelector := func(sel string) bool {
		if pageSurface(sel) {
			return false
		}
		for _, chrome := range []string{".ui-topnav", "progress-bar", ".ui-theme-select"} {
			if strings.Contains(sel, chrome) {
				return true
			}
		}
		return false
	}
	// Properties whose value is a palette decision. Geometry and layout are
	// free to share a rule; colours are not.
	colourProp := regexp.MustCompile(`(?im)^\s*(color|background|background-color|border|border-color|border-top-color|border-bottom-color|box-shadow|fill|stroke|caret-color|outline-color)\s*:\s*([^;]+);`)

	type lastDecl struct {
		value  string
		shared bool
	}
	// One pass, in document order: the last declaration for a selector/property
	// pair is the one the cascade resolves to, so it is the only one that
	// matters. Two passes let an earlier chrome-only rule overwrite a later
	// shared one and the test went quiet.
	last := map[string]lastDecl{}

	for _, block := range cssBlock.FindAllStringSubmatch(source, -1) {
		selectorText, body := strings.TrimSpace(block[1]), block[2]
		if strings.HasPrefix(selectorText, "@") {
			continue
		}
		var selectors []string
		shared := false
		for _, raw := range strings.Split(selectorText, ",") {
			raw = strings.Join(strings.Fields(raw), " ")
			if raw == "" {
				continue
			}
			selectors = append(selectors, raw)
			if !headerSelector(raw) {
				shared = true
			}
		}
		for _, sel := range selectors {
			if !headerSelector(sel) {
				continue
			}
			for _, decl := range colourProp.FindAllStringSubmatch(body, -1) {
				last[sel+"|"+strings.ToLower(decl[1])] = lastDecl{value: strings.TrimSpace(decl[2]), shared: shared}
			}
		}
	}

	for key, decl := range last {
		if !decl.shared {
			continue
		}
		// transparent and none are palette-neutral: they show the bar through,
		// which is what we want, so they are not overrides that must be undone.
		if decl.value == "transparent" || decl.value == "none" {
			continue
		}
		parts := strings.SplitN(key, "|", 2)
		t.Errorf("selector %q property %q: last declaration is %q, from a rule shared with page surfaces; a shared rule must be overridden later by a chrome-only rule reading --header-*",
			parts[0], parts[1], decl.value)
	}
}

// stripCSSComments removes /* ... */ blocks. A comment sitting immediately
// before a rule lands inside the captured selector text, which silently splits
// one selector into a "shared" list and hides the rule from these tests.
func stripCSSComments(source string) string {
	return regexp.MustCompile(`(?s)/\*.*?\*/`).ReplaceAllString(source, "")
}
