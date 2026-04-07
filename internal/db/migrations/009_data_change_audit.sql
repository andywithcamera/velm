CREATE TABLE IF NOT EXISTS _audit_data_change (
	_id BIGSERIAL PRIMARY KEY,
	_created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
	user_id TEXT NOT NULL,
	table_name TEXT NOT NULL,
	record_id TEXT NOT NULL,
	operation TEXT NOT NULL,
	field_diff JSONB NOT NULL DEFAULT '{}'::jsonb,
	old_values JSONB NOT NULL DEFAULT '{}'::jsonb,
	new_values JSONB NOT NULL DEFAULT '{}'::jsonb
);

CREATE INDEX IF NOT EXISTS idx_audit_data_change_lookup
	ON _audit_data_change(table_name, record_id, _created_at DESC);
