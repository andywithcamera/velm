package db

import (
	"context"
	"errors"
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

type UserRoleAssignment struct {
	UserID   string
	UserName string
	Email    string
	RoleID   string
	RoleName string
	AppID    string
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

func ListUserRoleAssignments(ctx context.Context) ([]UserRoleAssignment, error) {
	rows, err := Pool.Query(ctx, `
		SELECT
			ur.user_id,
			COALESCE(u.name, ''),
			COALESCE(u.email, ''),
			ur.role_id::text,
			r.name,
			COALESCE(ur.app_id, '')
		FROM _user_role ur
		JOIN _role r ON r._id = ur.role_id
		LEFT JOIN _user u ON u._id::text = ur.user_id
		ORDER BY u.name ASC, r.priority ASC
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var assignments []UserRoleAssignment
	for rows.Next() {
		var assignment UserRoleAssignment
		if err := rows.Scan(
			&assignment.UserID,
			&assignment.UserName,
			&assignment.Email,
			&assignment.RoleID,
			&assignment.RoleName,
			&assignment.AppID,
		); err != nil {
			return nil, err
		}
		assignments = append(assignments, assignment)
	}
	return assignments, rows.Err()
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

func GrantUserRole(ctx context.Context, userID, roleID, appID string) error {
	userID = strings.TrimSpace(userID)
	roleID = strings.TrimSpace(roleID)
	appID = strings.TrimSpace(appID)

	if roleID == "" {
		return errors.New("role id is required")
	}
	if appID == "" {
		appID = ""
	}

	_, err := Pool.Exec(ctx, `
		INSERT INTO _user_role (user_id, role_id, app_id)
		VALUES ($1, $2, $3)
		ON CONFLICT DO NOTHING
	`, userID, roleID, appID)
	if err == nil {
		InvalidateAuthzCacheForUser(userID)
	}
	return err
}

func RevokeUserRole(ctx context.Context, userID, roleID, appID string) error {
	userID = strings.TrimSpace(userID)
	roleID = strings.TrimSpace(roleID)
	appID = strings.TrimSpace(appID)

	if appID == "" {
		hasAdminPerm, err := roleHasGlobalAdminPermission(ctx, roleID)
		if err != nil {
			return err
		}
		if hasAdminPerm {
			exists, err := userRoleAssignmentExists(ctx, userID, roleID, appID)
			if err != nil {
				return err
			}
			if exists {
				adminCountAfter, err := countGlobalAdminsAfterUserRoleRevocation(ctx, userID, roleID, appID)
				if err != nil {
					return err
				}
				if adminCountAfter == 0 {
					return ErrLastGlobalAdminRemoval
				}
			}
		}
	}

	_, err := Pool.Exec(ctx, `
		DELETE FROM _user_role
		WHERE user_id = $1
		  AND role_id = $2
		  AND app_id = $3
	`, userID, roleID, appID)
	if err == nil {
		InvalidateAuthzCacheForUser(userID)
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

func roleHasGlobalAdminPermission(ctx context.Context, roleID string) (bool, error) {
	var ok bool
	err := Pool.QueryRow(ctx, `
		WITH RECURSIVE effective_roles AS (
			SELECT $1::uuid AS role_id
			UNION
			SELECT ri.inherits_role_id
			FROM effective_roles er
			JOIN _role_inheritance ri ON ri.role_id = er.role_id
		)
		SELECT EXISTS (
			SELECT 1
			FROM effective_roles er
			JOIN _role_permission rp ON rp.role_id = er.role_id
			JOIN _permission p ON p._id = rp.permission_id
			WHERE p.resource = 'platform'
			  AND p.action = 'admin'
			  AND p.scope = 'global'
		)
	`, roleID).Scan(&ok)
	return ok, err
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

func userRoleAssignmentExists(ctx context.Context, userID, roleID, appID string) (bool, error) {
	var ok bool
	err := Pool.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1
			FROM _user_role
			WHERE user_id = $1
			  AND role_id = $2
			  AND app_id = $3
		)
	`, userID, roleID, appID).Scan(&ok)
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

func countGlobalAdminsAfterUserRoleRevocation(ctx context.Context, userID, roleID, appID string) (int, error) {
	var count int
	err := Pool.QueryRow(ctx, `
		WITH RECURSIVE seed_roles AS (
			SELECT ur.user_id, ur.role_id
			FROM _user_role ur
			WHERE ur.app_id = ''
			  AND NOT (ur.user_id = $1 AND ur.role_id = $2 AND ur.app_id = $3)
			UNION
			SELECT gm.user_id, gr.role_id
			FROM _group_membership gm
			JOIN _group_role gr ON gr.group_id = gm.group_id
			WHERE gr.app_id = ''
		),
		effective_roles AS (
			SELECT sr.user_id, sr.role_id
			FROM seed_roles sr
			UNION
			SELECT er.user_id, ri.inherits_role_id
			FROM effective_roles er
			JOIN _role_inheritance ri ON ri.role_id = er.role_id
		)
		SELECT COUNT(DISTINCT er.user_id)
		FROM effective_roles er
		JOIN _role_permission rp ON rp.role_id = er.role_id
		JOIN _permission p ON p._id = rp.permission_id
		WHERE p.resource = 'platform'
		  AND p.action = 'admin'
		  AND p.scope = 'global'
	`, userID, roleID, appID).Scan(&count)
	return count, err
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
