CREATE TABLE IF NOT EXISTS _user_preference (
	user_id TEXT NOT NULL,
	namespace TEXT NOT NULL,
	pref_key TEXT NOT NULL,
	pref_value JSONB NOT NULL,
	_created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
	_updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
	PRIMARY KEY (user_id, namespace, pref_key)
);

CREATE INDEX IF NOT EXISTS idx_user_preference_user_namespace
	ON _user_preference(user_id, namespace);
