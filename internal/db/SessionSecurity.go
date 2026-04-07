package db

import "context"

func GetOrInitUserSessionVersion(ctx context.Context, userID string) (int, error) {
	if _, err := Pool.Exec(ctx, `
		INSERT INTO _user_security_state (user_id, session_version)
		VALUES ($1, 1)
		ON CONFLICT (user_id) DO NOTHING
	`, userID); err != nil {
		return 0, err
	}

	var version int
	if err := Pool.QueryRow(ctx, `
		SELECT session_version
		FROM _user_security_state
		WHERE user_id = $1
	`, userID).Scan(&version); err != nil {
		return 0, err
	}
	return version, nil
}

func BumpUserSessionVersion(ctx context.Context, userID string) error {
	_, err := Pool.Exec(ctx, `
		INSERT INTO _user_security_state (user_id, session_version)
		VALUES ($1, 2)
		ON CONFLICT (user_id)
		DO UPDATE SET
			session_version = _user_security_state.session_version + 1,
			_updated_at = NOW()
	`, userID)
	return err
}

func BumpSessionVersionForRole(ctx context.Context, roleID string) error {
	_, err := Pool.Exec(ctx, impactedUserIDsForRoleCTE+`
		INSERT INTO _user_security_state (user_id, session_version)
		SELECT iu.user_id, 2
		FROM impacted_users iu
		ON CONFLICT (user_id)
		DO UPDATE SET
			session_version = _user_security_state.session_version + 1,
			_updated_at = NOW()
	`, roleID)
	return err
}
