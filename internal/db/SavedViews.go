package db

import (
	"context"
	"encoding/json"
)

type SavedView struct {
	ID         int64           `json:"id"`
	Name       string          `json:"name"`
	Visibility string          `json:"visibility"`
	OwnerUser  string          `json:"ownerUserId"`
	State      json.RawMessage `json:"state"`
}

func ListSavedViews(ctx context.Context, userID, appID, tableName string) ([]SavedView, error) {
	rows, err := Pool.Query(
		ctx,
		`
			SELECT _id, name, visibility, owner_user_id, state::text
			FROM _saved_view
			WHERE table_name = $1
			  AND app_id = $2
			  AND (
				(visibility = 'private' AND owner_user_id = $3)
				OR visibility = 'app'
			  )
			ORDER BY visibility DESC, name ASC
		`,
		tableName,
		appID,
		userID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var views []SavedView
	for rows.Next() {
		var item SavedView
		if err := rows.Scan(&item.ID, &item.Name, &item.Visibility, &item.OwnerUser, &item.State); err != nil {
			return nil, err
		}
		views = append(views, item)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return views, nil
}

func UpsertSavedView(ctx context.Context, appID, tableName, name, visibility, ownerUserID string, state []byte) error {
	_, err := Pool.Exec(
		ctx,
		`
			INSERT INTO _saved_view (app_id, table_name, name, visibility, owner_user_id, state)
			VALUES ($1, $2, $3, $4, $5, $6::jsonb)
			ON CONFLICT (app_id, table_name, visibility, owner_user_id, name)
			DO UPDATE SET
				state = EXCLUDED.state,
				_updated_at = NOW()
		`,
		appID,
		tableName,
		name,
		visibility,
		ownerUserID,
		string(state),
	)
	return err
}

func DeleteSavedView(ctx context.Context, id int64, ownerUserID string) error {
	_, err := Pool.Exec(
		ctx,
		`DELETE FROM _saved_view WHERE _id = $1 AND owner_user_id = $2`,
		id,
		ownerUserID,
	)
	return err
}
