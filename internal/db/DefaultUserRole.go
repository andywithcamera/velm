package db

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"
)

// A user created through the record form used to get no role at all: the
// backfill in 002_authz_core.sql only covered rows that existed when it ran,
// and roles reach a user only through group membership since 083. With zero
// roles every permission check fails, so the account signs in and is then
// redirected to /forbidden on every page, including the read-only ones
// (issue #196).
//
// The repair is a membership of a shared system group that carries the viewer
// role: least privilege, one canonical grant path, and visible on /admin/access
// so an operator can move the user up or out.

const (
	// DefaultUserRoleGroupName is the group every roleless new user joins.
	DefaultUserRoleGroupName = "All users"
	// DefaultUserRoleGroupRole is the role that group carries.
	DefaultUserRoleGroupRole = "viewer"
)

// ensureDefaultUserRoleGroupTx returns the id of the default group, creating
// the group and its viewer grant if this instance does not have them yet.
func ensureDefaultUserRoleGroupTx(ctx context.Context, tx pgx.Tx) (string, error) {
	var groupID string
	err := tx.QueryRow(ctx, `
		INSERT INTO _group (name, description, is_system)
		VALUES ($1, 'Every user created through the UI joins this group so the account can sign in and read. Add roles here to grant them to all such users.', TRUE)
		ON CONFLICT (name) DO UPDATE SET name = EXCLUDED.name
		RETURNING _id::text
	`, DefaultUserRoleGroupName).Scan(&groupID)
	if err != nil {
		return "", fmt.Errorf("ensure default role group: %w", err)
	}

	var roleID string
	if err := tx.QueryRow(ctx, `SELECT _id::text FROM _role WHERE name = $1`, DefaultUserRoleGroupRole).Scan(&roleID); err != nil {
		return "", fmt.Errorf("look up %s role: %w", DefaultUserRoleGroupRole, err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO _group_role (group_id, role_id, app_id)
		VALUES ($1, $2, '')
		ON CONFLICT (group_id, role_id, app_id) DO NOTHING
	`, groupID, roleID); err != nil {
		return "", fmt.Errorf("ensure default role grant: %w", err)
	}
	return groupID, nil
}

// AssignDefaultUserRoleTx gives userID a role if it has no global role at all.
// Idempotent, and a no-op for a user that already holds any global role, so an
// operator who grants a role before the user is created (or a second pass) is
// never overridden.
func AssignDefaultUserRoleTx(ctx context.Context, tx pgx.Tx, userID string) error {
	var hasRole bool
	if err := tx.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1
			FROM _group_membership gm
			JOIN _group_role gr ON gr.group_id = gm.group_id
			WHERE gm.user_id = $1 AND gr.app_id = ''
		)
	`, userID).Scan(&hasRole); err != nil {
		return fmt.Errorf("check user roles: %w", err)
	}
	if hasRole {
		return nil
	}

	groupID, err := ensureDefaultUserRoleGroupTx(ctx, tx)
	if err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO _group_membership (user_id, group_id)
		VALUES ($1, $2)
		ON CONFLICT (user_id, group_id) DO NOTHING
	`, userID, groupID); err != nil {
		return fmt.Errorf("add default group membership: %w", err)
	}
	return nil
}
