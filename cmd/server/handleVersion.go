package main

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"runtime"
	"strings"
	"sync"
	"time"

	"velm/internal/db"
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
	Schema  string `json:"schema"`
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
		Schema:  appliedSchemaVersion(),
		Go:      runtime.Version(),
	}
}

// appliedSchemaVersion names the highest migration the database has applied.
// It answers the other half of "what is running": the commit says which code,
// this says which schema that code is actually talking to. A database that
// failed mid-migration, or one restored from an older backup, shows up here as
// a schema older than the release rather than as a mystery at request time.
//
// Failures read "unknown" like the other build fields do, never as a plausible
// version.
//
// The result is cached briefly because this endpoint takes no authentication:
// an uptime poller is welcome to it, a flood of anonymous callers is not
// welcome to a database query each. The schema only changes on a boot or a
// migration, so a few seconds of staleness costs nothing.
const schemaVersionCacheTTL = 10 * time.Second

var schemaVersionCache struct {
	mu       sync.Mutex
	cachedAt time.Time
	value    string
}

func appliedSchemaVersion() string {
	schemaVersionCache.mu.Lock()
	defer schemaVersionCache.mu.Unlock()

	if !schemaVersionCache.cachedAt.IsZero() && time.Since(schemaVersionCache.cachedAt) < schemaVersionCacheTTL {
		return schemaVersionCache.value
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	value := unknownBuildValue
	if version, err := db.AppliedSchemaVersion(ctx); err == nil {
		value = buildValueOrUnknown(version)
	}

	schemaVersionCache.value = value
	schemaVersionCache.cachedAt = time.Now()
	return value
}

// handleVersion reports what is actually running: the commit and release, and
// the schema version the database reports. Unauthenticated by design: it
// exposes nothing beyond identifiers already public in the repository — no
// data, no configuration, no migration contents — and it exists so a deploy
// can be verified from outside with a single request.
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
