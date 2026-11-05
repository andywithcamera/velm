package main

import (
	"encoding/json"
	"net/http"
	"os"
	"runtime"
	"strings"
)

// Build metadata, injected at link time:
//
//	go build -ldflags "-X main.buildCommit=$(git rev-parse HEAD) \
//	  -X main.buildRelease=$(git describe --tags --always) \
//	  -X main.buildTime=$(date -u +%FT%TZ)" ./cmd/server
//
// Stamped values win. Unstamped builds fall back to the platform's commit
// variable (Railway sets RAILWAY_GIT_COMMIT_SHA at runtime), and finally to
// "unknown" — so a missing stamp never reads as a plausible commit.
var (
	buildCommit  = ""
	buildTime    = ""
	buildRelease = ""
)

const unknownBuildValue = "unknown"

type versionInfo struct {
	Commit  string `json:"commit"`
	Release string `json:"release"`
	Built   string `json:"built"`
	Go      string `json:"go"`
}

// buildValueOrUnknown trims a candidate build field, reporting "unknown" when
// it carries nothing usable.
func buildValueOrUnknown(value string) string {
	if trimmed := strings.TrimSpace(value); trimmed != "" {
		return trimmed
	}
	return unknownBuildValue
}

// currentVersionInfo resolves the running build identity. Runtime env is read
// only as a fallback: a linked -X value is the authoritative stamp, because it
// describes the artifact rather than the environment it happens to boot in.
func currentVersionInfo() versionInfo {
	return versionInfo{
		Commit:  buildValueOrUnknown(firstNonEmpty(buildCommit, os.Getenv("RAILWAY_GIT_COMMIT_SHA"))),
		Release: buildValueOrUnknown(firstNonEmpty(buildRelease, os.Getenv("BUILD_RELEASE"))),
		Built:   buildValueOrUnknown(firstNonEmpty(buildTime, os.Getenv("RAILWAY_GIT_DEPLOYED_AT"))),
		Go:      runtime.Version(),
	}
}

// handleVersion reports what is actually running. Unauthenticated by design:
// it exposes nothing but the commit already public in the repository, and it
// exists so a deploy can be verified from outside with a single request.
func handleVersion(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	payload, err := json.Marshal(currentVersionInfo())
	if err != nil {
		http.Error(w, "Failed to render version", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusOK)
	if r.Method == http.MethodHead {
		return
	}
	_, _ = w.Write(append(payload, '\n'))
}
