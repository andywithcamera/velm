CREATE TABLE IF NOT EXISTS _security_log (
	_id BIGSERIAL PRIMARY KEY,
	_created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
	event_type TEXT NOT NULL,
	severity TEXT NOT NULL DEFAULT 'warn',
	user_id TEXT,
	request_path TEXT,
	request_method TEXT,
	ip TEXT,
	user_agent TEXT,
	detail JSONB NOT NULL DEFAULT '{}'::jsonb
);

CREATE INDEX IF NOT EXISTS idx_security_log_event_time
	ON _security_log(event_type, _created_at DESC);
