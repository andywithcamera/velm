package db

import (
	"context"
	"fmt"
	"strings"
	"unicode"

	"golang.org/x/crypto/bcrypt"
)

type BootstrapUserInput struct {
	Email      string
	Name       string
	Password   string
	GrantAdmin bool
}

type BootstrapUserResult struct {
	UserID       string
	Email        string
	Name         string
	Created      bool
	GrantedAdmin bool
}

const bootstrapFirstUserLockKey int64 = 0x56454c4d55534552

func BootstrapUser(ctx context.Context, input BootstrapUserInput) (BootstrapUserResult, error) {
	input, err := normalizeBootstrapUserInput(input)
	if err != nil {
		return BootstrapUserResult{}, err
	}

	passwordHash, err := bcrypt.GenerateFromPassword([]byte(input.Password), bcrypt.DefaultCost)
	if err != nil {
		return BootstrapUserResult{}, fmt.Errorf("hash password: %w", err)
	}

	tx, err := Pool.Begin(ctx)
	if err != nil {
		return BootstrapUserResult{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	existingUser, found, err := findUniqueUserByEmailTx(ctx, tx, input.Email)
	if err != nil {
		return BootstrapUserResult{}, err
	}

	result := BootstrapUserResult{
		Email:        input.Email,
		Name:         input.Name,
		GrantedAdmin: input.GrantAdmin,
	}

	if found {
		result.UserID = existingUser.ID
		if _, err := tx.Exec(ctx, `
			UPDATE _user
			SET name = $1,
				email = $2,
				password_hash = $3,
				_updated_at = NOW()
			WHERE _id::text = $4
		`, input.Name, input.Email, string(passwordHash), existingUser.ID); err != nil {
			return BootstrapUserResult{}, fmt.Errorf("update user %q: %w", input.Email, err)
		}
	} else {
		result.Created = true
		if err := tx.QueryRow(ctx, `
			INSERT INTO _user (name, email, password_hash)
			VALUES ($1, $2, $3)
			RETURNING _id::text
		`, input.Name, input.Email, string(passwordHash)).Scan(&result.UserID); err != nil {
			return BootstrapUserResult{}, fmt.Errorf("create user %q: %w", input.Email, err)
		}
	}

	if input.GrantAdmin {
		if err := grantGlobalAdminByUserIDTx(ctx, tx, result.UserID, input.Email); err != nil {
			return BootstrapUserResult{}, err
		}
	}

	if err := tx.Commit(ctx); err != nil {
		return BootstrapUserResult{}, err
	}
	return result, nil
}

func BootstrapFirstUser(ctx context.Context, input BootstrapUserInput) (BootstrapUserResult, bool, error) {
	input, err := normalizeBootstrapUserInput(input)
	if err != nil {
		return BootstrapUserResult{}, false, err
	}

	passwordHash, err := bcrypt.GenerateFromPassword([]byte(input.Password), bcrypt.DefaultCost)
	if err != nil {
		return BootstrapUserResult{}, false, fmt.Errorf("hash password: %w", err)
	}

	tx, err := Pool.Begin(ctx)
	if err != nil {
		return BootstrapUserResult{}, false, err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock($1)`, bootstrapFirstUserLockKey); err != nil {
		return BootstrapUserResult{}, false, fmt.Errorf("lock bootstrap first user: %w", err)
	}

	var hasUsers bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM _user LIMIT 1)`).Scan(&hasUsers); err != nil {
		return BootstrapUserResult{}, false, fmt.Errorf("check for existing users: %w", err)
	}
	if hasUsers {
		return BootstrapUserResult{}, false, nil
	}

	result := BootstrapUserResult{
		Email:        input.Email,
		Name:         input.Name,
		Created:      true,
		GrantedAdmin: input.GrantAdmin,
	}

	if err := tx.QueryRow(ctx, `
		INSERT INTO _user (name, email, password_hash)
		VALUES ($1, $2, $3)
		RETURNING _id::text
	`, input.Name, input.Email, string(passwordHash)).Scan(&result.UserID); err != nil {
		return BootstrapUserResult{}, false, fmt.Errorf("create first user %q: %w", input.Email, err)
	}

	if input.GrantAdmin {
		if err := grantGlobalAdminByUserIDTx(ctx, tx, result.UserID, input.Email); err != nil {
			return BootstrapUserResult{}, false, err
		}
	}

	if err := tx.Commit(ctx); err != nil {
		return BootstrapUserResult{}, false, err
	}
	return result, true, nil
}

func normalizeBootstrapUserInput(input BootstrapUserInput) (BootstrapUserInput, error) {
	input.Email = normalizeBootstrapUserEmail(input.Email)
	input.Name = strings.TrimSpace(input.Name)
	if input.Email == "" {
		return BootstrapUserInput{}, fmt.Errorf("email is required")
	}
	if input.Password == "" {
		return BootstrapUserInput{}, fmt.Errorf("password is required")
	}
	if input.Name == "" {
		input.Name = defaultBootstrapUserName(input.Email)
	}
	return input, nil
}

func defaultBootstrapUserName(email string) string {
	localPart := email
	if at := strings.Index(localPart, "@"); at > 0 {
		localPart = localPart[:at]
	}

	parts := strings.FieldsFunc(localPart, func(r rune) bool {
		return r == '.' || r == '_' || r == '-'
	})
	if len(parts) == 0 {
		return email
	}

	normalizedParts := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		runes := []rune(part)
		runes[0] = unicode.ToUpper(runes[0])
		normalizedParts = append(normalizedParts, string(runes))
	}
	if len(normalizedParts) == 0 {
		return email
	}
	return strings.Join(normalizedParts, " ")
}
