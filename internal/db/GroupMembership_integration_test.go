package db

import (
	"context"
	"errors"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
)

// TestGroupMembershipGrantsAndRevokesRoles covers the read/write pair the
// access-control page uses after #199 R3, and the guard that has to survive
// the move from direct grants to membership.
//
// Own scratch database for the same reason as TestLastGlobalAdminGuard: the
// last-admin invariant is absolute, so it is only testable where the test
// controls every admin.
func TestGroupMembershipGrantsAndRevokesRoles(t *testing.T) {
	baseURL := os.Getenv("TEST_DATABASE_URL")
	if baseURL == "" {
		t.Skip("TEST_DATABASE_URL not set; skipping real-Postgres integration test")
	}

	ctx := context.Background()
	dbName := fmt.Sprintf("velm_membership_%d", time.Now().UnixNano())

	admin, err := pgx.Connect(ctx, baseURL)
	if err != nil {
		t.Fatalf("connect to TEST_DATABASE_URL: %v", err)
	}
	if _, err := admin.Exec(ctx, "CREATE DATABASE "+pgx.Identifier{dbName}.Sanitize()); err != nil {
		_ = admin.Close(ctx)
		t.Fatalf("create scratch database (does the TEST_DATABASE_URL user have CREATEDB?): %v", err)
	}

	scratchURL := databaseURLFor(t, baseURL, dbName)
	t.Setenv("DATABASE_URL", scratchURL)
	os.Setenv("DATABASE_URL", scratchURL)
	if err := ConnectToDB(); err != nil {
		_ = admin.Close(ctx)
		t.Fatalf("ConnectToDB: %v", err)
	}

	t.Cleanup(func() {
		bg := context.Background()
		CloseDB()
		if _, err := admin.Exec(bg, "DROP DATABASE IF EXISTS "+pgx.Identifier{dbName}.Sanitize()+" WITH (FORCE)"); err != nil {
			t.Logf("cleanup: drop scratch database %s: %v", dbName, err)
		}
		_ = admin.Close(bg)
	})

	if err := RunMigrations(ctx); err != nil {
		t.Fatalf("RunMigrations: %v", err)
	}
	if err := EnsureAdministratorsGroup(ctx); err != nil {
		t.Fatalf("EnsureAdministratorsGroup: %v", err)
	}

	adminGroupID := func() string {
		var id string
		if err := Pool.QueryRow(ctx, `SELECT _id::text FROM _group WHERE name = $1`, AdministratorsGroupName).Scan(&id); err != nil {
			t.Fatalf("look up %s group: %v", AdministratorsGroupName, err)
		}
		return id
	}()

	newUser := func(label string) string {
		var id string
		if err := Pool.QueryRow(ctx, `
			INSERT INTO _user (name, email, password_hash)
			VALUES ($1, $2, '$2a$10$N9qo8uLOickgx2ZMRZoMyeIjZAgcfl7p92ldGxad68LJZdL17lhWy')
			RETURNING _id::text
		`, label, label+"@example.test").Scan(&id); err != nil {
			t.Fatalf("seed _user %s: %v", label, err)
		}
		return id
	}

	alice := newUser("membership-alice")

	// 1. Membership alone confers the role: nothing is written to _user_role.
	if err := AddGroupMembership(ctx, alice, adminGroupID); err != nil {
		t.Fatalf("AddGroupMembership: %v", err)
	}
	if !hasGlobalPermission(t, ctx, alice, "admin") {
		t.Fatal("a member of the Administrators group does not have global admin")
	}
	if n, err := CountDirectUserRoleGrants(ctx); err != nil {
		t.Fatalf("CountDirectUserRoleGrants: %v", err)
	} else if n != 0 {
		t.Fatalf("adding a group membership wrote %d direct _user_role grants, want 0", n)
	}

	// 2. Re-adding is a no-op, not an error: a double-submitted form must not
	//    look like a failure.
	if err := AddGroupMembership(ctx, alice, adminGroupID); err != nil {
		t.Fatalf("re-adding a membership should be idempotent: %v", err)
	}

	// 3. The membership shows up with the roles it confers.
	memberships, err := ListAuthzGroupMemberships(ctx)
	if err != nil {
		t.Fatalf("ListAuthzGroupMemberships: %v", err)
	}
	if len(memberships) != 1 {
		t.Fatalf("got %d group memberships, want 1", len(memberships))
	}
	if memberships[0].UserName != "membership-alice" || memberships[0].GroupName != AdministratorsGroupName {
		t.Fatalf("unexpected membership row: %+v", memberships[0])
	}
	if memberships[0].Roles != "admin" {
		t.Fatalf("membership reports roles %q, want %q", memberships[0].Roles, "admin")
	}

	groups, err := ListAuthzGroups(ctx)
	if err != nil {
		t.Fatalf("ListAuthzGroups: %v", err)
	}
	found := false
	for _, g := range groups {
		if g.Name != AdministratorsGroupName {
			continue
		}
		found = true
		if !g.IsSystem {
			t.Error("Administrators group is not marked is_system")
		}
		if g.MemberCnt != 1 {
			t.Errorf("Administrators member count is %d, want 1", g.MemberCnt)
		}
		if g.Roles != "admin" {
			t.Errorf("Administrators carries roles %q, want %q", g.Roles, "admin")
		}
	}
	if !found {
		t.Fatalf("Administrators group missing from ListAuthzGroups")
	}

	// 4. Removing the last admin through the new path is refused by the
	//    database guard, and surfaces as the shared sentinel error.
	if err := RemoveGroupMembership(ctx, alice, adminGroupID); err == nil {
		t.Fatal("removing the last global admin through a group membership was allowed")
	} else if !errors.Is(err, ErrLastGlobalAdminRemoval) {
		t.Fatalf("RemoveGroupMembership on the last admin returned %v, want ErrLastGlobalAdminRemoval", err)
	}

	// 4b. The revoke must be visible on the very next permission check. The
	//     authz cache has a 30s TTL, so a stale positive here would leave a
	//     demoted user an admin for up to half a minute. Prime the cache first
	//     so this fails without the invalidation.
	if ok, err := UserHasPermission(ctx, alice, "admin", ""); err != nil {
		t.Fatalf("prime authz cache: %v", err)
	} else if !ok {
		t.Fatal("cache prime: alice should hold admin before the revoke")
	}

	// 5. With a second admin it goes through.
	bob := newUser("membership-bob")
	if err := AddGroupMembership(ctx, bob, adminGroupID); err != nil {
		t.Fatalf("AddGroupMembership bob: %v", err)
	}
	if err := RemoveGroupMembership(ctx, alice, adminGroupID); err != nil {
		t.Fatalf("removing one of two admins should be allowed: %v", err)
	}
	if hasGlobalPermission(t, ctx, alice, "admin") {
		t.Fatal("alice still has global admin after her membership was removed")
	}
	if ok, err := UserHasPermission(ctx, alice, "admin", ""); err != nil {
		t.Fatalf("post-revoke permission check: %v", err)
	} else if ok {
		t.Fatal("cached admin permission survived the membership removal")
	}
	if !hasGlobalPermission(t, ctx, bob, "admin") {
		t.Fatal("bob lost global admin when alice's membership was removed")
	}
}

