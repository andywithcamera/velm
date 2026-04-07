CREATE TABLE IF NOT EXISTS _builder_schema_change (
	_id BIGSERIAL PRIMARY KEY,
	change_type TEXT NOT NULL,
	table_name TEXT NOT NULL,
	column_name TEXT,
	before_state JSONB,
	after_state JSONB,
	applied_by TEXT,
	applied_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
	CONSTRAINT chk_builder_schema_change_type CHECK (
		change_type IN (
			'table_create',
			'table_update',
			'table_delete',
			'column_create',
			'column_update',
			'column_delete'
		)
	)
);

CREATE INDEX IF NOT EXISTS idx_builder_schema_change_table_time
	ON _builder_schema_change(table_name, applied_at DESC);
