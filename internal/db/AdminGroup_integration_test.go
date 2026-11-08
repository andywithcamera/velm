package db

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"
)

// TestAdministratorsGroupIsTheAdminGrant proves the property #199 R1 depends on:
// membership in the Administrators group alone confers global admin authority.
// It also proves the group is idempotent, and that app seed cleanup cannot
// delete it.
//
// Gated on TEST_DATABASE_URL like the other real-Postgres tests.
func TestAdministratorsGroupIsTheAdminGrant(t *testing.T) {
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Skip("TEST_DATABASE_URL not set; skipping real-Postgres integration test")
	}

	t.Setenv("DATABASE_URL", url)
	os.Setenv("DATABASE_URL", url)
	if err := ConnectToDB(); err != nil {
		t.Fatalf("ConnectToDB: %v", err)
	}
	t.Cleanup(CloseDB)

	ctx := context.Background()
	if err := RunMigrations(ctx); err != nil {
		t.Fatalf("RunMigrations: %v", err)
	}

	suffix := fmt.Sprintf("%s-%d", t.Name(), time.Now().UnixNano())
	email := suffix + "@example.test"

	var userID string
	if err := Pool.QueryRow(ctx, `
		INSERT INTO _user (name, email, password_hash)
		VALUES ($1, $2, '$2a$10$N9qo8uLOickgx2ZMRZoMyeIjZAgcfl7p92ldGxad68LJZdL17lhWy')
		RETURNING _id::text
	`, "admingroup-integration", email).Scan(&userID); err != nil {
		t.Fatalf("seed _user: %v", err)
	}

	t.Cleanup(func() {
		bg := context.Background()
		if _, err := Pool.Exec(bg, `DELETE FROM _group_membership WHERE user_id = $1`, userID); err != nil {
			t.Logf("cleanup: delete membership for %s: %v", userID, err)
		}
		if _, err := Pool.Exec(bg, `DELETE FROM _user WHERE _id = $1`, userID); err != nil {
			t.Logf("cleanup: delete user %s: %v", userID, err)
		}
	})

	// 1. Idempotent: two calls, one group, one global admin role mapping.
	if err := EnsureAdministratorsGroup(ctx); err != nil {
		t.Fatalf("EnsureAdministratorsGroup: %v", err)
	}
	if err := EnsureAdministratorsGroup(ctx); err != nil {
		t.Fatalf("EnsureAdministratorsGroup (second call): %v", err)
	}

	var groupCount int
	if err := Pool.QueryRow(ctx, `SELECT COUNT(*) FROM _group WHERE name = $1`, AdministratorsGroupName).Scan(&groupCount); err != nil {
		t.Fatalf("count groups: %v", err)
	}
	if groupCount != 1 {
		t.Fatalf("%s group count = %d, want 1", AdministratorsGroupName, groupCount)
	}

	var roleMappingCount int
	if err := Pool.QueryRow(ctx, `
		SELECT COUNT(*)
		FROM _group_role gr
		JOIN _group g ON g._id = gr.group_id
		JOIN _role r ON r._id = gr.role_id
		WHERE g.name = $1 AND r.name = 'admin' AND gr.app_id = ''
	`, AdministratorsGroupName).Scan(&roleMappingCount); err != nil {
		t.Fatalf("count group roles: %v", err)
	}
	if roleMappingCount != 1 {
		t.Fatalf("global admin role mappings on %s = %d, want 1", AdministratorsGroupName, roleMappingCount)
	}

	// 2. Membership, not a direct grant, is what confers admin.
	tx, err := Pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	if err := AddUserToAdministratorsGroupTx(ctx, tx, userID); err != nil {
		_ = tx.Rollback(ctx)
		t.Fatalf("AddUserToAdministratorsGroupTx: %v", err)
	}
	if err := AddUserToAdministratorsGroupTx(ctx, tx, userID); err != nil {
		_ = tx.Rollback(ctx)
		t.Fatalf("AddUserToAdministratorsGroupTx (second call): %v", err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("commit: %v", err)
	}

	var membershipCount int
	if err := Pool.QueryRow(ctx, `SELECT COUNT(*) FROM _group_membership WHERE user_id = $1`, userID).Scan(&membershipCount); err != nil {
		t.Fatalf("count memberships: %v", err)
	}
	if membershipCount != 1 {
		t.Fatalf("membership count for %s = %d, want 1", userID, membershipCount)
	}

	roles, err := ListEffectiveRoleNames(ctx, userID, "")
	if err != nil {
		t.Fatalf("ListEffectiveRoleNames: %v", err)
	}
	got := map[string]bool{}
	for _, name := range roles {
		got[name] = true
	}
	for _, want := range []string{"admin", "operator", "viewer"} {
		if !got[want] {
			t.Fatalf("effective roles = %v, missing %q (inheritance admin->operator->viewer)", roles, want)
		}
	}

	// 3. App seed cleanup must not delete the out-of-the-box admin group, even
	//    when the seed references it by name — is_system is the guard.
	definition := &AppDefinition{
		Seeds: []AppDefinitionSeed{
			{
				Table: "_group_membership",
				Rows: []map[string]any{
					{"group_id": "@group:" + AdministratorsGroupName},
				},
			},
		},
	}

	cleanupTx, err := Pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin seed cleanup: %v", err)
	}
	if err := deleteProvisionedSeedGroupsTx(ctx, cleanupTx, definition); err != nil {
		_ = cleanupTx.Rollback(ctx)
		t.Fatalf("deleteProvisionedSeedGroupsTx: %v", err)
	}
	if err := cleanupTx.Commit(ctx); err != nil {
		t.Fatalf("commit seed cleanup: %v", err)
	}

	var survivors int
	if err := Pool.QueryRow(ctx, `SELECT COUNT(*) FROM _group WHERE name = $1 AND is_system = TRUE`, AdministratorsGroupName).Scan(&survivors); err != nil {
		t.Fatalf("count surviving group: %v", err)
	}
	if survivors != 1 {
		t.Fatalf("%s survived seed cleanup as a system group = %d, want 1", AdministratorsGroupName, survivors)
	}
}
