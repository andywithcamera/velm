-- AI model: a callable model ID owned by a Connection (decision 2026-09-06:
-- Model is a CHILD of Connection, not a shared catalog item). Rows are
-- populated from a live provider list-models call, so they self-update.
CREATE TABLE IF NOT EXISTS _ai_model (
	_id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
	connection_id UUID NOT NULL REFERENCES _ai_connection(_id) ON DELETE CASCADE,
	model_id TEXT NOT NULL,             -- provider-facing ID, e.g. 'claude-sonnet-4-5'
	display_name TEXT NOT NULL DEFAULT '',
	model_metadata JSONB NOT NULL DEFAULT '{}'::jsonb,
	is_active BOOLEAN NOT NULL DEFAULT TRUE,
	_created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
	_updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
	UNIQUE (connection_id, model_id)
);

CREATE INDEX IF NOT EXISTS idx_ai_model_connection_id
	ON _ai_model(connection_id);
