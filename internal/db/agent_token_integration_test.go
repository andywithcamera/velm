package db

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"velm/internal/auth"
)

// TestAgentTokenLifecycle exercises the full agent-token path against a REAL
// PostgreSQL database, the gap unit tests (which use a fake TokenStore) can't
// cover: the 070 migration applying cleanly, the _agent_api_token schema,
// the user_id join key, and the resolve/revoke/expiry behaviour end to end.
//
// It is gated on TEST_DATABASE_URL so unit runs stay fast and standalone.
// CI's `test` job (ci.yml) boots a Postgres service and sets this variable.
//
// Every row this test creates is unique to the run and removed again in
// t.Cleanup, so the suite is re-runnable against a database it already used
// (issue #177: a fixed token hash tripped _agent_api_token_token_hash_key on
// the second run). A test that only passes on a virgin database is not
// evidence of anything on the database a developer is actually working with.
func TestAgentTokenLifecycle(t *testing.T) {
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Skip("TEST_DATABASE_URL not set; skipping real-Postgres integration test")
	}

	// Switch the package-global pool to the test database. The db package uses
	// a single global Pool via DATABASE_URL; reuse ConnectToDB/CloseDB.
	t.Setenv("DATABASE_URL", url)
	os.Setenv("DATABASE_URL", url)
	if err := ConnectToDB(); err != nil {
		t.Fatalf("ConnectToDB: %v", err)
	}
	t.Cleanup(CloseDB)

	// 1. Apply the full migration set, including 070_agent_api_token.sql.
	if err := RunMigrations(context.Background()); err != nil {
		t.Fatalf("RunMigrations (should include 070): %v", err)
	}

	// 2. Seed a real _user row — agents are indistinguishable _user identities.
	//    The email carries the run suffix so a second run does not collide with
	//    a row the first one left behind (or, worse, silently test the stale one).
	ctx := context.Background()
	suffix := fmt.Sprintf("%s-%d", t.Name(), time.Now().UnixNano())
	email := suffix + "@example.test"

	var userID string
	if err := Pool.QueryRow(ctx, `
		INSERT INTO _user (name, email, password_hash)
		VALUES ($1, $2, '$2a$10$N9qo8uLOickgx2ZMRZoMyeIjZAgcfl7p92ldGxad68LJZdL17lhWy')
		RETURNING _id::text
	`, "agent-integration", email).Scan(&userID); err != nil {
		t.Fatalf("seed _user: %v", err)
	}

	// Remove what this run created. Registered after CloseDB, so LIFO ordering
	// runs it first, while the pool is still open. Tokens go by user_id to
	// cover every row the run could have created, not just the one we know by
	// name. Failures are logged, not fatal: a cleanup error must not turn a
	// passing test red, but it must be visible enough to explain the next run.
	t.Cleanup(func() {
		if _, err := Pool.Exec(context.Background(), `DELETE FROM _agent_api_token WHERE user_id = $1`, userID); err != nil {
			t.Logf("cleanup: delete agent tokens for %s: %v", userID, err)
		}
		if _, err := Pool.Exec(context.Background(), `DELETE FROM _user WHERE _id = $1`, userID); err != nil {
			t.Logf("cleanup: delete user %s: %v", userID, err)
		}
	})

	store := NewAgentTokenStore()

	// 3. Create a token (store only a SHA-256 hash; raw secret shown once).
	//    Note: the token binds an identity but carries NO scopes/authority —
	//    ACLs are user-based and resolved from the bound _user row, exactly as
	//    for a session (issue #68: authN differs, authZ identical).
	raw := auth.HashAgentToken("velm_integration_test_secret_" + suffix)
	if err := store.CreateAgentToken(ctx, userID, raw, "integration-test"); err != nil {
		t.Fatalf("CreateAgentToken: %v", err)
	}

	// 4. Resolve it back and verify the identity is the same real _user row.
	tok, err := store.LookupAgentToken(ctx, raw)
	if err != nil {
		t.Fatalf("LookupAgentToken: %v", err)
	}
	if tok.UserID != userID {
		t.Fatalf("token user_id = %q, want %q", tok.UserID, userID)
	}
	if tok.Revoked {
		t.Fatal("freshly created token should not be revoked")
	}

	gotEmail, name, err := store.LookupUserIdentity(ctx, userID)
	if err != nil {
		t.Fatalf("LookupUserIdentity: %v", err)
	}
	if gotEmail != email || name != "agent-integration" {
		t.Fatalf("identity = %q/%q, want %s/agent-integration", gotEmail, name, email)
	}

	// 5. Unknown hash -> ErrNoAgentToken (not a raw DB error).
	unknown := auth.HashAgentToken("velm_does_not_exist")
	if _, err := store.LookupAgentToken(ctx, unknown); err != auth.ErrNoAgentToken {
		t.Fatalf("unknown hash error = %v, want auth.ErrNoAgentToken", err)
	}

	// 6. TouchAgentToken updates last_used_at.
	if err := store.TouchAgentToken(ctx, tok.ID); err != nil {
		t.Fatalf("TouchAgentToken: %v", err)
	}
}
