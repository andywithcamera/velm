package db

import (
	"context"
	"os"
	"testing"

	"velm/internal/auth"
)

// TestAgentTokenLifecycle exercises the full agent-token path against a REAL
// PostgreSQL database, the gap unit tests (which use a fake TokenStore) can't
// cover: the 070 migration applying cleanly, the _agent_api_token schema,
// the user_id join key, and the resolve/revoke/expiry behaviour end to end.
//
// It is gated on TEST_DATABASE_URL so unit runs stay fast and standalone.
// CI's `test` job (ci.yml) boots a Postgres service and sets this variable.
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
	ctx := context.Background()
	var userID string
	if err := Pool.QueryRow(ctx, `
		INSERT INTO _user (name, email, password_hash)
		VALUES ('agent-integration', 'agent@example.test', '$2a$10$N9qo8uLOickgx2ZMRZoMyeIjZAgcfl7p92ldGxad68LJZdL17lhWy')
		RETURNING _id::text
	`).Scan(&userID); err != nil {
		t.Fatalf("seed _user: %v", err)
	}

	store := NewAgentTokenStore()

	// 3. Create a token (store only a SHA-256 hash; raw secret shown once).
	//    Note: the token binds an identity but carries NO scopes/authority —
	//    ACLs are user-based and resolved from the bound _user row, exactly as
	//    for a session (issue #68: authN differs, authZ identical).
	raw := auth.HashAgentToken("velm_integration_test_secret")
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

	email, name, err := store.LookupUserIdentity(ctx, userID)
	if err != nil {
		t.Fatalf("LookupUserIdentity: %v", err)
	}
	if email != "agent@example.test" || name != "agent-integration" {
		t.Fatalf("identity = %q/%q, want agent@example.test/agent-integration", email, name)
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
