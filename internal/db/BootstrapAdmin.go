package db

import (
	"context"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"
)

func BootstrapAdminByEmail(ctx context.Context, email string) error {
	email = normalizeBootstrapUserEmail(email)
	if email == "" {
		return fmt.Errorf("email is required")
	}

	tx, err := Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	user, found, err := findUniqueUserByEmailTx(ctx, tx, email)
	if err != nil {
		return err
	}
	if !found {
		return fmt.Errorf("user not found for email %q", email)
	}

	if err := grantGlobalAdminByUserIDTx(ctx, tx, user.ID, email); err != nil {
		return err
	}

	return tx.Commit(ctx)
}

type bootstrapUserRecord struct {
	ID    string
	Name  string
	Email string
}

func normalizeBootstrapUserEmail(email string) string {
	return strings.TrimSpace(strings.ToLower(email))
}

func findUniqueUserByEmailTx(ctx context.Context, tx pgx.Tx, email string) (bootstrapUserRecord, bool, error) {
	rows, err := tx.Query(ctx, `
		SELECT _id::text, name, email
		FROM _user
		WHERE LOWER(email) = $1
		ORDER BY _created_at ASC, _id::text ASC
	`, email)
	if err != nil {
		return bootstrapUserRecord{}, false, err
	}
	defer rows.Close()

	var matches []bootstrapUserRecord
	for rows.Next() {
		var user bootstrapUserRecord
		if err := rows.Scan(&user.ID, &user.Name, &user.Email); err != nil {
			return bootstrapUserRecord{}, false, err
		}
		matches = append(matches, user)
	}
	if err := rows.Err(); err != nil {
		return bootstrapUserRecord{}, false, err
	}
	if len(matches) == 0 {
		return bootstrapUserRecord{}, false, nil
	}
	if len(matches) > 1 {
		return bootstrapUserRecord{}, false, fmt.Errorf("multiple users found for email %q", email)
	}
	return matches[0], true, nil
}

func adminRoleIDTx(ctx context.Context, tx pgx.Tx) (string, error) {
	var adminRoleID string
	if err := tx.QueryRow(ctx, `SELECT _id::text FROM _role WHERE name = 'admin' LIMIT 1`).Scan(&adminRoleID); err != nil {
		return "", fmt.Errorf("admin role not found")
	}
	return adminRoleID, nil
}

func grantGlobalAdminByUserIDTx(ctx context.Context, tx pgx.Tx, userID, email string) error {
	adminRoleID, err := adminRoleIDTx(ctx, tx)
	if err != nil {
		return err
	}

	if _, err := tx.Exec(ctx, `
		INSERT INTO _user_role (user_id, role_id, app_id)
		VALUES ($1, $2, '')
		ON CONFLICT DO NOTHING
	`, userID, adminRoleID); err != nil {
		return err
	}

	if _, err := tx.Exec(ctx, `
		INSERT INTO _bootstrap_admin (user_id, email)
		VALUES ($1, $2)
		ON CONFLICT (user_id)
		DO UPDATE SET
			email = EXCLUDED.email,
			bootstrapped_at = NOW()
	`, userID, email); err != nil {
		return err
	}

	return nil
}
