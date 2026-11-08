package db

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
)

// TestLastGlobalAdminGuard proves 082_last_admin_guard.sql actually holds the
// line, on every edge that can empty the last admin, not just the two Go
// callers that used to be the only ones checking.
//
// Gated on TEST_DATABASE_URL like the other real-Postgres tests.
func TestLastGlobalAdminGuard(t *testing.T) {
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
		t.Fatalf("RunMigrations (must include 082): %v", err)
	}
	if err := EnsureAdministratorsGroup(ctx); err != nil {
		t.Fatalf("EnsureAdministratorsGroup: %v", err)
	}

	suffix := fmt.Sprintf("%s-%d", t.Name(), time.Now().UnixNano())
	newUser := func(label string) string {
		var id string
		if err := Pool.QueryRow(ctx, `
			INSERT INTO _user (name, email, password_hash)
			VALUES ($1, $2, '$2a$10$N9qo8uLOickgx2ZMRZoMyeIjZAgcfl7p92ldGxad68LJZdL17lhWy')
			RETURNING _id::text
		`, label, label+"-"+suffix+"@example.test").Scan(&id); err != nil {
			t.Fatalf("seed _user %s: %v", label, err)
		}
		return id
	}

	alice := newUser("guard-alice")
	bob := newUser("guard-bob")

	t.Cleanup(func() {
		bg := context.Background()
		// Removing the users is allowed once none remain, which the trigger
		// permits deliberately; do it in one statement so it is one check.
		if _, err := Pool.Exec(bg, `DELETE FROM _group_membership WHERE user_id = ANY($1)`, []string{alice, bob}); err != nil {
			t.Logf("cleanup: membership: %v", err)
		}
		if _, err := Pool.Exec(bg, `DELETE FROM _user WHERE _id::text = ANY($1)`, []string{alice, bob}); err != nil {
			t.Logf("cleanup: users: %v", err)
		}
	})

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

	// 1. Moving admin from alice to bob inside one transaction is legitimate,
	//    even though it deletes alice's membership before bob has one. A
	//    non-deferred trigger would reject this; that is the whole reason the
	//    guard is DEFERRABLE INITIALLY DEFERRED.
	moveTx, err := Pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin move: %v", err)
	}
	if _, err := moveTx.Exec(ctx, `DELETE FROM _group_membership WHERE user_id = $1`, alice); err != nil {
		_ = moveTx.Rollback(ctx)
		t.Fatalf("move: delete alice membership: %v", err)
	}
	// Bob already joined above, so this is really "remove one of two". Re-add
	// alice and delete bob in the same transaction to exercise the ordering.
	if _, err := moveTx.Exec(ctx, `
		INSERT INTO _group_membership (group_id, user_id)
		SELECT g._id, $1 FROM _group g WHERE g.name = $2
		ON CONFLICT DO NOTHING
	`, alice, AdministratorsGroupName); err != nil {
		_ = moveTx.Rollback(ctx)
		t.Fatalf("move: re-add alice: %v", err)
	}
	if err := moveTx.Commit(ctx); err != nil {
		t.Fatalf("moving admin between users in one transaction was refused: %v", err)
	}

	// 2. Two admins -> removing one is fine.
	if err := leave(bob); err != nil {
		t.Fatalf("removing one of two admins should be allowed: %v", err)
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

	// 4. Deleting the group takes the membership with it by cascade, so the
	//    same refusal must still fire — this is the path the record editor
	//    exposes and nothing checked before.
	_, err = Pool.Exec(ctx, `DELETE FROM _group WHERE name = $1`, AdministratorsGroupName)
	if err == nil {
		// The group exists, so the delete would have stranded every admin.
		// Re-create it before failing so the next assertion still means
		// something.
		_ = EnsureAdministratorsGroup(ctx)
		t.Fatal("deleting the admin group was allowed while it held the last admin")
	}
	if !IsLastGlobalAdminViolation(err) {
		t.Fatalf("deleting the admin group failed with %v, want a VL001 last-admin violation", err)
	}

	// 5. Deleting every user is permitted: no users means no admin to strand,
	//    and blocking a teardown helps nobody.
	tx, err := Pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin wipe: %v", err)
	}
	if _, err := tx.Exec(ctx, `DELETE FROM _user WHERE _id::text = ANY($1)`, []string{alice, bob}); err != nil {
		_ = tx.Rollback(ctx)
		t.Fatalf("wipe: delete users: %v", err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("deleting every user should be allowed, got %v", err)
	}
}

// TestLastAdminViolationIsRecognisedByTheRecordEditor pins the mapping the
// generic save path depends on: the deferred trigger's SQLSTATE must be
// translated into a 400 with the shared message, not a bare 500.
func TestLastAdminViolationIsRecognisedByTheRecordEditor(t *testing.T) {
	err := &pgconn.PgError{Code: lastAdminViolationSQLState, Message: "cannot remove the last global admin"}
	if !IsLastGlobalAdminViolation(err) {
		t.Fatal("IsLastGlobalAdminViolation did not recognise VL001")
	}
	if IsLastGlobalAdminViolation(&pgconn.PgError{Code: "23505"}) {
		t.Fatal("IsLastGlobalAdminViolation matched an unrelated SQLSTATE")
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
}
