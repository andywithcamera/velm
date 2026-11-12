package db

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"
)

// Issue #216: Administrators carried admin with a non-empty app_id, so the
// grant resolved to nothing in the global-admin queries and members of the
// group had no permissions at all.

// TestAdministratorsGroupReconcilesAppScopedAdminGrant proves the repair half:
// a legacy app-scoped admin grant on Administrators is removed and the global
// grant survives when the group is ensured again.
func TestAdministratorsGroupReconcilesAppScopedAdminGrant(t *testing.T) {
	ctx := scopeTestContext(t)

	if err := EnsureAdministratorsGroup(ctx); err != nil {
		t.Fatalf("EnsureAdministratorsGroup: %v", err)
	}

	var groupID, roleID string
	if err := Pool.QueryRow(ctx, `SELECT _id::text FROM _group WHERE name = $1`, AdministratorsGroupName).Scan(&groupID); err != nil {
		t.Fatalf("look up group: %v", err)
	}
	if err := Pool.QueryRow(ctx, `SELECT _id::text FROM _role WHERE name = 'admin'`).Scan(&roleID); err != nil {
		t.Fatalf("look up admin role: %v", err)
	}

	// Reproduce the legacy state. The guard trigger (092) is what now prevents
	// this, so it is disabled for the insert only: the row represents data
	// written before the guard existed, which the reconciliation must clear.
	if _, err := Pool.Exec(ctx, `ALTER TABLE _group_role DISABLE TRIGGER _group_role_system_scope_guard`); err != nil {
		t.Fatalf("disable scope guard: %v", err)
	}
	_, insertErr := Pool.Exec(ctx, `
		INSERT INTO _group_role (group_id, role_id, app_id)
		VALUES ($1, $2, $3)
		ON CONFLICT (group_id, role_id, app_id) DO NOTHING
	`, groupID, roleID, fmt.Sprintf("legacy-app-%d", time.Now().UnixNano()))
	if _, err := Pool.Exec(ctx, `ALTER TABLE _group_role ENABLE TRIGGER _group_role_system_scope_guard`); err != nil {
		t.Fatalf("re-enable scope guard: %v", err)
	}
	if insertErr != nil {
		t.Fatalf("seed legacy app-scoped grant: %v", insertErr)
	}

	if err := EnsureAdministratorsGroup(ctx); err != nil {
		t.Fatalf("EnsureAdministratorsGroup (repair): %v", err)
	}

	var scoped, global int
	if err := Pool.QueryRow(ctx, `
		SELECT COUNT(*) FILTER (WHERE app_id <> ''), COUNT(*) FILTER (WHERE app_id = '')
		FROM _group_role WHERE group_id = $1 AND role_id = $2
	`, groupID, roleID).Scan(&scoped, &global); err != nil {
		t.Fatalf("count admin grants: %v", err)
	}
	if scoped != 0 {
		t.Errorf("app-scoped admin grants on %s = %d, want 0", AdministratorsGroupName, scoped)
	}
	if global != 1 {
		t.Errorf("global admin grants on %s = %d, want 1", AdministratorsGroupName, global)
	}
}

// TestSystemGroupRejectsAppScopedGrant proves the prevention half: the database
// refuses to create the state #216 was found in, whatever write path tries,
// while an ordinary group can still hold an app-scoped grant.
func TestSystemGroupRejectsAppScopedGrant(t *testing.T) {
	ctx := scopeTestContext(t)

	if err := EnsureAdministratorsGroup(ctx); err != nil {
		t.Fatalf("EnsureAdministratorsGroup: %v", err)
	}

	var groupID, roleID string
	if err := Pool.QueryRow(ctx, `SELECT _id::text FROM _group WHERE name = $1`, AdministratorsGroupName).Scan(&groupID); err != nil {
		t.Fatalf("look up group: %v", err)
	}
	if err := Pool.QueryRow(ctx, `SELECT _id::text FROM _role WHERE name = 'admin'`).Scan(&roleID); err != nil {
		t.Fatalf("look up admin role: %v", err)
	}

	_, err := Pool.Exec(ctx, `
		INSERT INTO _group_role (group_id, role_id, app_id) VALUES ($1, $2, 'scoped-app')
	`, groupID, roleID)
	if err == nil {
		_, _ = Pool.Exec(ctx, `DELETE FROM _group_role WHERE group_id = $1 AND role_id = $2 AND app_id = 'scoped-app'`, groupID, roleID)
		t.Fatalf("app-scoped admin grant on %s was accepted, want rejection", AdministratorsGroupName)
	}
	if !strings.Contains(err.Error(), "app-scoped role grant is not allowed on a system group") {
		t.Fatalf("unexpected error: %v", err)
	}

	// A non-system group keeps the scoped form: it is how app roles are granted.
	suffix := fmt.Sprintf("scope-test-%d", time.Now().UnixNano())
	var plainGroupID string
	if err := Pool.QueryRow(ctx, `
		INSERT INTO _group (name, description, is_system) VALUES ($1, 'temporary', FALSE) RETURNING _id::text
	`, suffix).Scan(&plainGroupID); err != nil {
		t.Fatalf("seed plain group: %v", err)
	}
	t.Cleanup(func() {
		_, _ = Pool.Exec(context.Background(), `DELETE FROM _group WHERE _id = $1`, plainGroupID)
	})
	if _, err := Pool.Exec(ctx, `
		INSERT INTO _group_role (group_id, role_id, app_id) VALUES ($1, $2, 'scoped-app')
	`, plainGroupID, roleID); err != nil {
		t.Fatalf("app-scoped grant on a non-system group was rejected: %v", err)
	}
}

func scopeTestContext(t *testing.T) context.Context {
	t.Helper()
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
	return ctx
}
