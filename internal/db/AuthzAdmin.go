package db

import (
	"context"
	"errors"
	"fmt"
	"strings"
)

var ErrLastGlobalAdminRemoval = errors.New("cannot remove the last global admin")

type AuthzRole struct {
	ID          string
	Name        string
	Description string
	Priority    int
}

type AuthzPermission struct {
	ID          string
	Resource    string
	Action      string
	Scope       string
	Description string
}

type AuthzUser struct {
	ID    string
	Name  string
	Email string
}

type RolePermissionAssignment struct {
	RoleID       string
	RoleName     string
	PermissionID string
	Resource     string
	Action       string
	Scope        string
}

func ListAuthzUsers(ctx context.Context) ([]AuthzUser, error) {
	rows, err := Pool.Query(ctx, `
		SELECT _id::text, name, email
		FROM _user
		ORDER BY name ASC
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var users []AuthzUser
	for rows.Next() {
		var user AuthzUser
		if err := rows.Scan(&user.ID, &user.Name, &user.Email); err != nil {
			return nil, err
		}
		users = append(users, user)
	}
	return users, rows.Err()
}

func ListRoles(ctx context.Context) ([]AuthzRole, error) {
	rows, err := Pool.Query(ctx, `
		SELECT _id::text, name, COALESCE(description, ''), priority
		FROM _role
		ORDER BY priority ASC, name ASC
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var roles []AuthzRole
	for rows.Next() {
		var role AuthzRole
		if err := rows.Scan(&role.ID, &role.Name, &role.Description, &role.Priority); err != nil {
			return nil, err
		}
		roles = append(roles, role)
	}
	return roles, rows.Err()
}

func ListPermissions(ctx context.Context) ([]AuthzPermission, error) {
	rows, err := Pool.Query(ctx, `
		SELECT _id::text, resource, action, scope, COALESCE(description, '')
		FROM _permission
		ORDER BY resource ASC, action ASC, scope ASC
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var permissions []AuthzPermission
	for rows.Next() {
		var permission AuthzPermission
		if err := rows.Scan(
			&permission.ID,
			&permission.Resource,
			&permission.Action,
			&permission.Scope,
			&permission.Description,
		); err != nil {
			return nil, err
		}
		permissions = append(permissions, permission)
	}
	return permissions, rows.Err()
}

func ListRolePermissionAssignments(ctx context.Context) ([]RolePermissionAssignment, error) {
	rows, err := Pool.Query(ctx, `
		SELECT
			r._id::text,
			r.name,
			p._id::text,
			p.resource,
			p.action,
			p.scope
		FROM _role_permission rp
		JOIN _role r ON r._id = rp.role_id
		JOIN _permission p ON p._id = rp.permission_id
		ORDER BY r.priority ASC, p.resource ASC, p.action ASC, p.scope ASC
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var assignments []RolePermissionAssignment
	for rows.Next() {
		var assignment RolePermissionAssignment
		if err := rows.Scan(
			&assignment.RoleID,
			&assignment.RoleName,
			&assignment.PermissionID,
			&assignment.Resource,
			&assignment.Action,
			&assignment.Scope,
		); err != nil {
			return nil, err
		}
		assignments = append(assignments, assignment)
	}
	return assignments, rows.Err()
}

func CreateRole(ctx context.Context, name, description string, priority int) error {
	if isReservedRoleName(name) {
		return fmt.Errorf("role name %q is reserved: it matches any signed-in user and is set on service methods and endpoints, not granted to people", strings.TrimSpace(name))
	}
	_, err := Pool.Exec(ctx, `
		INSERT INTO _role (name, description, is_system, priority)
		VALUES ($1, $2, FALSE, $3)
		ON CONFLICT (name)
		DO UPDATE SET
			description = EXCLUDED.description,
			priority = EXCLUDED.priority,
			_updated_at = NOW()
	`, strings.TrimSpace(strings.ToLower(name)), strings.TrimSpace(description), priority)
	if err == nil {
		InvalidateAuthzCache()
	}
	return err
}

func CreatePermission(ctx context.Context, resource, action, scope, description string) error {
	_, err := Pool.Exec(ctx, `
		INSERT INTO _permission (resource, action, scope, description)
		VALUES ($1, $2, $3, $4)
		ON CONFLICT (resource, action, scope)
		DO UPDATE SET
			description = EXCLUDED.description,
			_updated_at = NOW()
	`,
		strings.TrimSpace(strings.ToLower(resource)),
		strings.TrimSpace(strings.ToLower(action)),
		strings.TrimSpace(strings.ToLower(scope)),
		strings.TrimSpace(description),
	)
	if err == nil {
		InvalidateAuthzCache()
	}
	return err
}

func GrantRolePermission(ctx context.Context, roleID, permissionID string) error {
	_, err := Pool.Exec(ctx, `
		INSERT INTO _role_permission (role_id, permission_id)
		VALUES ($1, $2)
		ON CONFLICT DO NOTHING
	`, roleID, permissionID)
	if err == nil {
		InvalidateAuthzCache()
	}
	return err
}

func RevokeRolePermission(ctx context.Context, roleID, permissionID string) error {
	isAdminPermission, err := permissionIsGlobalAdmin(ctx, permissionID)
	if err != nil {
		return err
	}
	if isAdminPermission {
		exists, err := rolePermissionAssignmentExists(ctx, roleID, permissionID)
		if err != nil {
			return err
		}
		if exists {
			adminCountAfter, err := countGlobalAdminsAfterRolePermissionRevocation(ctx, roleID, permissionID)
			if err != nil {
				return err
			}
			if adminCountAfter == 0 {
				return ErrLastGlobalAdminRemoval
			}
		}
	}

	_, err = Pool.Exec(ctx, `
		DELETE FROM _role_permission
		WHERE role_id = $1
		  AND permission_id = $2
	`, roleID, permissionID)
	if err == nil {
		InvalidateAuthzCache()
	}
	return err
}

func permissionIsGlobalAdmin(ctx context.Context, permissionID string) (bool, error) {
	var ok bool
	err := Pool.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1
			FROM _permission p
			WHERE p._id = $1
			  AND p.resource = 'platform'
			  AND p.action = 'admin'
			  AND p.scope = 'global'
		)
	`, permissionID).Scan(&ok)
	return ok, err
}

func rolePermissionAssignmentExists(ctx context.Context, roleID, permissionID string) (bool, error) {
	var ok bool
	err := Pool.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1
			FROM _role_permission
			WHERE role_id = $1
			  AND permission_id = $2
		)
	`, roleID, permissionID).Scan(&ok)
	return ok, err
}

func countGlobalAdminsAfterRolePermissionRevocation(ctx context.Context, roleID, permissionID string) (int, error) {
	var count int
	err := Pool.QueryRow(ctx, effectiveGlobalAdminUsersCTE+`
		SELECT COUNT(DISTINCT er.user_id)
		FROM effective_roles er
		JOIN _role_permission rp ON rp.role_id = er.role_id
		JOIN _permission p ON p._id = rp.permission_id
		WHERE p.resource = 'platform'
		  AND p.action = 'admin'
		  AND p.scope = 'global'
		  AND NOT (rp.role_id = $1 AND rp.permission_id = $2)
	`, roleID, permissionID).Scan(&count)
	return count, err
}
