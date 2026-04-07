CREATE TABLE IF NOT EXISTS _saved_view (
	_id BIGSERIAL PRIMARY KEY,
	app_id TEXT NOT NULL DEFAULT '',
	table_name TEXT NOT NULL,
	name TEXT NOT NULL,
	visibility TEXT NOT NULL DEFAULT 'private',
	owner_user_id TEXT NOT NULL,
	state JSONB NOT NULL,
	_created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
	_updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
	CONSTRAINT chk_saved_view_visibility CHECK (visibility IN ('private', 'app'))
);

CREATE UNIQUE INDEX IF NOT EXISTS uq_saved_view_owner_name
	ON _saved_view(app_id, table_name, visibility, owner_user_id, name);

CREATE INDEX IF NOT EXISTS idx_saved_view_lookup
	ON _saved_view(app_id, table_name, visibility, owner_user_id);
