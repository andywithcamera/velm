CREATE TABLE IF NOT EXISTS _record_comment (
	_id BIGSERIAL PRIMARY KEY,
	_created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
	user_id TEXT NOT NULL,
	table_name TEXT NOT NULL,
	record_id TEXT NOT NULL,
	body TEXT NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_record_comment_lookup
	ON _record_comment(table_name, record_id, _created_at DESC);
