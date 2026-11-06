package main

import (
	"bytes"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// Issue #30: "clicking a tab sometimes returns to /".
//
// Two invariants keep it fixed, and both are cheap to check statically.
//
// First, no script may rewrite the address bar on load. web/static/js/
// maintain-base-url.js pushed "/" onto history on every page and again on every
// popstate, so for every page the browser believed it was at the root.
//
// Second, the app editor's links are built from the path the server rendered
// rather than from window.location. A page that derives its own links from the
// address bar is only correct while the address bar is, and the editor is
// served from two paths (/admin/app-editor and /builder/pages).

// history.pushState({}, ”, '/') and its replaceState twin: state object, unused
// title, then the root path.
var historyRewrite = regexp.MustCompile(`(?:push|replace)State\(\s*\{\}\s*,\s*['"]\s*['"]\s*,\s*['"]\s*/?\s*['"]\s*\)`)

func TestNoScriptRewritesTheAddressBarToRoot(t *testing.T) {
	withRepoRoot(t)

	paths, err := filepath.Glob(filepath.Join("web", "static", "js", "*.js"))
	if err != nil {
		t.Fatalf("glob static js: %v", err)
	}
	paths = append(paths, templateFiles(t)...)

	for _, path := range paths {
		source, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		if match := historyRewrite.Find(source); match != nil {
			t.Errorf("%s rewrites the address bar: %q. Every link built from window.location then points at the root, which is how #30 happened.", path, match)
		}
	}
}

func TestAppEditorLinksUseTheServedPathNotTheAddressBar(t *testing.T) {
	withRepoRoot(t)

	source, err := os.ReadFile(filepath.Join("web", "templates", "page-builder-view.html"))
	if err != nil {
		t.Fatalf("read template: %v", err)
	}
	text := string(source)

	if strings.Contains(text, "new URL(window.location.href)") {
		t.Error("page-builder-view.html builds a URL from window.location.href; build it from the served path ({{js .Uri}}) so a tab link cannot resolve against the root")
	}
	if !strings.Contains(text, `new URL("{{js .Uri}}", window.location.origin)`) {
		t.Error("page-builder-view.html does not derive its base URL from the served path ({{js .Uri}})")
	}
}

func TestLayoutDoesNotLoadTheAddressBarRewriteScript(t *testing.T) {
	withRepoRoot(t)

	if _, err := os.Stat(filepath.Join("web", "static", "js", "maintain-base-url.js")); err == nil {
		t.Error("web/static/js/maintain-base-url.js is back; it sets the address bar to / on every page load (#30)")
	}
	source, err := os.ReadFile(filepath.Join("web", "templates", "layout.html"))
	if err != nil {
		t.Fatalf("read layout: %v", err)
	}
	if strings.Contains(string(source), "maintain-base-url.js") {
		t.Error("layout.html loads maintain-base-url.js (#30)")
	}
}

// The base has to survive server-side escaping intact: the editor builds every
// tab link from it, and html/template rewrites a URL used inside a JS string.
func TestAppEditorRendersServedPathAsURLBase(t *testing.T) {
	t.Chdir("../..")
	tpl := parseTemplates(t)
	data := map[string]any{
		"Uri": "/admin/app-editor", "Title": "App Editor", "PageTitle": "App Editor", "Section": "Admin",
		"AppID": "system", "Menu": []any{}, "Properties": map[string]string{}, "User": nil,
		"Theme": "velm", "CSRFToken": "csrf-token", "RequestID": "req-1", "CSPNonce": "nonce-1",
		"Breadcrumbs":            []Breadcrumb{},
		"AppEditorSelectedApp":   map[string]any{"Name": "system", "DefinitionVersion": 1, "ObjectCount": 0},
		"AppEditorObjects":       []any{},
		"AppEditorInitialOpenID": "",
	}
	var buf bytes.Buffer
	if err := tpl.ExecuteTemplate(&buf, "page-builder-view", data); err != nil {
		t.Fatalf("page-builder-view render failed: %v", err)
	}
	// html/template writes JSON-safe slashes inside a script ("\/"), which is the
	// same string once JavaScript parses it, so compare on the unescaped form.
	rendered := strings.ReplaceAll(buf.String(), `\/`, "/")
	if !strings.Contains(rendered, `new URL("/admin/app-editor", window.location.origin)`) {
		t.Errorf("editor base URL did not render as /admin/app-editor; got:\n%s", firstLinesContaining(rendered, "editorBaseURL"))
	}
}

func firstLinesContaining(text, needle string) string {
	var out []string
	for _, line := range strings.Split(text, "\n") {
		if strings.Contains(line, needle) {
			out = append(out, strings.TrimSpace(line))
		}
	}
	return strings.Join(out, "\n")
}
