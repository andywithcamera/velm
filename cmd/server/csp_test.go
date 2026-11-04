package main

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"velm/internal/security"
)

var (
	scriptTag         = regexp.MustCompile(`(?i)<script[^>]*>`)
	inlineHandlerAttr = regexp.MustCompile(`(?i)\son(click|change|input|submit|load|error|keyup|keydown|keypress|focus|blur|mouseover|mouseout|dblclick|play|ended|timeupdate)=`)
)

// A nonce cannot cover inline event handlers, so attribute-based handlers are
// rejected outright: they would be silently blocked once script-src stops
// allowing 'unsafe-inline'.
func TestTemplatesHaveNoInlineEventHandlers(t *testing.T) {
	for _, path := range templateFiles(t) {
		source, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		if match := inlineHandlerAttr.Find(source); match != nil {
			t.Errorf("%s uses inline event handler %q; use a data attribute handled by csp-behaviour.js", path, strings.TrimSpace(string(match)))
		}
	}
}

func TestTemplatesNonceEveryExecutableInlineScript(t *testing.T) {
	for _, path := range templateFiles(t) {
		source, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		for _, tag := range scriptTag.FindAllString(string(source), -1) {
			lower := strings.ToLower(tag)
			if strings.Contains(lower, " src=") {
				continue // external script, covered by the source list
			}
			if strings.Contains(lower, `type="application/json"`) {
				continue // data block, not executed
			}
			if !strings.Contains(tag, `nonce="{{.CSPNonce}}"`) {
				t.Errorf("%s has inline script tag without a nonce: %s", path, tag)
			}
		}
	}
}

// The rendered markup must carry the same nonce the response header advertises,
// otherwise the browser blocks every inline script on the page.
func TestRenderedScriptsCarryHeaderNonce(t *testing.T) {
	withRepoRoot(t)

	var nonce string
	handler := security.SecurityHeaders(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		nonce = security.NonceFromContext(r.Context())
		data := map[string]any{"Results": nil, "CSPNonce": nonce}
		var buf bytes.Buffer
		if err := templates.ExecuteTemplate(&buf, "search-results.html", data); err != nil {
			t.Fatalf("execute search-results: %v", err)
		}
		if !strings.Contains(buf.String(), `nonce="`+nonce+`"`) {
			t.Errorf("rendered markup does not carry nonce %q:\n%s", nonce, buf.String())
		}
	}))

	handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/", nil))
	if nonce == "" {
		t.Fatal("no nonce reached the handler")
	}
}

// withRepoRoot chdirs into the repository root so the relative template globs
// in initTemplates resolve, and restores the working directory afterwards.
func withRepoRoot(t *testing.T) {
	t.Helper()
	root := repoRoot(t)
	previous, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	if err := os.Chdir(root); err != nil {
		t.Fatalf("chdir %s: %v", root, err)
	}
	t.Cleanup(func() { _ = os.Chdir(previous) })
	initTemplates()
}

func repoRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	for {
		if info, err := os.Stat(filepath.Join(dir, "web", "templates")); err == nil && info.IsDir() {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("could not locate repository root containing web/templates")
		}
		dir = parent
	}
}

func templateFiles(t *testing.T) []string {
	t.Helper()
	root := repoRoot(t)
	patterns := []string{"web/templates/*.html", "web/templates/components/*.html"}
	var files []string
	for _, pattern := range patterns {
		matches, err := filepath.Glob(filepath.Join(root, pattern))
		if err != nil {
			t.Fatalf("glob %s: %v", pattern, err)
		}
		files = append(files, matches...)
	}
	if len(files) == 0 {
		t.Fatal("no templates found; run from the repository root")
	}
	return files
}
