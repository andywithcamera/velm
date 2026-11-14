package main

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// Issue #326: the ambient overlay never painted.
//
// velm-overlay.js appends a fixed .ui-overlay-canvas to <body> and the CSS
// gives it `z-index: -1`, so it sits behind all app content. body also declared
// `background: var(--bg-base)`, opaque, which paints over a negative-z-index
// child of body. The canvas was created, sized and animating, and not one pixel
// of it was visible — measured in production at 0 differing pixels between
// overlay off and overlay on, then 1748 pixels once body was made transparent.
//
// The pairing that matters: a canvas at z-index -1 is only visible if no
// ancestor between it and the root paints an opaque background. <html> may
// paint (negative-z children paint above the root background); <body> may not.

var (
	cssRuleBlock  = regexp.MustCompile(`(?s)([^{}]*)\{([^}]*)\}`)
	backgroundDec = regexp.MustCompile(`(?i)\bbackground(?:-color)?\s*:\s*([^;}]+)`)
	overlayCanvas = regexp.MustCompile(`(?s)\.ui-overlay-canvas\s*\{(.*?)\}`)
	canvasZIndex  = regexp.MustCompile(`(?i)z-index\s*:\s*(-?[0-9]+)`)
)

// elementBackgrounds returns, for each selector list that targets the bare
// `body` element, the background declaration values found in that rule.
func elementBackgrounds(source string) map[string][]string {
	out := map[string][]string{}
	for _, m := range cssRuleBlock.FindAllStringSubmatch(source, -1) {
		selectors, decls := m[1], m[2]
		for _, sel := range strings.Split(selectors, ",") {
			sel = strings.TrimSpace(sel)
			if sel == "" {
				continue
			}
			for _, bg := range backgroundDec.FindAllStringSubmatch(decls, -1) {
				out[sel] = append(out[sel], strings.TrimSpace(bg[1]))
			}
		}
	}
	return out
}

// uiCSSFiles lists every stylesheet under web/static/css/ui.
func uiCSSFiles(t *testing.T) []string {
	t.Helper()
	files, err := filepath.Glob(filepath.Join(repoRoot(t), "web", "static", "css", "ui", "*.css"))
	if err != nil {
		t.Fatalf("glob stylesheets: %v", err)
	}
	if len(files) == 0 {
		t.Fatal("no stylesheets found under web/static/css/ui")
	}
	return files
}

// TestBodyNeverPaintsOpaqueBackground is the regression test for #326. Any
// opaque background on the body element hides the overlay canvas, which is the
// only negative-z-index layer the app has.
func TestBodyNeverPaintsOpaqueBackground(t *testing.T) {
	for _, path := range uiCSSFiles(t) {
		source, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		for selector, values := range elementBackgrounds(string(source)) {
			// Only rules that target the body element itself.
			if !targetsBodyElement(selector) {
				continue
			}
			for _, value := range values {
				lower := strings.ToLower(value)
				if lower == "transparent" || lower == "none" || lower == "inherit" {
					continue
				}
				if strings.HasPrefix(lower, "rgba(") && strings.HasSuffix(lower, ", 0)") {
					continue
				}
				t.Errorf("%s: selector %q sets background: %s\nbody must stay transparent because the ambient overlay canvas is at z-index -1 and an opaque body background paints over it (#326). Move the theme colour to <html>.",
					filepath.Base(path), selector, value)
			}
		}
	}
}

// targetsBodyElement reports whether a selector paints the body element itself,
// as opposed to a descendant such as `body.some-class` or `.wrapper body`.
func targetsBodyElement(selector string) bool {
	sel := strings.TrimSpace(selector)
	if sel == "" || strings.ContainsAny(sel, ".[#:>+~") {
		return false
	}
	fields := strings.Fields(sel)
	if len(fields) == 0 {
		return false
	}
	return fields[len(fields)-1] == "body"
}

// TestOverlayCanvasPayloadIsBehindContent pins why the body background matters:
// the canvas is fixed and at a negative z-index. If it ever moves above 0 the
// constraint on body changes, and this test should be revisited deliberately
// rather than by accident.
func TestOverlayCanvasPayloadIsBehindContent(t *testing.T) {
	source, err := os.ReadFile(filepath.Join(repoRoot(t), "web", "static", "css", "ui", "45-overlays.css"))
	if err != nil {
		t.Fatalf("read overlays stylesheet: %v", err)
	}
	block := overlayCanvas.FindStringSubmatch(string(source))
	if block == nil {
		t.Fatal(".ui-overlay-canvas rule not found in 45-overlays.css")
	}
	z := canvasZIndex.FindStringSubmatch(block[1])
	if z == nil {
		t.Fatal(".ui-overlay-canvas has no z-index; without a negative value it would paint over app content")
	}
	if z[1] != "-1" {
		t.Errorf(".ui-overlay-canvas z-index = %s, want -1.\nIf this is now >= 0 the canvas is above content and the body background rule no longer protects it; update issue #326 reasoning and this test together.", z[1])
	}
}

// TestThemeBackgroundIsOnHTML keeps the colour claim honest: if body stops
// painting, something must still paint the theme background, or transparent
// pages bleed through to white.
func TestThemeBackgroundIsOnHTML(t *testing.T) {
	for _, path := range uiCSSFiles(t) {
		source, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		if !regexp.MustCompile(`(?i)html\s*\{[^}]*background\s*:`).Match(source) &&
			!regexp.MustCompile(`(?i)(^|\s)html\s*,`).Match(source) {
			continue
		}
		return // at least one stylesheet paints the html element
	}
	t.Error("no stylesheet paints a background on <html>; with body transparent the page has no theme colour (#326)")
}
