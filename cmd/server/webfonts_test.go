package main

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// Issue #319: the app's webfonts never loaded in production.
//
// layout.html pulled Syne, DM Sans and JetBrains Mono from fonts.googleapis.com.
// Production CSP is `style-src 'self' 'unsafe-inline'` and `font-src 'self'
// data:`, so the stylesheet was blocked and, had it loaded, the .woff2 files
// from fonts.gstatic.com would have been blocked too. There was no visible
// error: every `font-family: "Syne", sans-serif` simply fell back to the system
// font. The tests below pin the fix (serve from origin) and the property that
// broke (subresources must be same-origin, because CSP forbids anything else).

var (
	linkTag       = regexp.MustCompile(`(?is)<link\b[^>]*>`)
	scriptSrcTag  = regexp.MustCompile(`(?is)<script\b[^>]*\bsrc\s*=\s*["']([^"']+)["']`)
	hrefAttr      = regexp.MustCompile(`(?is)\bhref\s*=\s*["']([^"']+)["']`)
	relAttr       = regexp.MustCompile(`(?is)\brel\s*=\s*["']([^"']+)["']`)
	asAttr        = regexp.MustCompile(`(?is)\bas\s*=\s*["']([^"']+)["']`)
	absoluteURL   = regexp.MustCompile(`(?i)^(?:[a-z][a-z0-9+.\-]*:)?//`)
	fontFaceURL   = regexp.MustCompile(`(?is)url\(\s*["']?([^"')]+)["']?\s*\)`)
	fontFaceBlock = regexp.MustCompile(`(?is)@font-face\s*\{[^}]*\}`)
	fontFamilyCSS = regexp.MustCompile(`(?is)font-family\s*:\s*([^;}]+)`)
	fontFaceFam   = regexp.MustCompile(`(?is)font-family\s*:\s*["']([^"']+)["']`)
	fontWeightDef = regexp.MustCompile(`(?is)font-weight\s*:\s*([0-9]+(?:\s+[0-9]+)?)`)
)

// subresourceRefs returns every URL a template loads as a subresource:
// stylesheets, preloaded fonts, and scripts. Anchors and images are excluded on
// purpose — a link to GitHub is fine, a stylesheet from Google is not.
func subresourceRefs(source []byte) []string {
	var refs []string
	for _, tag := range linkTag.FindAllString(string(source), -1) {
		rel := relAttr.FindStringSubmatch(tag)
		if rel == nil {
			continue
		}
		relValue := strings.ToLower(rel[1])
		isStylesheet := strings.Contains(relValue, "stylesheet")
		isFontPreload := strings.Contains(relValue, "preload") && asAttr.MatchString(tag) &&
			strings.EqualFold(asAttr.FindStringSubmatch(tag)[1], "font")
		if !isStylesheet && !isFontPreload {
			continue
		}
		if href := hrefAttr.FindStringSubmatch(tag); href != nil {
			refs = append(refs, href[1])
		}
	}
	for _, m := range scriptSrcTag.FindAllStringSubmatch(string(source), -1) {
		refs = append(refs, m[1])
	}
	return refs
}

// knownOffOrigin is a ratchet, not a permission. Each entry is a subresource
// that production CSP already blocks today, with the issue that will remove it.
// It exists so this test can fail on new violations while the existing ones are
// paid down; an entry with no issue number is a bug in the list.
var knownOffOrigin = map[string]string{
	// CodeMirror from jsdelivr: the run-script editor never initialises.
	filepath.Join("web", "templates", "run-script-view.html"): "#321",
}

// TestTemplatesLoadOnlySameOriginSubresources is the direct guard on #319. CSP
// allows style/script/font from 'self' only, so any absolute subresource URL is
// dead weight at best and a silent visual regression at worst.
func TestTemplatesLoadOnlySameOriginSubresources(t *testing.T) {
	root := repoRoot(t)
	for _, path := range templateFiles(t) {
		source, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			t.Fatalf("relative path for %s: %v", path, err)
		}
		issue, known := knownOffOrigin[rel]
		for _, ref := range subresourceRefs(source) {
			if !absoluteURL.MatchString(ref) {
				continue
			}
			if known {
				t.Logf("%s: %q is off-origin, already blocked in production and tracked by %s", rel, ref, issue)
				continue
			}
			t.Errorf("%s loads %q from another origin; production CSP allows style-src/script-src/font-src 'self' only, so it will be blocked. Serve it from /static/ instead", rel, ref)
		}
	}
}

