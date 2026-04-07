package db

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"
)

func EnsureAuthzSchema() error {
	if Pool == nil {
		return fmt.Errorf("database pool is not initialized")
	}

	ctx := context.Background()
	tx, err := Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	statements := []string{
		`CREATE EXTENSION IF NOT EXISTS pgcrypto`,
		`CREATE TABLE IF NOT EXISTS _role (
			_id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
			name TEXT NOT NULL UNIQUE,
			description TEXT,
			is_system BOOLEAN NOT NULL DEFAULT TRUE,
			priority INTEGER NOT NULL DEFAULT 100,
			_created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			_updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
		)`,
		`CREATE TABLE IF NOT EXISTS _permission (
			_id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
			resource TEXT NOT NULL,
			action TEXT NOT NULL,
			scope TEXT NOT NULL DEFAULT 'global',
			description TEXT,
			_created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			_updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			UNIQUE(resource, action, scope)
		)`,
		`CREATE TABLE IF NOT EXISTS _role_permission (
			role_id UUID NOT NULL REFERENCES _role(_id) ON DELETE CASCADE,
			permission_id UUID NOT NULL REFERENCES _permission(_id) ON DELETE CASCADE,
			_created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			PRIMARY KEY (role_id, permission_id)
		)`,
		`CREATE TABLE IF NOT EXISTS _user_role (
			user_id TEXT NOT NULL,
			role_id UUID NOT NULL REFERENCES _role(_id) ON DELETE CASCADE,
			app_id TEXT NOT NULL DEFAULT '',
			_created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			PRIMARY KEY (user_id, role_id, app_id)
		)`,
		`CREATE INDEX IF NOT EXISTS idx_user_role_user_id ON _user_role(user_id)`,
		`CREATE INDEX IF NOT EXISTS idx_permission_lookup ON _permission(resource, action, scope)`,
		`UPDATE _user_role SET app_id = '' WHERE app_id IS NULL`,
	}

	for _, stmt := range statements {
		if _, err := tx.Exec(ctx, stmt); err != nil {
			return err
		}
	}

	if err := seedAuthzData(ctx, tx); err != nil {
		return err
	}

	return tx.Commit(ctx)
}

func seedAuthzData(ctx context.Context, tx pgx.Tx) error {
	roleStatements := []struct {
		name        string
		description string
		priority    int
	}{
		{name: "admin", description: "Full access", priority: 10},
		{name: "operator", description: "Read/write access", priority: 20},
		{name: "viewer", description: "Read-only access", priority: 30},
	}

	for _, role := range roleStatements {
		_, err := tx.Exec(ctx, `
			INSERT INTO _role (name, description, is_system, priority)
			VALUES ($1, $2, TRUE, $3)
			ON CONFLICT (name)
			DO UPDATE SET
				description = EXCLUDED.description,
				priority = EXCLUDED.priority
		`, role.name, role.description, role.priority)
		if err != nil {
			return err
		}
	}

	permissionStatements := []struct {
		resource    string
		action      string
		scope       string
		description string
	}{
		{resource: "platform", action: "view", scope: "global", description: "View platform content"},
		{resource: "platform", action: "write", scope: "global", description: "Create and update platform content"},
		{resource: "platform", action: "admin", scope: "global", description: "Administrative platform actions"},
	}

	for _, permission := range permissionStatements {
		_, err := tx.Exec(ctx, `
			INSERT INTO _permission (resource, action, scope, description)
			VALUES ($1, $2, $3, $4)
			ON CONFLICT (resource, action, scope)
			DO UPDATE SET description = EXCLUDED.description
		`, permission.resource, permission.action, permission.scope, permission.description)
		if err != nil {
			return err
		}
	}

	mappingStatements := []struct {
		roleName string
		action   string
	}{
		{roleName: "admin", action: "view"},
		{roleName: "admin", action: "write"},
		{roleName: "admin", action: "admin"},
		{roleName: "operator", action: "view"},
		{roleName: "operator", action: "write"},
		{roleName: "viewer", action: "view"},
	}

	for _, mapping := range mappingStatements {
		_, err := tx.Exec(ctx, `
			INSERT INTO _role_permission (role_id, permission_id)
			SELECT r._id, p._id
			FROM _role r
			JOIN _permission p
			  ON p.resource = 'platform'
			 AND p.action = $2
			 AND p.scope = 'global'
			WHERE r.name = $1
			ON CONFLICT DO NOTHING
		`, mapping.roleName, mapping.action)
		if err != nil {
			return err
		}
	}

	_, err := tx.Exec(ctx, `
		DO $$
		BEGIN
			IF to_regclass('_user') IS NOT NULL THEN
				INSERT INTO _user_role (user_id, role_id, app_id)
				SELECT u._id::text, r._id, ''
				FROM _user u
				JOIN _role r ON r.name = 'admin'
				WHERE NOT EXISTS (
					SELECT 1
					FROM _user_role ur
					WHERE ur.user_id = u._id::text
				);
			END IF;
		END
		$$
	`)
	return err
}
