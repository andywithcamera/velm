package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// describeReleaseScript is the Dockerfile's release stamp. It is exercised here
// rather than trusted, because the value it prints is what /version reports as
// the release and nobody can tell a wrong tag from a right one by eye.
const describeReleaseScript = "../../scripts/describe-release.sh"

func requireGit(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
}

func runGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	full := append([]string{"-c", "user.email=test@example.invalid", "-c", "user.name=Test", "-c", "commit.gpgsign=false", "-c", "init.defaultBranch=main"}, args...)
	cmd := exec.Command("git", full...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

// writeFile commits one file into the repo so each call is a distinct commit.
func writeFile(t *testing.T, dir, name, body string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
		t.Fatalf("write %s: %v", name, err)
	}
}

func newRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	runGit(t, dir, "init", "-q")
	return dir
}

// runDescribe executes the script inside dir and returns its single output line.
func runDescribe(t *testing.T, dir string, env ...string) string {
	t.Helper()
	script, err := filepath.Abs(describeReleaseScript)
	if err != nil {
		t.Fatalf("resolve script: %v", err)
	}
	cmd := exec.Command("sh", script)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), env...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("script: %v\n%s", err, out)
	}
	return strings.TrimSpace(string(out))
}

func TestDescribeReleaseUsesTagOnTaggedCommit(t *testing.T) {
	requireGit(t)
	dir := newRepo(t)
	writeFile(t, dir, "a", "1")
	runGit(t, dir, "add", ".")
	runGit(t, dir, "commit", "-qm", "first")
	runGit(t, dir, "tag", "-a", "v0.1.0", "-m", "release")

	if got := runDescribe(t, dir); got != "v0.1.0" {
		t.Fatalf("release = %q, want v0.1.0", got)
	}
}

func TestDescribeReleaseCountsCommitsAfterTag(t *testing.T) {
	requireGit(t)
	dir := newRepo(t)
	writeFile(t, dir, "a", "1")
	runGit(t, dir, "add", ".")
	runGit(t, dir, "commit", "-qm", "first")
	runGit(t, dir, "tag", "-a", "v0.1.0", "-m", "release")
	writeFile(t, dir, "b", "2")
	runGit(t, dir, "add", ".")
	runGit(t, dir, "commit", "-qm", "second")

	sha := runGit(t, dir, "rev-parse", "--short=7", "HEAD")
	want := "v0.1.0-1-g" + sha
	if got := runDescribe(t, dir); got != want {
		t.Fatalf("release = %q, want %q", got, want)
	}
}

// A tagless history must not invent a release: it falls back to the sha.
func TestDescribeReleaseFallsBackToShaWithoutTags(t *testing.T) {
	requireGit(t)
	dir := newRepo(t)
	writeFile(t, dir, "a", "1")
	runGit(t, dir, "add", ".")
	runGit(t, dir, "commit", "-qm", "first")

	want := runGit(t, dir, "rev-parse", "--short=7", "HEAD")
	got := runDescribe(t, dir)
	if got != want {
		t.Fatalf("release = %q, want short sha %q", got, want)
	}
	if got == unknownBuildValue {
		t.Fatal("a repository with history must not report unknown")
	}
}

// The production case: Railway clones shallow, where the tag is not in the
// history the clone carries. The script deepens the clone, so the tag survives.
func TestDescribeReleaseRecoversTagFromShallowClone(t *testing.T) {
	requireGit(t)
	src := newRepo(t)
	writeFile(t, src, "a", "1")
	runGit(t, src, "add", ".")
	runGit(t, src, "commit", "-qm", "first")
	runGit(t, src, "tag", "-a", "v0.1.0", "-m", "release")
	writeFile(t, src, "b", "2")
	runGit(t, src, "add", ".")
	runGit(t, src, "commit", "-qm", "second")

	parent := t.TempDir()
	clone := filepath.Join(parent, "clone")
	runGit(t, parent, "clone", "-q", "--depth", "1", "file://"+src, clone)

	if _, err := os.Stat(filepath.Join(clone, ".git", "shallow")); err != nil {
		t.Fatalf("clone is not shallow, test would prove nothing: %v", err)
	}
	want := "v0.1.0-1-g" + runGit(t, clone, "rev-parse", "--short=7", "HEAD")
	if got := runDescribe(t, clone); got != want {
		t.Fatalf("release = %q, want %q (shallow clone must be deepened)", got, want)
	}
}

func TestDescribeReleaseReportsUnknownOutsideRepository(t *testing.T) {
	requireGit(t)
	if got := runDescribe(t, t.TempDir()); got != unknownBuildValue {
		t.Fatalf("release = %q, want %q", got, unknownBuildValue)
	}
}

// An explicit stamp is authoritative even when git would say something else:
// that is how a pipeline overrides a build context it does not control.
func TestDescribeReleasePrefersExplicitStamp(t *testing.T) {
	requireGit(t)
	dir := newRepo(t)
	writeFile(t, dir, "a", "1")
	runGit(t, dir, "add", ".")
	runGit(t, dir, "commit", "-qm", "first")

	if got := runDescribe(t, dir, "BUILD_RELEASE=v9.9.9"); got != "v9.9.9" {
		t.Fatalf("release = %q, want v9.9.9", got)
	}
}

// The script must never print a value that looks like a version but is not
// derived from anything. A dirty tree is the one case it may embellish, and
// only with the suffix git itself uses.
func TestDescribeReleaseMarksDirtyTree(t *testing.T) {
	requireGit(t)
	dir := newRepo(t)
	writeFile(t, dir, "a", "1")
	runGit(t, dir, "add", ".")
	runGit(t, dir, "commit", "-qm", "first")
	runGit(t, dir, "tag", "-a", "v0.1.0", "-m", "release")
	writeFile(t, dir, "a", "changed")

	got := runDescribe(t, dir)
	if got != "v0.1.0-dirty" {
		t.Fatalf("release = %q, want v0.1.0-dirty", got)
	}
}
