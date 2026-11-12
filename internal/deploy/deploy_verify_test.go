package deploy

import (
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// verifyScript is what the post-deploy workflow runs. It is exercised here
// because a guard that cannot tell "serving the merged commit" from "serving
// the previous one" is worse than no guard: issue #191 was a deploy that was
// silently skipped while GitHub was told the deployment succeeded.
const verifyScript = "../../scripts/verify-deploy.sh"

// runVerify executes the script against a stub /version endpoint.
func runVerify(t *testing.T, served, expected string) (string, error) {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"commit":"` + served + `","release":"unknown","built":"2026-11-12T00:00:00Z","schema":"091","go":"go1.26"}` + "\n"))
	}))
	defer server.Close()

	script, err := filepath.Abs(verifyScript)
	if err != nil {
		t.Fatalf("resolve script: %v", err)
	}
	cmd := exec.Command("sh", script, expected)
	cmd.Env = append(os.Environ(),
		"VELM_VERSION_URL="+server.URL,
		"VERIFY_ATTEMPTS=2",
		"VERIFY_INTERVAL=0",
	)
	out, err := cmd.CombinedOutput()
	return strings.TrimSpace(string(out)), err
}

// A skipped deployment (the #191 case) leaves production on the previous commit
// while the platform reports success. That must be an error, not a silent pass.
func TestVerifyDeployFailsWhenProductionServesOldCommit(t *testing.T) {
	out, err := runVerify(t, "39b350b1111111111111111111111111111111111", "d58ddf4222222222222222222222222222222222")
	if err == nil {
		t.Fatalf("verify passed while production served the wrong commit; output:\n%s", out)
	}
	for _, want := range []string{"39b350b", "d58ddf4"} {
		if !strings.Contains(out, want) {
			t.Errorf("failure message does not name %q; output:\n%s", want, out)
		}
	}
}

func TestVerifyDeployFailsWhenCommitIsUnknown(t *testing.T) {
	if out, err := runVerify(t, "unknown", "d58ddf4222222222222222222222222222222222"); err == nil {
		t.Fatalf("verify passed while /version reported unknown; output:\n%s", out)
	}
}

func TestVerifyDeployPassesOnExactCommit(t *testing.T) {
	sha := "d58ddf4222222222222222222222222222222222"
	out, err := runVerify(t, sha, sha)
	if err != nil {
		t.Fatalf("verify failed on matching commit: %v\n%s", err, out)
	}
	if !strings.Contains(out, "verified") {
		t.Errorf("success output should say the deploy was verified; got:\n%s", out)
	}
}

// Railway reports the commit as the full SHA in some paths and as a short SHA
// in others; a prefix match of at least 7 characters is the same commit.
func TestVerifyDeployAcceptsShortSHA(t *testing.T) {
	if out, err := runVerify(t, "d58ddf4", "d58ddf4222222222222222222222222222222222"); err != nil {
		t.Fatalf("verify rejected a short sha of the deployed commit: %v\n%s", err, out)
	}
}
