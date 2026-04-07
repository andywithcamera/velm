CREATE TABLE IF NOT EXISTS _user_security_state (
	user_id TEXT PRIMARY KEY,
	session_version INTEGER NOT NULL DEFAULT 1,
	_updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_user_security_state_updated
	ON _user_security_state(_updated_at DESC);
