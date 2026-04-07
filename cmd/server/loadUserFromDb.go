package main

import (
	"context"
	"fmt"
	"velm/internal/db"
)

// loadUserFromDB fetches a user by email.
func loadUserFromDB(email string) (User, error) {
	ctx := context.Background()
	row := db.Pool.QueryRow(ctx,
		`SELECT
			u._id::text,
			u.name,
			u.email
		 FROM _user u
		 WHERE email = $1
		 LIMIT 1`,
		email,
	)

	var user User
	err := row.Scan(&user.ID, &user.Name, &user.Email)
	if err != nil {
		return User{}, fmt.Errorf("failed to load user from DB: %w", err)
	}
	if err := db.Pool.QueryRow(ctx, effectivePrimaryUserRoleNameQuery, user.ID).Scan(&user.Role); err != nil {
		return User{}, fmt.Errorf("failed to load effective user role: %w", err)
	}

	return user, nil
}

const effectivePrimaryUserRoleNameQuery = `
WITH RECURSIVE seed_roles AS (
	SELECT ur.role_id
	FROM _user_role ur
	WHERE ur.user_id = $1
	  AND ur.app_id = ''
	UNION
	SELECT gr.role_id
	FROM _group_membership gm
	JOIN _group_role gr ON gr.group_id = gm.group_id
	WHERE gm.user_id = $1
	  AND gr.app_id = ''
),
effective_roles AS (
	SELECT sr.role_id
	FROM seed_roles sr
	UNION
	SELECT ri.inherits_role_id
	FROM effective_roles er
	JOIN _role_inheritance ri ON ri.role_id = er.role_id
)
SELECT COALESCE((
	SELECT r.name
	FROM effective_roles er
	JOIN _role r ON r._id = er.role_id
	ORDER BY r.priority ASC, r.name ASC
	LIMIT 1
), 'viewer')
`
