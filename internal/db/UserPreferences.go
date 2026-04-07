package db

import (
	"context"

	"github.com/jackc/pgx/v5"
)

func GetUserPreference(ctx context.Context, userID, namespace, key string) ([]byte, error) {
	var raw []byte
	err := Pool.QueryRow(
		ctx,
		`SELECT pref_value::text FROM _user_preference WHERE user_id = $1 AND namespace = $2 AND pref_key = $3`,
		userID,
		namespace,
		key,
	).Scan(&raw)
	if err != nil {
		if err == pgx.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}
	return raw, nil
}

func UpsertUserPreference(ctx context.Context, userID, namespace, key string, value []byte) error {
	_, err := Pool.Exec(
		ctx,
		`
			INSERT INTO _user_preference (user_id, namespace, pref_key, pref_value)
			VALUES ($1, $2, $3, $4::jsonb)
			ON CONFLICT (user_id, namespace, pref_key)
			DO UPDATE SET
				pref_value = EXCLUDED.pref_value,
				_updated_at = NOW()
		`,
		userID,
		namespace,
		key,
		string(value),
	)
	return err
}
