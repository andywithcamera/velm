package db

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// TestLastGlobalAdminGuard proves 082_last_admin_guard.sql holds the line on
// every edge that can empty the last admin, not just the two Go callers that
// used to be the only ones checking.
//
// It runs against its own throwaway database rather than the shared test one.
// The invariant is absolute — "at least one admin exists in this database" —
// so it can only be exercised in a database where the test controls every
// admin. On the shared velm_test database another test's leftover admin makes
// "remove the last admin" legitimately succeed, and the assertion tests
// nothing. Each run creates, migrates and drops velm_guard_<nanos>.
//
// Gated on TEST_DATABASE_URL like the other real-Postgres tests. The user in
// that URL needs CREATEDB (the CI service user and a normal local superuser
// both have it).
func TestLastGlobalAdminGuard(t *testing.T) {
	baseURL := os.Getenv("TEST_DATABASE_URL")
	if baseURL == "" {
		t.Skip("TEST_DATABASE_URL not set; skipping real-Postgres integration test")
	}

	ctx := context.Background()
	dbName := fmt.Sprintf("velm_guard_%d", time.Now().UnixNano())

	admin, err := pgx.Connect(ctx, baseURL)
	if err != nil {
		t.Fatalf("connect to TEST_DATABASE_URL: %v", err)
	}
	if _, err := admin.Exec(ctx, "CREATE DATABASE "+pgx.Identifier{dbName}.Sanitize()); err != nil {
		_ = admin.Close(ctx)
		t.Fatalf("create scratch database (does the TEST_DATABASE_URL user have CREATEDB?): %v", err)
	}

	guardURL := databaseURLFor(t, baseURL, dbName)

	t.Setenv("DATABASE_URL", guardURL)
	os.Setenv("DATABASE_URL", guardURL)
	if err := ConnectToDB(); err != nil {
		_ = admin.Close(ctx)
		t.Fatalf("ConnectToDB: %v", err)
	}

	t.Cleanup(func() {
		bg := context.Background()
		CloseDB()
		// FORCE terminates any connection the pool left behind; without it the
		// drop fails on "database is being accessed by other users".
		if _, err := admin.Exec(bg, "DROP DATABASE IF EXISTS "+pgx.Identifier{dbName}.Sanitize()+" WITH (FORCE)"); err != nil {
			t.Logf("cleanup: drop scratch database %s: %v", dbName, err)
		}
		_ = admin.Close(bg)
	})

	if err := RunMigrations(ctx); err != nil {
		t.Fatalf("RunMigrations (must include 082): %v", err)
	}
	if err := EnsureAdministratorsGroup(ctx); err != nil {
		t.Fatalf("EnsureAdministratorsGroup: %v", err)
	}

	// A virgin database: migrations, no users. Nothing to strand yet.
	if count := countGlobalAdmins(t, ctx); count != 0 {
		t.Fatalf("scratch database starts with %d global admins, want 0", count)
	}

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

	alice := newUser("guard-alice")
	bob := newUser("guard-bob")

	join := func(userID string) {
		tx, err := Pool.Begin(ctx)
		if err != nil {
			t.Fatalf("begin: %v", err)
		}
		if err := AddUserToAdministratorsGroupTx(ctx, tx, userID); err != nil {
			_ = tx.Rollback(ctx)
			t.Fatalf("AddUserToAdministratorsGroupTx: %v", err)
		}
		if err := tx.Commit(ctx); err != nil {
			t.Fatalf("commit join: %v", err)
		}
	}

	leave := func(userID string) error {
		tx, err := Pool.Begin(ctx)
		if err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `DELETE FROM _group_membership WHERE user_id = $1`, userID); err != nil {
			_ = tx.Rollback(ctx)
			return err
		}
		return tx.Commit(ctx)
	}

	join(alice)
	join(bob)
	if count := countGlobalAdmins(t, ctx); count != 2 {
		t.Fatalf("after joining both users the database has %d global admins, want 2", count)
	}

	// 1. Moving admin between users inside one transaction is legitimate even
	//    though it deletes a membership before the replacement exists. A
	//    non-deferred trigger rejects this at the wrong moment; that is the
	//    whole reason the guard is DEFERRABLE INITIALLY DEFERRED.
	moveTx, err := Pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin move: %v", err)
	}
	if _, err := moveTx.Exec(ctx, `DELETE FROM _group_membership WHERE user_id = $1`, alice); err != nil {
		_ = moveTx.Rollback(ctx)
		t.Fatalf("move: delete alice membership: %v", err)
	}
	if _, err := moveTx.Exec(ctx, `
		INSERT INTO _group_membership (group_id, user_id)
		SELECT g._id, $1 FROM _group g WHERE g.name = $2
		ON CONFLICT DO NOTHING
	`, alice, AdministratorsGroupName); err != nil {
		_ = moveTx.Rollback(ctx)
		t.Fatalf("move: re-add alice: %v", err)
	}
	if err := moveTx.Commit(ctx); err != nil {
		t.Fatalf("moving admin within one transaction was refused: %v", err)
	}

	// 2. Two admins -> removing one is fine.
	if err := leave(bob); err != nil {
		t.Fatalf("removing one of two admins should be allowed: %v", err)
	}
	if count := countGlobalAdmins(t, ctx); count != 1 {
		t.Fatalf("after removing bob the database has %d global admins, want 1", count)
	}

	// 3. The last admin cannot be removed.
	err = leave(alice)
	if err == nil {
		t.Fatal("removing the last global admin was allowed")
	}
	if !IsLastGlobalAdminViolation(err) {
		t.Fatalf("removing the last admin failed with %v, want a VL001 last-admin violation", err)
	}
	if normalized := NormalizeLastGlobalAdminError(err); normalized != ErrLastGlobalAdminRemoval {
		t.Fatalf("NormalizeLastGlobalAdminError = %v, want ErrLastGlobalAdminRemoval", normalized)
	}

	// 4. Deleting the group cascades through _group_membership, so the same
	//    refusal must fire. This is the path the record editor exposes and
	//    nothing checked before.
	if _, err = Pool.Exec(ctx, `DELETE FROM _group WHERE name = $1`, AdministratorsGroupName); err == nil {
		t.Fatal("deleting the admin group was allowed while it held the last admin")
	} else if !IsLastGlobalAdminViolation(err) {
		t.Fatalf("deleting the admin group failed with %v, want a VL001 last-admin violation", err)
	}

	// 5. Dropping the admin permission off the role strands every admin holder
	//    without touching a single membership.
	if _, err = Pool.Exec(ctx, `
		DELETE FROM _role_permission
		WHERE role_id IN (SELECT _id FROM _role WHERE name = 'admin')
		  AND permission_id IN (
			SELECT _id FROM _permission
			WHERE resource = 'platform' AND action = 'admin' AND scope = 'global'
		  )
	`); err == nil {
		t.Fatal("removing platform/admin/global from the admin role was allowed while it held the last admin")
	} else if !IsLastGlobalAdminViolation(err) {
		t.Fatalf("removing the admin permission failed with %v, want a VL001 last-admin violation", err)
	}

	if count := countGlobalAdmins(t, ctx); count != 1 {
		t.Fatalf("the refused operations left %d global admins, want 1", count)
	}

	// 6. Deleting every user is permitted: no users means no admin to strand.
	tx, err := Pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin wipe: %v", err)
	}
	if _, err := tx.Exec(ctx, `DELETE FROM _group_membership`); err != nil {
		_ = tx.Rollback(ctx)
		t.Fatalf("wipe: memberships: %v", err)
	}
	if _, err := tx.Exec(ctx, `DELETE FROM _user`); err != nil {
		_ = tx.Rollback(ctx)
		t.Fatalf("wipe: users: %v", err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("deleting every user should be allowed, got %v", err)
	}
}

