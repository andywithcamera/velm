package db

import (
	"context"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"
)

// AdministratorsGroupName is the out-of-the-box group that carries the global
// admin role. Admin authority is granted by membership in this group alone
// (issue #199 R4).
const AdministratorsGroupName = "Administrators"

// administratorsGroupDescription deliberately differs from
// provisionedSeedGroupDescription so app seed cleanup never treats the group as
// provisioned app data. The group is also marked is_system, which
// deleteProvisionedSeedGroupsTx skips outright.
const administratorsGroupDescription = "Out-of-the-box group carrying the global admin role."

// EnsureAdministratorsGroupTx makes the Administrators group exist and hold the
// global admin role. Idempotent: safe on every boot and on every grant.
func EnsureAdministratorsGroupTx(ctx context.Context, tx pgx.Tx) (string, error) {
	var groupID string
	if err := tx.QueryRow(ctx, `
		INSERT INTO _group (name, description, is_system)
		VALUES ($1, $2, TRUE)
		ON CONFLICT (name) DO UPDATE
		SET is_system = TRUE
		RETURNING _id::text
	`, AdministratorsGroupName, administratorsGroupDescription).Scan(&groupID); err != nil {
		return "", fmt.Errorf("ensure %s group: %w", AdministratorsGroupName, err)
	}

	adminRoleID, err := adminRoleIDTx(ctx, tx)
	if err != nil {
		return "", err
	}

	// Reconcile before granting (issue #216). A grant carrying a non-empty
	// app_id resolves to nothing in the global-admin queries, which filter
	// gr.app_id = ''. Two rows can coexist because the unique key includes
	// app_id, so one stray scoped row does not prevent the global one and used
	// to leave the group silently powerless. Only the global grant is valid on
	// this system group, so drop any scoped admin grants.
	if _, err := tx.Exec(ctx, `
		DELETE FROM _group_role
		WHERE group_id = $1
		  AND role_id = $2
		  AND app_id <> ''
	`, groupID, adminRoleID); err != nil {
		return "", fmt.Errorf("reconcile scoped admin grants on %s: %w", AdministratorsGroupName, err)
	}

	if _, err := tx.Exec(ctx, `
		INSERT INTO _group_role (group_id, role_id, app_id)
		VALUES ($1, $2, '')
		ON CONFLICT (group_id, role_id, app_id) DO NOTHING
	`, groupID, adminRoleID); err != nil {
		return "", fmt.Errorf("grant global admin role to %s: %w", AdministratorsGroupName, err)
	}

	return groupID, nil
}

// EnsureAdministratorsGroup runs EnsureAdministratorsGroupTx in its own transaction.
func EnsureAdministratorsGroup(ctx context.Context) error {
	if Pool == nil {
		return fmt.Errorf("database pool is not initialized")
	}

	tx, err := Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if _, err := EnsureAdministratorsGroupTx(ctx, tx); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// AddUserToAdministratorsGroupTx makes userID a member of the Administrators
// group, creating the group first if needed. Idempotent.
func AddUserToAdministratorsGroupTx(ctx context.Context, tx pgx.Tx, userID string) error {
	userID = strings.TrimSpace(userID)
	if userID == "" {
		return fmt.Errorf("user id is required")
	}

	groupID, err := EnsureAdministratorsGroupTx(ctx, tx)
	if err != nil {
		return err
	}

	if _, err := tx.Exec(ctx, `
		INSERT INTO _group_membership (group_id, user_id)
		VALUES ($1, $2)
		ON CONFLICT (group_id, user_id) DO NOTHING
	`, groupID, userID); err != nil {
		return fmt.Errorf("add user %q to %s: %w", userID, AdministratorsGroupName, err)
	}

	InvalidateAuthzCacheForUser(userID)
	return nil
}
