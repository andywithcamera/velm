package db

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
)

// TestNewUserGetsARole reproduces issue #196 and proves the repair.
//
// A user created through the record form is a _user row and nothing else. With
// no group membership it has no roles, so every permission check fails and
// every page redirects to /forbidden. The test asserts that inert state first
// (that is the defect), then that the code path used by the save handler gives
// the account read access without over-granting.
func TestNewUserGetsARole(t *testing.T) {
	baseURL := os.Getenv("TEST_DATABASE_URL")
	if baseURL == "" {
		t.Skip("TEST_DATABASE_URL not set; skipping real-Postgres integration test")
	}

	ctx := context.Background()
	dbName := fmt.Sprintf("velm_defaultrole_%d", time.Now().UnixNano())

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

	newUser := func(name, email string) string {
		var id string
		if err := Pool.QueryRow(ctx,
			`INSERT INTO _user (name, email) VALUES ($1, $2) RETURNING _id::text`,
			name, email).Scan(&id); err != nil {
			t.Fatalf("insert user: %v", err)
		}
		return id
	}
	can := func(userID, action string) bool {
		allowed, err := UserHasPermission(ctx, userID, action, "")
		if err != nil {
			t.Fatalf("UserHasPermission(%s): %v", action, err)
		}
		return allowed
	}

	stuckID := newUser("Stuck", "stuck@example.com")

	// The defect: signed in, valid credential, no role, so even read is denied.
	if can(stuckID, "view") {
		t.Fatal("a user with no membership can already view; the defect scenario is not reproduced")
	}

	// The repair, exactly as HandleSave calls it: inside the creating tx.
	tx, err := Pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	if err := AssignDefaultUserRoleTx(ctx, tx, stuckID); err != nil {
		_ = tx.Rollback(ctx)
		t.Fatalf("AssignDefaultUserRoleTx: %v", err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("commit: %v", err)
	}
	InvalidateAuthzCache()

	if !can(stuckID, "view") {
		t.Fatal("user still cannot view after the default role; every page would still redirect to /forbidden")
	}
	if can(stuckID, "admin") {
		t.Fatal("default role granted admin; a new user must not be an administrator")
	}

	// Idempotent: running it again does not add memberships or fail.
	tx2, err := Pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	if err := AssignDefaultUserRoleTx(ctx, tx2, stuckID); err != nil {
		_ = tx2.Rollback(ctx)
		t.Fatalf("second AssignDefaultUserRoleTx: %v", err)
	}
	if err := tx2.Commit(ctx); err != nil {
		t.Fatalf("commit: %v", err)
	}
	var memberships int
	if err := Pool.QueryRow(ctx, `SELECT COUNT(*) FROM _group_membership WHERE user_id = $1`, stuckID).Scan(&memberships); err != nil {
		t.Fatalf("count memberships: %v", err)
	}
	if memberships != 1 {
		t.Fatalf("expected exactly 1 membership after a repeat call, got %d", memberships)
	}

	// A user who already holds a role is left alone: no membership is added,
	// so an operator's grant is never widened to the default group.
	placedID := newUser("Placed", "placed@example.com")
	if _, err := Pool.Exec(ctx, `INSERT INTO _group_membership (user_id, group_id)
		SELECT $1, g._id FROM _group g WHERE g.name = $2`, placedID, AdministratorsGroupName); err != nil {
		t.Fatalf("seed administrators group membership: %v", err)
	}
	tx3, err := Pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	if err := AssignDefaultUserRoleTx(ctx, tx3, placedID); err != nil {
		_ = tx3.Rollback(ctx)
		t.Fatalf("AssignDefaultUserRoleTx for placed user: %v", err)
	}
	if err := tx3.Commit(ctx); err != nil {
		t.Fatalf("commit: %v", err)
	}
	if err := Pool.QueryRow(ctx, `SELECT COUNT(*) FROM _group_membership WHERE user_id = $1`, placedID).Scan(&memberships); err != nil {
		t.Fatalf("count memberships: %v", err)
	}
	if memberships != 1 {
		t.Fatalf("a user that already had a role joined the default group too: %d memberships", memberships)
	}
}
