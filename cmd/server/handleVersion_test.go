package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"runtime"
	"testing"
	"time"

	"velm/internal/db"
)

// resetSchemaVersionCache clears the short-lived cache so a test observes a
// fresh lookup rather than whatever an earlier test cached.
func resetSchemaVersionCache(t *testing.T) {
	t.Helper()
	schemaVersionCache.mu.Lock()
	defer schemaVersionCache.mu.Unlock()
	schemaVersionCache.cachedAt = time.Time{}
	schemaVersionCache.value = ""
}

func setBuildMetadata(t *testing.T, commit, built string) {
	t.Helper()
	previousCommit, previousTime := buildCommit, buildTime
	buildCommit, buildTime = commit, built
	t.Cleanup(func() {
		buildCommit, buildTime = previousCommit, previousTime
	})
}

func setBuildRelease(t *testing.T, release string) {
	t.Helper()
	previous := buildRelease
	buildRelease = release
	t.Cleanup(func() { buildRelease = previous })
}

func TestHandleVersionReportsStampedBuild(t *testing.T) {
	resetSchemaVersionCache(t)
	if db.Pool != nil {
		t.Skip("a database is connected; the no-database schema assertion does not apply")
	}
	setBuildMetadata(t, "1295514e7e6ef660cb22c065634845ca5900da30", "2026-11-04T22:18:52Z")
	t.Setenv("RAILWAY_GIT_COMMIT_SHA", "")

	req := httptest.NewRequest(http.MethodGet, "/version", nil)
	rec := httptest.NewRecorder()

	handleVersion(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	if got := rec.Header().Get("Cache-Control"); got != "no-store" {
		t.Fatalf("cache control = %q, want no-store", got)
	}

	var got versionInfo
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal body: %v", err)
	}
	if got.Commit != "1295514e7e6ef660cb22c065634845ca5900da30" {
		t.Fatalf("commit = %q", got.Commit)
	}
	if got.Release != unknownBuildValue {
		t.Fatalf("release = %q, want %q when unstamped", got.Release, unknownBuildValue)
	}
	if got.Built != "2026-11-04T22:18:52Z" {
		t.Fatalf("built = %q", got.Built)
	}
	if got.Go != runtime.Version() {
		t.Fatalf("go = %q, want %q", got.Go, runtime.Version())
	}
	if got.Schema != unknownBuildValue {
		t.Fatalf("schema = %q, want %q without a database", got.Schema, unknownBuildValue)
	}
}

func TestHandleVersionFallsBackToPlatformCommit(t *testing.T) {
	setBuildMetadata(t, "", "")
	t.Setenv("RAILWAY_GIT_COMMIT_SHA", "4df52b3aa1b2c3d4e5f60718293a4b5c6d7e8f90")

	got := currentVersionInfo()

	if got.Commit != "4df52b3aa1b2c3d4e5f60718293a4b5c6d7e8f90" {
		t.Fatalf("commit = %q, want the Railway commit", got.Commit)
	}
	if got.Built != unknownBuildValue {
		t.Fatalf("built = %q, want %q", got.Built, unknownBuildValue)
	}
}

func TestHandleVersionStampedCommitBeatsPlatformCommit(t *testing.T) {
	setBuildMetadata(t, "1295514", "2026-11-04T22:18:52Z")
	t.Setenv("RAILWAY_GIT_COMMIT_SHA", "stale-platform-value")

	if got := currentVersionInfo().Commit; got != "1295514" {
		t.Fatalf("commit = %q, want the linked stamp", got)
	}
}

func TestHandleVersionReportsUnknownWhenUnstamped(t *testing.T) {
	setBuildMetadata(t, "", "")
	t.Setenv("RAILWAY_GIT_COMMIT_SHA", "")

	req := httptest.NewRequest(http.MethodGet, "/version", nil)
	rec := httptest.NewRecorder()

	handleVersion(rec, req)

	var got versionInfo
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal body: %v", err)
	}
	if got.Commit != unknownBuildValue || got.Built != unknownBuildValue {
		t.Fatalf("unstamped build = %+v, want both %q", got, unknownBuildValue)
	}
}

func TestHandleVersionAllowsHeadWithoutBody(t *testing.T) {
	setBuildMetadata(t, "1295514", "2026-11-04T22:18:52Z")

	req := httptest.NewRequest(http.MethodHead, "/version", nil)
	rec := httptest.NewRecorder()

	handleVersion(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	if rec.Body.Len() != 0 {
		t.Fatalf("body = %q, want empty", rec.Body.String())
	}
}

func TestHandleVersionRejectsNonGet(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/version", nil)
	rec := httptest.NewRecorder()

	handleVersion(rec, req)

	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusMethodNotAllowed)
	}
}

func TestHandleVersionReportsReleaseTag(t *testing.T) {
	setBuildMetadata(t, "5497507", "2026-11-05T07:05:00Z")
	setBuildRelease(t, "v0.1.0")
	t.Setenv("BUILD_RELEASE", "")

	got := currentVersionInfo()

	if got.Release != "v0.1.0" {
		t.Fatalf("release = %q, want v0.1.0", got.Release)
	}
	if got.Commit != "5497507" {
		t.Fatalf("commit = %q, want the linked stamp", got.Commit)
	}
}

func TestHandleVersionReleaseIsIndependentOfCommit(t *testing.T) {
	// A build between tags is a real, reportable state: commit known,
	// release name derived by git describe as v0.1.0-N-gSHA. It must not
	// collapse to unknown just because the commit is not a tag tip.
	setBuildMetadata(t, "abc1234", "2026-11-05T07:05:00Z")
	setBuildRelease(t, "v0.1.0-3-gabc1234")
	t.Setenv("BUILD_RELEASE", "")

	got := currentVersionInfo()

	if got.Release != "v0.1.0-3-gabc1234" {
		t.Fatalf("release = %q, want the describe output", got.Release)
	}
	if got.Commit != "abc1234" {
		t.Fatalf("commit = %q", got.Commit)
	}
}

func TestHandleVersionReleaseFallsBackToEnv(t *testing.T) {
	setBuildMetadata(t, "", "")
	setBuildRelease(t, "")
	t.Setenv("BUILD_RELEASE", "v0.1.0")

	if got := currentVersionInfo().Release; got != "v0.1.0" {
		t.Fatalf("release = %q, want the env fallback", got)
	}
}
