package db

import (
	"context"

	"github.com/jackc/pgx/v5"
)

// AIProvider is a catalog row describing an external model API surface
// (endpoint, wire style, auth header conventions). No secrets live here.
type AIProvider struct {
	ID                   string `json:"_id"`
	Slug                 string `json:"slug"`
	DisplayName          string `json:"display_name"`
	BaseURL              string `json:"base_url"`
	APIStyle             string `json:"api_style"`
	DefaultListModelsPath string `json:"default_list_models_path"`
	IsActive             bool   `json:"is_active"`
	ProviderMetadata     map[string]any `json:"provider_metadata"`
}

const aiProviderColumns = `_id, slug, display_name, base_url, api_style, default_list_models_path, is_active, provider_metadata`

func scanAIProvider(row pgx.Row) (*AIProvider, error) {
	var p AIProvider
	err := row.Scan(&p.ID, &p.Slug, &p.DisplayName, &p.BaseURL, &p.APIStyle,
		&p.DefaultListModelsPath, &p.IsActive, &p.ProviderMetadata)
	if err != nil {
		return nil, err
	}
	return &p, nil
}

func ListAIProviders(ctx context.Context) ([]AIProvider, error) {
	rows, err := Pool.Query(ctx, `SELECT `+aiProviderColumns+` FROM _ai_provider WHERE is_active ORDER BY slug`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []AIProvider
	for rows.Next() {
		var p AIProvider
		if err := rows.Scan(&p.ID, &p.Slug, &p.DisplayName, &p.BaseURL, &p.APIStyle,
			&p.DefaultListModelsPath, &p.IsActive, &p.ProviderMetadata); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

func GetAIProvider(ctx context.Context, id string) (*AIProvider, error) {
	p, err := scanAIProvider(Pool.QueryRow(ctx,
		`SELECT `+aiProviderColumns+` FROM _ai_provider WHERE _id = $1`, id))
	if err != nil {
		return nil, err
	}
	return p, nil
}
