package db

import (
	"context"
	"fmt"
	"strings"
	"time"
)

// AIConnection is a per-account credential bound to a provider. The API key
// is stored encrypted (see issue #102: generalise MFASecretCipher). Only the
// fingerprint prefix is ever exposed outside this package.
type AIConnection struct {
	ID                string         `json:"_id"`
	ProviderID        string         `json:"provider_id"`
	Label             string         `json:"label"`
	APIKeyFingerprint string         `json:"api_key_fingerprint"`
	AccountInfo       map[string]any `json:"account_info"`
	IsActive          bool           `json:"is_active"`
	LastVerifiedAt    *time.Time     `json:"last_verified_at"`
	ModelsSyncedAt    *time.Time     `json:"models_synced_at"`
	// PinnedBaseURL is the provider endpoint in force when the key was last
	// entered. The key is only ever sent here: changing _ai_provider.base_url
	// does not move an existing credential to a new destination.
	PinnedBaseURL string `json:"pinned_base_url"`
}

const aiConnectionColumns = `_id, provider_id, label, api_key_fingerprint, account_info, is_active, last_verified_at, models_synced_at, pinned_base_url`

func scanAIConnection(row pgxRow) (*AIConnection, error) {
	var c AIConnection
	err := row.Scan(&c.ID, &c.ProviderID, &c.Label, &c.APIKeyFingerprint,
		&c.AccountInfo, &c.IsActive, &c.LastVerifiedAt, &c.ModelsSyncedAt, &c.PinnedBaseURL)
	if err != nil {
		return nil, err
	}
	return &c, nil
}

// pgxRow abstracts *pgx.Row so tests can stub scanning. Kept minimal.
type pgxRow interface{ Scan(dest ...any) error }

func ListAIConnections(ctx context.Context) ([]AIConnection, error) {
	rows, err := Pool.Query(ctx, `SELECT `+aiConnectionColumns+` FROM _ai_connection ORDER BY _created_at`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []AIConnection
	for rows.Next() {
		var c AIConnection
		if err := rows.Scan(&c.ID, &c.ProviderID, &c.Label, &c.APIKeyFingerprint,
			&c.AccountInfo, &c.IsActive, &c.LastVerifiedAt, &c.ModelsSyncedAt, &c.PinnedBaseURL); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

func GetAIConnection(ctx context.Context, id string) (*AIConnection, error) {
	return scanAIConnection(Pool.QueryRow(ctx,
		`SELECT `+aiConnectionColumns+` FROM _ai_connection WHERE _id = $1`, id))
}

// CreateAIConnection stores a key already encrypted, pinned to the destination
// it was entered for. pinnedBaseURL must be the provider endpoint validated at
// the point of entry, not one read back later.
func CreateAIConnection(ctx context.Context, providerID, label, encryptedKey, fingerprint, pinnedBaseURL string) (*AIConnection, error) {
	var id string
	if err := Pool.QueryRow(ctx,
		`INSERT INTO _ai_connection (provider_id, label, api_key_enc, api_key_fingerprint, pinned_base_url)
		 VALUES ($1, $2, $3, $4, $5) RETURNING _id`, providerID, label, encryptedKey, fingerprint, pinnedBaseURL).Scan(&id); err != nil {
		return nil, err
	}
	return GetAIConnection(ctx, id)
}

// UpdateAIConnection applies only the supplied fields; nil leaves a column
// unchanged so a key-only PATCH does not wipe the label. When a key is
// supplied the destination is re-pinned with it: entering a key authorises the
// endpoint, so the two columns never move independently.
func UpdateAIConnection(ctx context.Context, id string, label *string, encryptedKey, fingerprint, pinnedBaseURL *string) error {
	sets := []string{"_updated_at=NOW()"}
	args := []any{id}
	add := func(column string, value string) {
		args = append(args, value)
		sets = append(sets, fmt.Sprintf("%s=$%d", column, len(args)))
	}
	if label != nil {
		add("label", *label)
	}
	if encryptedKey != nil {
		add("api_key_enc", *encryptedKey)
	}
	if fingerprint != nil {
		add("api_key_fingerprint", *fingerprint)
	}
	if pinnedBaseURL != nil {
		add("pinned_base_url", *pinnedBaseURL)
	}
	_, err := Pool.Exec(ctx,
		`UPDATE _ai_connection SET `+strings.Join(sets, ", ")+` WHERE _id=$1`, args...)
	return err
}

func DeleteAIConnection(ctx context.Context, id string) error {
	_, err := Pool.Exec(ctx, `DELETE FROM _ai_connection WHERE _id=$1`, id)
	return err
}

// SetAIConnectionKey replaces the key material and re-pins the destination.
func SetAIConnectionKey(ctx context.Context, id, encryptedKey, fingerprint, pinnedBaseURL string) error {
	_, err := Pool.Exec(ctx,
		`UPDATE _ai_connection
		 SET api_key_enc=$2, api_key_fingerprint=$3, pinned_base_url=$4, _updated_at=NOW()
		 WHERE _id=$1`,
		id, encryptedKey, fingerprint, pinnedBaseURL)
	return err
}

// GetAIConnectionKey returns the encrypted key material for a connection.
// Callers are responsible for decryption and must never log the result.
func GetAIConnectionKey(ctx context.Context, id string) (string, error) {
	var enc string
	err := Pool.QueryRow(ctx, `SELECT api_key_enc FROM _ai_connection WHERE _id=$1`, id).Scan(&enc)
	return enc, err
}

func MarkAIConnectionVerified(ctx context.Context, id string) error {
	_, err := Pool.Exec(ctx, `UPDATE _ai_connection SET last_verified_at=NOW(), _updated_at=NOW() WHERE _id=$1`, id)
	return err
}

func MarkAIConnectionModelsSynced(ctx context.Context, id string) error {
	_, err := Pool.Exec(ctx, `UPDATE _ai_connection SET models_synced_at=NOW(), _updated_at=NOW() WHERE _id=$1`, id)
	return err
}
