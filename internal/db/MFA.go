package db

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

const (
	AuthFactorTypeTOTP       = "totp"
	AuthFactorStatusPending  = "pending"
	AuthFactorStatusActive   = "active"
	AuthFactorStatusDisabled = "disabled"
)

type UserAuthFactor struct {
	ID          string
	UserID      string
	FactorType  string
	Label       string
	Status      string
	SecretEnc   string
	ConfirmedAt *time.Time
	LastUsedAt  *time.Time
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

type UserRecoveryCode struct {
	ID        string
	FactorID  string
	UserID    string
	CodeHash  string
	UsedAt    *time.Time
	CreatedAt time.Time
	UpdatedAt time.Time
}

func ListUserAuthFactors(ctx context.Context, userID string) ([]UserAuthFactor, error) {
	if Pool == nil {
		return nil, fmt.Errorf("database pool is not initialized")
	}

	rows, err := Pool.Query(ctx, `
		SELECT _id, user_id, factor_type, label, status, secret_enc, confirmed_at, last_used_at, _created_at, _updated_at
		FROM _user_auth_factor
		WHERE user_id = $1 AND _deleted_at IS NULL
		ORDER BY _created_at ASC
	`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var factors []UserAuthFactor
	for rows.Next() {
		factor, err := scanUserAuthFactor(rows)
		if err != nil {
			return nil, err
		}
		factors = append(factors, factor)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return factors, nil
}

func GetActiveTOTPFactor(ctx context.Context, userID string) (*UserAuthFactor, error) {
	if Pool == nil {
		return nil, fmt.Errorf("database pool is not initialized")
	}

	row := Pool.QueryRow(ctx, `
		SELECT _id, user_id, factor_type, label, status, secret_enc, confirmed_at, last_used_at, _created_at, _updated_at
		FROM _user_auth_factor
		WHERE user_id = $1
		  AND factor_type = $2
		  AND status = $3
		  AND _deleted_at IS NULL
		ORDER BY _created_at DESC
		LIMIT 1
	`, userID, AuthFactorTypeTOTP, AuthFactorStatusActive)

	factor, err := scanUserAuthFactor(row)
	if err != nil {
		if err == pgx.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}
	return &factor, nil
}

func CreatePendingTOTPFactor(ctx context.Context, userID, label, secretEnc string) (*UserAuthFactor, error) {
	if Pool == nil {
		return nil, fmt.Errorf("database pool is not initialized")
	}

	row := Pool.QueryRow(ctx, `
		INSERT INTO _user_auth_factor (user_id, factor_type, label, status, secret_enc)
		VALUES ($1, $2, $3, $4, $5)
		RETURNING _id, user_id, factor_type, label, status, secret_enc, confirmed_at, last_used_at, _created_at, _updated_at
	`, userID, AuthFactorTypeTOTP, label, AuthFactorStatusPending, secretEnc)

	factor, err := scanUserAuthFactor(row)
	if err != nil {
		return nil, err
	}
	return &factor, nil
}

func ActivateTOTPFactor(ctx context.Context, factorID, userID string) error {
	if Pool == nil {
		return fmt.Errorf("database pool is not initialized")
	}

	tx, err := Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if _, err := tx.Exec(ctx, `
		UPDATE _user_auth_factor
		SET status = $3,
			confirmed_at = NOW(),
			_updated_at = NOW()
		WHERE _id = $1
		  AND user_id = $2
		  AND _deleted_at IS NULL
	`, factorID, userID, AuthFactorStatusActive); err != nil {
		return err
	}

	if _, err := tx.Exec(ctx, `
		UPDATE _user_auth_factor
		SET status = $3,
			_updated_at = NOW()
		WHERE user_id = $1
		  AND factor_type = $2
		  AND _id <> $4
		  AND status <> $3
		  AND _deleted_at IS NULL
	`, userID, AuthFactorTypeTOTP, AuthFactorStatusDisabled, factorID); err != nil {
		return err
	}

	return tx.Commit(ctx)
}

func DisableUserTOTPFactors(ctx context.Context, userID string) error {
	if Pool == nil {
		return fmt.Errorf("database pool is not initialized")
	}

	_, err := Pool.Exec(ctx, `
		UPDATE _user_auth_factor
		SET status = $2,
			_updated_at = NOW()
		WHERE user_id = $1
		  AND factor_type = $3
		  AND status <> $2
		  AND _deleted_at IS NULL
	`, userID, AuthFactorStatusDisabled, AuthFactorTypeTOTP)
	return err
}

func TouchAuthFactorLastUsed(ctx context.Context, factorID string) error {
	if Pool == nil {
		return fmt.Errorf("database pool is not initialized")
	}

	_, err := Pool.Exec(ctx, `
		UPDATE _user_auth_factor
		SET last_used_at = NOW(),
			_updated_at = NOW()
		WHERE _id = $1
		  AND _deleted_at IS NULL
	`, factorID)
	return err
}

func ReplaceRecoveryCodes(ctx context.Context, factorID, userID string, codeHashes []string) error {
	if Pool == nil {
		return fmt.Errorf("database pool is not initialized")
	}

	tx, err := Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if _, err := tx.Exec(ctx, `DELETE FROM _user_recovery_code WHERE factor_id = $1`, factorID); err != nil {
		return err
	}

	for _, codeHash := range codeHashes {
		if _, err := tx.Exec(ctx, `
			INSERT INTO _user_recovery_code (factor_id, user_id, code_hash)
			VALUES ($1, $2, $3)
		`, factorID, userID, codeHash); err != nil {
			return err
		}
	}

	return tx.Commit(ctx)
}

func ConsumeRecoveryCode(ctx context.Context, factorID, userID, codeHash string) (bool, error) {
	if Pool == nil {
		return false, fmt.Errorf("database pool is not initialized")
	}

	tag, err := Pool.Exec(ctx, `
		UPDATE _user_recovery_code
		SET used_at = NOW(),
			_updated_at = NOW()
		WHERE factor_id = $1
		  AND user_id = $2
		  AND code_hash = $3
		  AND used_at IS NULL
	`, factorID, userID, codeHash)
	if err != nil {
		return false, err
	}
	return tag.RowsAffected() == 1, nil
}

type authFactorScanner interface {
	Scan(dest ...any) error
}

func scanUserAuthFactor(scanner authFactorScanner) (UserAuthFactor, error) {
	var factor UserAuthFactor
	var confirmedAt, lastUsedAt *time.Time
	err := scanner.Scan(
		&factor.ID,
		&factor.UserID,
		&factor.FactorType,
		&factor.Label,
		&factor.Status,
		&factor.SecretEnc,
		&confirmedAt,
		&lastUsedAt,
		&factor.CreatedAt,
		&factor.UpdatedAt,
	)
	if err != nil {
		return UserAuthFactor{}, err
	}
	factor.ConfirmedAt = confirmedAt
	factor.LastUsedAt = lastUsedAt
	return factor, nil
}
