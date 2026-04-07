CREATE TABLE IF NOT EXISTS _audit_log (
	_id BIGSERIAL PRIMARY KEY,
	_created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
	user_id TEXT,
	user_email TEXT,
	user_role TEXT,
	method TEXT NOT NULL,
	path TEXT NOT NULL,
	status INTEGER NOT NULL,
	ip TEXT,
	user_agent TEXT
);
