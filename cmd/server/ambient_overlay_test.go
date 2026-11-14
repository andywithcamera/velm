package main

import (
	"os"
	"path/filepath"
	"regexp"
	"strconv"
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
// #326 fixed that by making body transparent. #334 then changed the intent: Andy
// wants the effect over the panels, not only in the gaps, so the canvas moved
// above every app layer. Both properties are pinned: one painter of the page
// colour, and a canvas above all content.

var (
	cssRuleBlock  = regexp.MustCompile(`(?s)([^{}]*)\{([^}]*)\}`)
	backgroundDec = regexp.MustCompile(`(?i)\bbackground(?:-color)?\s*:\s*([^;}]+)`)
	overlayCanvas = regexp.MustCompile(`(?s)\.ui-overlay-canvas\s*\{(.*?)\}`)
	canvasZIndex  = regexp.MustCompile(`(?i)z-index\s*:\s*(-?[0-9]+)`)
	anyZIndex     = regexp.MustCompile(`(?i)z-index\s*:\s*(-?[0-9]+)`)
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

// TestBodyNeverPaintsOpaqueBackground keeps the page colour coming from exactly
// one element. It was written for #326, when an opaque body background hid the
// then-negative-z overlay canvas; the overlay now paints above content, but a
// second opaque painter still causes white bands under short pages and makes
// theme colour ambiguous.
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
				t.Errorf("%s: selector %q sets background: %s\nbody must stay transparent; the theme colour is painted by <html> alone, so a second opaque painter causes white bands under short pages and makes theme colour ambiguous.",
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

// TestOverlayCanvasPaintsAboveAllContent is the regression test for #334: the
// overlay must render over panels and the header, not only in the gaps between
// them. It asserts the canvas's z-index is greater than every z-index in the app
// so it cannot be quietly overtaken by a new modal or toast layer.
func TestOverlayCanvasPaintsAboveAllContent(t *testing.T) {
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
		t.Fatal(".ui-overlay-canvas has no z-index; it would fall to auto and paint under positioned content")
	}
	canvasZ, err := strconv.Atoi(z[1])
	if err != nil {
		t.Fatalf("parse .ui-overlay-canvas z-index %q: %v", z[1], err)
	}
	if !strings.Contains(strings.ToLower(block[1]), "pointer-events: none") {
		t.Error(".ui-overlay-canvas must keep pointer-events: none; it covers the whole viewport, so without it nothing on the page is clickable")
	}

	highest, where := highestAppZIndex(t)
	if canvasZ <= highest {
		t.Errorf(".ui-overlay-canvas z-index = %d, but %s uses z-index %d.\nThe overlay must paint above all app content (#334); raise the canvas or lower that layer.", canvasZ, where, highest)
	}
}

// highestAppZIndex scans every app stylesheet and template for the largest
// z-index, ignoring the overlay sheet itself.
func highestAppZIndex(t *testing.T) (int, string) {
	t.Helper()
	root := repoRoot(t)
	var paths []string
	for _, pattern := range []string{
		filepath.Join(root, "web", "static", "css", "ui", "*.css"),
		filepath.Join(root, "web", "templates", "*.html"),
	} {
		matches, err := filepath.Glob(pattern)
		if err != nil {
			t.Fatalf("glob %s: %v", pattern, err)
		}
		paths = append(paths, matches...)
	}
	highest, where := 0, "nothing"
	for _, path := range paths {
		if strings.HasSuffix(path, "45-overlays.css") {
			continue
		}
		source, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		for _, m := range anyZIndex.FindAllStringSubmatch(string(source), -1) {
			v, err := strconv.Atoi(m[1])
			if err != nil {
				continue
			}
			if v > highest {
				highest, where = v, filepath.Base(path)
			}
		}
	}
	return highest, where
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
	t.Error("no stylesheet paints a background on <html>; with body transparent the page has no theme colour")
}