func countGlobalAdmins(t *testing.T, ctx context.Context) int {
	t.Helper()
	var count int
	if err := Pool.QueryRow(ctx, effectiveGlobalAdminUsersCTE+`
		SELECT COUNT(DISTINCT er.user_id)
		FROM effective_roles er
		JOIN _role_permission rp ON rp.role_id = er.role_id
		JOIN _permission p ON p._id = rp.permission_id
		WHERE p.resource = 'platform'
		  AND p.action = 'admin'
		  AND p.scope = 'global'
	`).Scan(&count); err != nil {
		t.Fatalf("count global admins: %v", err)
	}
	return count
}

// databaseURLFor swaps the database name in a Postgres URL, keeping every other
// part of the DSN (user, password, host, port, query parameters).
func databaseURLFor(t *testing.T, rawURL, dbName string) string {
	t.Helper()
	parsed, err := url.Parse(rawURL)
	if err != nil {
		t.Fatalf("parse TEST_DATABASE_URL: %v", err)
	}
	if parsed.Scheme == "" {
		t.Fatalf("TEST_DATABASE_URL is not a URL: %q", rawURL)
	}
	parsed.Path = "/" + dbName
	// sslmode etc. live in the query string and carry over untouched.
	if q := parsed.Query(); q.Get("search_path") != "" {
		t.Fatalf("TEST_DATABASE_URL sets search_path=%q; the scratch database needs the default", q.Get("search_path"))
	}
	return parsed.String()
}

// TestLastAdminViolationIsRecognisedByTheRecordEditor pins the mapping the
// generic save path depends on: the deferred trigger's SQLSTATE must be
// translated into a 400 with the shared message, not a bare 500. No database
// needed — the SQLSTATE is the whole contract.
func TestLastAdminViolationIsRecognisedByTheRecordEditor(t *testing.T) {
	err := &pgconn.PgError{Code: lastAdminViolationSQLState, Message: "cannot remove the last global admin"}
	if !IsLastGlobalAdminViolation(err) {
		t.Fatal("IsLastGlobalAdminViolation did not recognise VL001")
	}
	if IsLastGlobalAdminViolation(&pgconn.PgError{Code: "23505"}) {
		t.Fatal("IsLastGlobalAdminViolation matched an unrelated SQLSTATE")
	}
	if IsLastGlobalAdminViolation(nil) {
		t.Fatal("IsLastGlobalAdminViolation matched nil")
	}

	message, status, ok := classifySaveDatabaseError(err)
	if !ok {
		t.Fatal("classifySaveDatabaseError did not classify VL001")
	}
	if status != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", status, http.StatusBadRequest)
	}
	if message != ErrLastGlobalAdminRemoval.Error() {
		t.Fatalf("message = %q, want %q", message, ErrLastGlobalAdminRemoval.Error())
	}
	if strings.TrimSpace(message) == "" {
		t.Fatal("last-admin message is empty; the record editor would show a blank error")
	}
}
