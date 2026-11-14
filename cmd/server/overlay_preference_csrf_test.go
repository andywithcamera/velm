package main

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// Issue #329: the ambient overlay choice reverted to "none" on every page load.
//
// Every state-changing /api request is CSRF-validated server-side and refused
// with 403 without the header. persistTheme sent X-CSRF-Token; persistOverlay
// did not, so the POST that was meant to save the choice was rejected, the
// .catch() swallowed nothing (a 403 is a fulfilled promise, not a rejection),
// and the next render read the still-absent preference back as "none".
//
// Verified against a local server: no header -> 403 "CSRF validation failed",
// header -> 204, and GET then returned {"overlay":"aurora"}.

var (
	fetchCall  = regexp.MustCompile(`(?s)fetch\(\s*["'](/api/[^"']*)["']\s*,\s*\{(.*?)\}\s*\)`)
	httpMethod = regexp.MustCompile(`(?i)method\s*:\s*["']([A-Za-z]+)["']`)
	csrfHeader = regexp.MustCompile(`(?i)["']X-CSRF-Token["']`)
)

// TestStateChangingAPIFetchesSendCSRF is the ratchet: a fetch() to /api with a
// mutating method must carry X-CSRF-Token. A GET needs none, so those are
// skipped. This catches the class, not just the overlay instance.
func TestStateChangingAPIFetchesSendCSRF(t *testing.T) {
	// csrfExempt names call sites with a filed issue, so the ratchet can land
	// before they are fixed. Remove an entry when its issue closes.
	csrfExempt := map[string]int{
		"/api/app-runtime/call": 328,
	}
	checked := 0
	for _, file := range templateFiles(t) {
		source, err := os.ReadFile(file)
		if err != nil {
			t.Fatalf("read %s: %v", file, err)
		}
		for _, m := range fetchCall.FindAllStringSubmatch(string(source), -1) {
			url, opts := m[1], m[2]
			method := "GET"
			if mm := httpMethod.FindStringSubmatch(opts); mm != nil {
				method = strings.ToUpper(mm[1])
			}
			if method == "GET" || method == "HEAD" || method == "OPTIONS" {
				continue
			}
			if issue, ok := csrfExempt[url]; ok {
				t.Logf("%s: fetch(%q) exempt pending #%d", filepath.Base(file), url, issue)
				continue
			}
			checked++
			if !csrfHeader.MatchString(opts) {
				t.Errorf("%s: fetch(%q) is a %s with no X-CSRF-Token header; the server rejects it with 403",
					filepath.Base(file), url, method)
			}
		}
	}
	if checked == 0 {
		t.Fatal("found no state-changing /api fetch() calls; the matcher is not detecting them")
	}
}

// TestPersistOverlaySendsCSRF pins the specific function that regressed, so a
// rewrite of persistOverlay cannot quietly drop the header again.
func TestPersistOverlaySendsCSRF(t *testing.T) {
	source, err := os.ReadFile(filepath.Join(repoRoot(t), "web", "templates", "layout.html"))
	if err != nil {
		t.Fatalf("read layout: %v", err)
	}
	body := functionBody(t, string(source), "persistOverlay")
	if !csrfHeader.MatchString(body) {
		t.Error("persistOverlay does not send X-CSRF-Token; the preference will not persist")
	}
	if !strings.Contains(body, "/api/preferences/overlay") {
		t.Error("persistOverlay no longer posts to /api/preferences/overlay")
	}
}

// functionBody returns the text of a JS function declaration, up to its closing
// brace at the same indent, or the whole source tail if that cannot be found.
func functionBody(t *testing.T, source, name string) string {
	t.Helper()
	start := strings.Index(source, "function "+name+"(")
	if start < 0 {
		t.Fatalf("function %s not found", name)
	}
	rest := source[start:]
	end := strings.Index(rest, "\n                }")
	if end < 0 {
		return rest
	}
	return rest[:end]
}