// The ratchet must not outlive its reason: a template that has been cleaned up
// should lose its entry, so the exception cannot quietly become permanent.
func TestOffOriginAllowlistDoesNotOutliveItsFix(t *testing.T) {
	root := repoRoot(t)
	for path, issue := range knownOffOrigin {
		if !strings.HasPrefix(issue, "#") {
			t.Errorf("knownOffOrigin[%s] = %q: no issue reference", path, issue)
		}
		source, err := os.ReadFile(filepath.Join(root, path))
		if err != nil {
			t.Errorf("knownOffOrigin[%s]: %v (remove the stale entry)", path, err)
			continue
		}
		found := false
		for _, ref := range subresourceRefs(source) {
			if absoluteURL.MatchString(ref) {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("knownOffOrigin[%s] is stale: %s now loads only same-origin subresources, so the entry can go", path, issue)
		}
	}
}

// TestSelfHostedFontFacesResolveToRealFiles catches a mistyped or missing font
// path, which is the failure mode self-hosting introduces: the browser drops a
// font it cannot fetch without an error the app can see.
func TestSelfHostedFontFacesResolveToRealFiles(t *testing.T) {
	t.Chdir("../..")
	const sheet = "web/static/css/ui/01-fonts.css"

	source, err := os.ReadFile(sheet)
	if err != nil {
		t.Fatalf("read %s: %v", sheet, err)
	}
	blocks := fontFaceBlock.FindAllString(string(source), -1)
	if len(blocks) == 0 {
		t.Fatalf("%s declares no @font-face rules", sheet)
	}

	for _, block := range blocks {
		family := fontFaceFam.FindStringSubmatch(block)
		if family == nil {
			t.Errorf("block has no quoted font-family: %s", strings.TrimSpace(block))
			continue
		}
		srcs := fontFaceURL.FindAllStringSubmatch(block, -1)
		if len(srcs) == 0 {
			t.Errorf("%s: no url() source", family[1])
			continue
		}
		for _, match := range srcs {
			ref := match[1]
			if absoluteURL.MatchString(ref) {
				t.Errorf("%s: source %q is off-origin; CSP font-src is 'self' data:", family[1], ref)
				continue
			}
			// url() in a stylesheet resolves relative to that stylesheet.
			resolved := filepath.Join(filepath.Dir(sheet), filepath.FromSlash(ref))
			info, err := os.Stat(resolved)
			if err != nil {
				t.Errorf("%s: source %q does not exist (%v)", family[1], ref, err)
				continue
			}
			if info.Size() == 0 {
				t.Errorf("%s: source %q is empty", family[1], ref)
			}
			head := make([]byte, 4)
			file, err := os.Open(resolved)
			if err != nil {
				t.Errorf("%s: open %q: %v", family[1], ref, err)
				continue
			}
			if _, err := file.Read(head); err != nil {
				_ = file.Close()
				t.Errorf("%s: read %q: %v", family[1], ref, err)
				continue
			}
			_ = file.Close()
			if string(head) != "wOF2" {
				t.Errorf("%s: %q is not a woff2 file (magic %q)", family[1], ref, string(head))
			}
		}
	}
}

// TestEveryDeclaredFontHasAFace stops a stylesheet from asking for a family that
// nothing serves. That is exactly the state #319 left the app in for months: the
// rules were there, the face was not.
func TestEveryDeclaredFontHasAFace(t *testing.T) {
	t.Chdir("../..")
	sheets, err := filepath.Glob(filepath.Join("web", "static", "css", "ui", "*.css"))
	if err != nil {
		t.Fatalf("glob ui css: %v", err)
	}
	fontsSheet, err := os.ReadFile(filepath.Join("web", "static", "css", "ui", "01-fonts.css"))
	if err != nil {
		t.Fatalf("read fonts sheet: %v", err)
	}
	var served []string
	for _, m := range fontFaceFam.FindAllStringSubmatch(string(fontsSheet), -1) {
		served = append(served, strings.ToLower(strings.TrimSpace(m[1])))
	}
	if len(served) == 0 {
		t.Fatal("no @font-face families declared")
	}

	// Families that are expected to be absent: stacks that intentionally rely on
	// a font installed on the machine. A webfont is self-hosted or it is a bug.
	localOnly := map[string]bool{
		"ibm plex mono":      true, // builder editor; never shipped, falls back to SFMono/Consolas
		"sfmono-regular":     true,
		"sf mono":            true,
		"menlo":              true,
		"monaco":             true,
		"consolas":           true,
		"courier new":        true,
		"ui-monospace":       true,
		"segoe ui":           true,
		"blinkmacsystemfont": true,
	}
	for _, sheet := range sheets {
		if strings.HasSuffix(sheet, "01-fonts.css") {
			continue
		}
		source, err := os.ReadFile(sheet)
		if err != nil {
			t.Fatalf("read %s: %v", sheet, err)
		}
		for _, m := range fontFamilyCSS.FindAllStringSubmatch(string(source), -1) {
			for _, raw := range strings.Split(m[1], ",") {
				name := strings.ToLower(strings.TrimSpace(raw))
				if !strings.HasPrefix(name, "\"") && !strings.HasPrefix(name, "'") {
					continue // generic family or keyword such as inherit/monospace
				}
				name = strings.ToLower(strings.Trim(strings.TrimSpace(raw), `"'`))
				if localOnly[name] {
					continue
				}
				found := false
				for _, s := range served {
					if s == name {
						found = true
						break
					}
				}
				if !found {
					t.Errorf("%s uses font-family %q but no @font-face declares it; it will silently fall back to the system font", sheet, name)
				}
			}
		}
	}
}

// TestFontWeightsStayInsideTheVariableAxis checks the assumption that lets one
// file serve a whole family: the wght axis must span every weight a stylesheet
// asks for. Syne stops at 800, so a `font-weight: 900` rule would render faux
// bold from the 800 face — real, but not what the designer drew.
func TestFontWeightsStayInsideTheVariableAxis(t *testing.T) {
	t.Chdir("../..")
	fontsSheet, err := os.ReadFile(filepath.Join("web", "static", "css", "ui", "01-fonts.css"))
	if err != nil {
		t.Fatalf("read fonts sheet: %v", err)
	}

	type axis struct{ min, max int }
	ranges := map[string]axis{}
	for _, block := range fontFaceBlock.FindAllString(string(fontsSheet), -1) {
		family := fontFaceFam.FindStringSubmatch(block)
		weight := fontWeightDef.FindStringSubmatch(block)
		if family == nil || weight == nil {
			continue
		}
		a := axis{}
		fields := strings.Fields(weight[1])
		fmt.Sscanf(fields[0], "%d", &a.min)
		a.max = a.min
		if len(fields) > 1 {
			fmt.Sscanf(fields[1], "%d", &a.max)
		}
		ranges[strings.ToLower(strings.TrimSpace(family[1]))] = a
	}

	sheets, err := filepath.Glob(filepath.Join("web", "static", "css", "ui", "*.css"))
	if err != nil {
		t.Fatalf("glob ui css: %v", err)
	}
	checked := 0
	for _, sheet := range sheets {
		if strings.HasSuffix(sheet, "01-fonts.css") {
			continue
		}
		source, err := os.ReadFile(sheet)
		if err != nil {
			t.Fatalf("read %s: %v", sheet, err)
		}
		// A declaration block sets both properties or it is not our concern.
		for _, block := range strings.Split(string(source), "}") {
			family := fontFamilyCSS.FindStringSubmatch(block)
			weight := fontWeightDef.FindStringSubmatch(block)
			if family == nil || weight == nil {
				continue
			}
			var want int
			fmt.Sscanf(weight[1], "%d", &want)
			for _, raw := range strings.Split(family[1], ",") {
				name := strings.ToLower(strings.Trim(strings.TrimSpace(raw), `"'`))
				a, ok := ranges[name]
				if !ok {
					continue
				}
				checked++
				if want < a.min || want > a.max {
					t.Errorf("%s: font-weight %d on %q is outside its declared axis %d-%d; the browser will synthesise the missing weight", filepath.Base(sheet), want, name, a.min, a.max)
				}
			}
		}
	}
	if checked == 0 {
		t.Fatal("checked no family/weight pairs; the parser is not seeing the stylesheets")
	}
}

// TestSelfHostedFontsAreServedByTheStaticHandler checks the end of the path: the
// files sit under the /static/ root the app actually serves, with a font
// content type and the asset cache policy.
func TestSelfHostedFontsAreServedByTheStaticHandler(t *testing.T) {
	t.Chdir("../..")
	handler := newStaticAssetHandler("./web/static")

	for _, name := range []string{"instrument-sans.woff2", "monaspace-neon.woff2"} {
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/fonts/"+name, nil))
		if rec.Code != http.StatusOK {
			t.Fatalf("GET /fonts/%s: status = %d, want 200", name, rec.Code)
		}
		if got := rec.Header().Get("Content-Type"); !strings.Contains(got, "font/woff2") {
			t.Errorf("GET /fonts/%s: Content-Type = %q, want font/woff2", name, got)
		}
		if got := rec.Header().Get("Cache-Control"); got != "no-cache" {
			t.Errorf("GET /fonts/%s: Cache-Control = %q, want no-cache (#188)", name, got)
		}
	}
}

// The UI manifest is what the layout actually links, so the @font-face rules
// have to be reachable through it.
func TestFontsStylesheetIsImportedByTheUIManifest(t *testing.T) {
	t.Chdir("../..")
	manifest, err := os.ReadFile(filepath.Join("web", "static", "css", "ui.css"))
	if err != nil {
		t.Fatalf("read ui.css: %v", err)
	}
	if !strings.Contains(string(manifest), "ui/01-fonts.css") {
		t.Error("web/static/css/ui.css does not import ui/01-fonts.css, so no @font-face reaches the page")
	}
}
