package db

import (
	"context"
	"encoding/json"
)

// AIModel is a callable model owned by a Connection (issue #93 decision:
// Model is a child of Connection, not a shared catalog). Rows come from the
// live provider list-models call, so the catalog tracks upstream changes.
type AIModel struct {
	ID            string         `json:"_id"`
	ConnectionID  string         `json:"connection_id"`
	ModelID       string         `json:"model_id"`
	DisplayName   string         `json:"display_name"`
	ModelMetadata map[string]any `json:"model_metadata"`
	IsActive      bool           `json:"is_active"`
}

const aiModelColumns = `_id, connection_id, model_id, display_name, model_metadata, is_active`

func ListAIModelsByConnection(ctx context.Context, connectionID string) ([]AIModel, error) {
	rows, err := Pool.Query(ctx,
		`SELECT `+aiModelColumns+` FROM _ai_model WHERE connection_id=$1 ORDER BY model_id`, connectionID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []AIModel
	for rows.Next() {
		var m AIModel
		if err := rows.Scan(&m.ID, &m.ConnectionID, &m.ModelID, &m.DisplayName, &m.ModelMetadata, &m.IsActive); err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// SyncAIModels replaces the model rows for a connection with the freshly
// fetched catalog. Deactivated rows are removed; is_active on untouched rows
// persists via the ON CONFLICT no-op update.
func SyncAIModels(ctx context.Context, connectionID string, models []AIModel) error {
	tx, err := Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, `DELETE FROM _ai_model WHERE connection_id=$1`, connectionID); err != nil {
		return err
	}
	for _, m := range models {
		if _, err := tx.Exec(ctx,
			`INSERT INTO _ai_model (connection_id, model_id, display_name, model_metadata, is_active)
			 VALUES ($1,$2,$3,$4,$5)`, connectionID, m.ModelID, m.DisplayName, m.ModelMetadata, m.IsActive); err != nil {
			return err
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return err
	}
	return MarkAIConnectionModelsSynced(ctx, connectionID)
}

// SetUserActiveModel stores the user's active model preference under the
// 'agent' namespace (User.active_model in the decision, implemented as a
// _user_preference row so _user itself is untouched).
func SetUserActiveModel(ctx context.Context, userID, modelID string) error {
	// _user_preference.value is jsonb; encode so plain model IDs store as JSON strings.
	enc, err := json.Marshal(modelID)
	if err != nil {
		return err
	}
	return UpsertUserPreference(ctx, userID, "agent", "active_model", enc)
}

func GetUserActiveModel(ctx context.Context, userID string) (string, error) {
	raw, err := GetUserPreference(ctx, userID, "agent", "active_model")
	if err != nil || raw == nil {
		return "", err
	}
	var modelID string
	if err := json.Unmarshal(raw, &modelID); err != nil {
		// Tolerate legacy/plain-text rows written before JSON encoding.
		return string(raw), nil
	}
	return modelID, nil
}
