package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// The GitHub fallback exists for the one environment we cannot change: the
// production builder strips .git before the build context, so `git describe`
// has no repository to read and the commit sha arrives as a build arg. These
// tests drive the same script against a stub API, so the fallback is proven
// without a deploy.
//
// Fixtures are marshalled with indentation on purpose: GitHub answers
// pretty-printed with a space after each colon, and a parser that assumes
// compact JSON reads nothing and silently reports the bare sha.

const (
	stubRepo   = "example/velm"
	tagName    = "v1.0.0"
	tagObject  = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	tagCommit  = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	builtCommi = "1234567890abcdef1234567890abcdef12345678"
)

func prettyJSON(t *testing.T, v any) string {
	t.Helper()
	out, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		t.Fatalf("marshal fixture: %v", err)
	}
	return string(out)
}

// stubGitHub serves the four endpoints the script uses. Each field decides what
// GitHub would answer for that situation.
type stubGitHub struct {
	tagRefType string // "tag" (annotated) or "commit" (lightweight)
	compare    map[string]any
}

func (s stubGitHub) start(t *testing.T) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/repos/"+stubRepo+"/git/refs/tags", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(prettyJSON(t, []map[string]any{{
			"ref": "refs/tags/" + tagName,
			"object": map[string]any{
				"sha":  tagObject,
				"type": s.tagRefType,
			},
		}})))
	})
	mux.HandleFunc("/repos/"+stubRepo+"/git/ref/tags/"+tagName, func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(prettyJSON(t, map[string]any{
			"ref": "refs/tags/" + tagName,
			"object": map[string]any{
				"sha":  tagObject,
				"type": s.tagRefType,
			},
		})))
	})
	mux.HandleFunc("/repos/"+stubRepo+"/git/tags/"+tagObject, func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(prettyJSON(t, map[string]any{
			"node_id": "TA_stub",
			"sha":     tagObject, // the tag object's own sha, first in the document
			"tag":     tagName,
			"object": map[string]any{
				"sha":  tagCommit,
				"type": "commit",
			},
		})))
	})
	mux.HandleFunc("/repos/"+stubRepo+"/compare/", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(prettyJSON(t, s.compare)))
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

func TestDescribeReleaseUsesGitHubWhenNoRepository(t *testing.T) {
	srv := stubGitHub{
		tagRefType: "tag",
		compare:    map[string]any{"status": "ahead", "ahead_by": 13},
	}.start(t)

	got := runScript(t, t.TempDir(), srv.URL, builtCommi)
	want := "v1.0.0-13-g" + builtCommi[:7]
	if got != want {
		t.Fatalf("release = %q, want %q", got, want)
	}
}

// A build sitting exactly on the tag names the tag, not "v1.0.0-0-g...".
func TestDescribeReleaseNamesTagExactlyWhenCommitIsTagged(t *testing.T) {
	srv := stubGitHub{
		tagRefType: "tag",
		compare:    map[string]any{"status": "identical", "ahead_by": 0},
	}.start(t)

	if got := runScript(t, t.TempDir(), srv.URL, tagCommit); got != tagName {
		t.Fatalf("release = %q, want %q", got, tagName)
	}
}

// A lightweight tag points straight at the commit; there is no tag object to
// dereference, and the script must not ask for one.
func TestDescribeReleaseHandlesLightweightTag(t *testing.T) {
	srv := stubGitHub{
		tagRefType: "commit",
		compare:    map[string]any{"status": "identical", "ahead_by": 0},
	}.start(t)

	if got := runScript(t, t.TempDir(), srv.URL, tagCommit); got != tagName {
		t.Fatalf("release = %q, want %q", got, tagName)
	}
}

// diverged means the commit is not descended from the tag, so no distance is
// meaningful: report the sha rather than a wrong version.
func TestDescribeReleaseFallsBackToShaWhenCompareDiverges(t *testing.T) {
	srv := stubGitHub{
		tagRefType: "tag",
		compare:    map[string]any{"status": "diverged", "ahead_by": 4},
	}.start(t)

	if got := runScript(t, t.TempDir(), srv.URL, builtCommi); got != builtCommi[:7] {
		t.Fatalf("release = %q, want short sha %q", got, builtCommi[:7])
	}
}

// An unreachable API must not fail the build or invent a value.
func TestDescribeReleaseReportsUnknownWhenAPIFails(t *testing.T) {
	if got := runScript(t, t.TempDir(), "http://127.0.0.1:1", builtCommi); got != unknownBuildValue {
		t.Fatalf("release = %q, want %q", got, unknownBuildValue)
	}
}

// With no repository and no commit there is nothing to ask about.
func TestDescribeReleaseReportsUnknownWithNoRepositoryAndNoCommit(t *testing.T) {
	srv := stubGitHub{
		tagRefType: "tag",
		compare:    map[string]any{"status": "ahead", "ahead_by": 1},
	}.start(t)

	if got := runScript(t, t.TempDir(), srv.URL, ""); got != unknownBuildValue {
		t.Fatalf("release = %q, want %q", got, unknownBuildValue)
	}
}

// An explicit stamp is authoritative in every environment, including one with
// no repository at all.
func TestDescribeReleaseExplicitStampWinsWithoutRepository(t *testing.T) {
	dir := t.TempDir()
	got := runDescribe(t, dir, "BUILD_RELEASE=v9.9.9", "RELEASE_GITHUB_API=http://127.0.0.1:1")
	if got != "v9.9.9" {
		t.Fatalf("release = %q, want v9.9.9", got)
	}
}

// runScript invokes the script with a commit argument and a stub API, failing
// the test if the script exits non-zero (a build must never break on this).
func runScript(t *testing.T, dir, api, commit string) string {
	t.Helper()
	out, err := runDescribeWithCommit(t, dir, api, commit)
	if err != nil {
		t.Fatalf("script failed: %v", err)
	}
	return strings.TrimSpace(out)
}

// runDescribeWithCommit runs the script with an explicit commit argument, so a
// directory with no repository can still exercise the API path.
func runDescribeWithCommit(t *testing.T, dir, api, commit string) (string, error) {
	t.Helper()
	script, err := filepath.Abs(describeReleaseScript)
	if err != nil {
		return "", err
	}
	args := []string{script}
	if commit != "" {
		args = append(args, commit)
	}
	cmd := exec.Command("sh", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(),
		"RELEASE_GITHUB_API="+api,
		"RELEASE_REPO="+stubRepo,
		"BUILD_RELEASE=",
	)
	out, err := cmd.CombinedOutput()
	return string(out), err
}