// hasGlobalPermission mirrors the authz check without going through the
// cache, so a cached negative cannot make the test lie.
func hasGlobalPermission(t *testing.T, ctx context.Context, userID, action string) bool {
	t.Helper()
	var ok bool
	if err := Pool.QueryRow(ctx, effectiveGlobalAdminUsersCTE+`
		SELECT EXISTS (
			SELECT 1
			FROM effective_roles er
			JOIN _role_permission rp ON rp.role_id = er.role_id
			JOIN _permission p ON p._id = rp.permission_id
			WHERE er.user_id = $1
			  AND p.resource = 'platform'
			  AND p.action = $2
			  AND p.scope = 'global'
		)
	`, userID, action).Scan(&ok); err != nil {
		t.Fatalf("global permission query: %v", err)
	}
	return ok
}

// TestBootstrapAdminWritesNoDirectGrant pins the bug found in the live smoke
// test for #199 R3: while bootstrap dual-wrote _user_role, the last-admin guard
// was blind, because removing the group membership still left an admin behind
// via the direct grant. The guard could not fire at all. After R3 bootstrap
// grants admin by membership only, so the guard sees the removal and refuses
// it when it would empty the instance.
func TestBootstrapAdminWritesNoDirectGrant(t *testing.T) {
	baseURL := os.Getenv("TEST_DATABASE_URL")
	if baseURL == "" {
		t.Skip("TEST_DATABASE_URL not set; skipping real-Postgres integration test")
	}

	ctx := context.Background()
	dbName := fmt.Sprintf("velm_bootstrap_%d", time.Now().UnixNano())

	adminConn, err := pgx.Connect(ctx, baseURL)
	if err != nil {
		t.Fatalf("connect to TEST_DATABASE_URL: %v", err)
	}
	if _, err := adminConn.Exec(ctx, "CREATE DATABASE "+pgx.Identifier{dbName}.Sanitize()); err != nil {
		_ = adminConn.Close(ctx)
		t.Fatalf("create scratch database: %v", err)
	}

	scratchURL := databaseURLFor(t, baseURL, dbName)
	t.Setenv("DATABASE_URL", scratchURL)
	os.Setenv("DATABASE_URL", scratchURL)
	if err := ConnectToDB(); err != nil {
		_ = adminConn.Close(ctx)
		t.Fatalf("ConnectToDB: %v", err)
	}
	t.Cleanup(func() {
		bg := context.Background()
		CloseDB()
		if _, err := adminConn.Exec(bg, "DROP DATABASE IF EXISTS "+pgx.Identifier{dbName}.Sanitize()+" WITH (FORCE)"); err != nil {
			t.Logf("cleanup: drop scratch database %s: %v", dbName, err)
		}
		_ = adminConn.Close(bg)
	})

	if err := RunMigrations(ctx); err != nil {
		t.Fatalf("RunMigrations: %v", err)
	}
	if err := EnsureAdministratorsGroup(ctx); err != nil {
		t.Fatalf("EnsureAdministratorsGroup: %v", err)
	}

	const email = "boot-admin@example.test"
	var userID string
	if err := Pool.QueryRow(ctx, `
		INSERT INTO _user (name, email, password_hash)
		VALUES ('boot-admin', $1, '$2a$10$N9qo8uLOickgx2ZMRZoMyeIjZAgcfl7p92ldGxad68LJZdL17lhWy')
		RETURNING _id::text
	`, email).Scan(&userID); err != nil {
		t.Fatalf("seed _user: %v", err)
	}

	if err := BootstrapAdminByEmail(ctx, email); err != nil {
		t.Fatalf("BootstrapAdminByEmail: %v", err)
	}

	if !hasGlobalPermission(t, ctx, userID, "admin") {
		t.Fatal("bootstrapped user is not a global admin")
	}
	if n, err := CountDirectUserRoleGrants(ctx); err != nil {
		t.Fatalf("CountDirectUserRoleGrants: %v", err)
	} else if n != 0 {
		t.Fatalf("bootstrap wrote %d direct _user_role grants, want 0: a direct grant makes the last-admin guard blind", n)
	}

	var groupID string
	if err := Pool.QueryRow(ctx, `SELECT _id::text FROM _group WHERE name = $1`, AdministratorsGroupName).Scan(&groupID); err != nil {
		t.Fatalf("look up Administrators group: %v", err)
	}
	if err := RemoveGroupMembership(ctx, userID, groupID); err == nil {
		t.Fatal("removing the only admin's group membership was allowed; the guard is blind")
	} else if !errors.Is(err, ErrLastGlobalAdminRemoval) {
		t.Fatalf("RemoveGroupMembership returned %v, want ErrLastGlobalAdminRemoval", err)
	}
}
