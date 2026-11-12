package db

import (
	"context"
	"fmt"
)

// Group membership is the supported way to give a user a role (issue #199).
// Roles reach a user through _group_membership -> _group_role at read time, so
// there is no stored copy to keep in sync: adding or removing a membership
// changes effective permissions in the same statement.

// AuthzGroup is a group as the access-control page presents it: the roles it
// carries, flattened for display.
type AuthzGroup struct {
	ID        string
	Name      string
	Roles     string
	IsSystem  bool
	MemberCnt int
}

// AuthzGroupMembership is one user's membership of one group.
type AuthzGroupMembership struct {
	UserID    string
	UserName  string
	Email     string
	GroupID   string
	GroupName string
	Roles     string
}

// ListAuthzGroups returns every group with the roles it grants. A role granted
// at app scope is annotated with @<app_id> (issue #216): the scope is part of
// what the grant means, and a scoped grant on a system group is now rejected
// outright.
func ListAuthzGroups(ctx context.Context) ([]AuthzGroup, error) {
	rows, err := Pool.Query(ctx, `
		SELECT g._id::text, g.name, COALESCE(g.is_system, FALSE),
		       COALESCE(string_agg(DISTINCT r.name || CASE WHEN gr.app_id = '' THEN '' ELSE ' @' || gr.app_id END, ', ' ORDER BY r.name || CASE WHEN gr.app_id = '' THEN '' ELSE ' @' || gr.app_id END), ''),
		       (SELECT COUNT(*) FROM _group_membership gm WHERE gm.group_id = g._id)
		FROM _group g
		LEFT JOIN _group_role gr ON gr.group_id = g._id
		LEFT JOIN _role r ON r._id = gr.role_id
		GROUP BY g._id, g.name, g.is_system
		ORDER BY g.name
	`)
	if err != nil {
		return nil, fmt.Errorf("list groups: %w", err)
	}
	defer rows.Close()

	var out []AuthzGroup
	for rows.Next() {
		var g AuthzGroup
		if err := rows.Scan(&g.ID, &g.Name, &g.IsSystem, &g.Roles, &g.MemberCnt); err != nil {
			return nil, fmt.Errorf("scan group: %w", err)
		}
		out = append(out, g)
	}
	return out, rows.Err()
}

// ListAuthzGroupMemberships returns every user->group edge with the roles that
// membership confers, so the page can show where each role actually comes from.
func ListAuthzGroupMemberships(ctx context.Context) ([]AuthzGroupMembership, error) {
	rows, err := Pool.Query(ctx, `
		SELECT u._id::text, u.name, u.email, g._id::text, g.name,
		       COALESCE(string_agg(DISTINCT r.name || CASE WHEN gr.app_id = '' THEN '' ELSE ' @' || gr.app_id END, ', ' ORDER BY r.name || CASE WHEN gr.app_id = '' THEN '' ELSE ' @' || gr.app_id END), '')
		FROM _group_membership gm
		JOIN _group g ON g._id = gm.group_id
		JOIN _user u ON u._id::text = gm.user_id
		LEFT JOIN _group_role gr ON gr.group_id = g._id
		LEFT JOIN _role r ON r._id = gr.role_id
		GROUP BY u._id, u.name, u.email, g._id, g.name
		ORDER BY u.name, g.name
	`)
	if err != nil {
		return nil, fmt.Errorf("list group memberships: %w", err)
	}
	defer rows.Close()

	var out []AuthzGroupMembership
	for rows.Next() {
		var m AuthzGroupMembership
		if err := rows.Scan(&m.UserID, &m.UserName, &m.Email, &m.GroupID, &m.GroupName, &m.Roles); err != nil {
			return nil, fmt.Errorf("scan group membership: %w", err)
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// AddGroupMembership puts a user in a group. Idempotent: re-adding is a no-op
// rather than an error, so a double submit does not look like a failure.
func AddGroupMembership(ctx context.Context, userID, groupID string) error {
	if _, err := Pool.Exec(ctx, `
		INSERT INTO _group_membership (user_id, group_id)
		VALUES ($1, $2)
		ON CONFLICT (user_id, group_id) DO NOTHING
	`, userID, groupID); err != nil {
		return NormalizeLastGlobalAdminError(fmt.Errorf("add group membership: %w", err))
	}
	// A membership changes the roles of one user, but the cache is keyed by
	// (user, action, app) with a 30s TTL, and a stale positive is a stale grant:
	// drop every entry rather than try to work out which actions moved.
	InvalidateAuthzCache()
	return nil
}

// RemoveGroupMembership takes a user out of a group. The last-admin guard runs
// at COMMIT inside the database, so this is refused when it would leave the
// instance with no global admin.
func RemoveGroupMembership(ctx context.Context, userID, groupID string) error {
	if _, err := Pool.Exec(ctx, `
		DELETE FROM _group_membership WHERE user_id = $1 AND group_id = $2
	`, userID, groupID); err != nil {
		return NormalizeLastGlobalAdminError(fmt.Errorf("remove group membership: %w", err))
	}
	InvalidateAuthzCache()
	return nil
}
