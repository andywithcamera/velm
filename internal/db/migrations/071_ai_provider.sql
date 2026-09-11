-- AI provider catalog: endpoint templates + provider-specific metadata.
-- Catalog data only -- NO secrets here. Secrets live on _ai_connection.
-- Decisions on issue #93 (2026-09-11): providers seeded; model catalogs are
-- NOT seeded -- _ai_model rows are populated from a live provider call
-- ("list models") when a Connection is created/refreshed.
CREATE TABLE IF NOT EXISTS _ai_provider (
	_id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
	slug TEXT NOT NULL UNIQUE,          -- 'anthropic', 'openai'
	display_name TEXT NOT NULL,
	base_url TEXT NOT NULL,             -- API root, e.g. https://api.anthropic.com
	api_style TEXT NOT NULL,            -- wire protocol: 'anthropic', 'openai'
	default_list_models_path TEXT NOT NULL DEFAULT '',
	is_active BOOLEAN NOT NULL DEFAULT TRUE,
	provider_metadata JSONB NOT NULL DEFAULT '{}'::jsonb,
	_created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
	_updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

INSERT INTO _ai_provider (slug, display_name, base_url, api_style, default_list_models_path, provider_metadata)
VALUES
	('anthropic', 'Anthropic', 'https://api.anthropic.com', 'anthropic', '/v1/models',
	 '{"auth_header":"x-api-key","version_header":"anthropic-version","version_value":"2023-06-01"}'::jsonb),
	('openai', 'OpenAI', 'https://api.openai.com', 'openai', '/v1/models',
	 '{"auth_header":"Authorization","auth_scheme":"Bearer"}'::jsonb)
ON CONFLICT (slug) DO NOTHING;
