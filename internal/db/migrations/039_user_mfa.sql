CREATE EXTENSION IF NOT EXISTS pgcrypto;

CREATE TABLE IF NOT EXISTS _user_auth_factor (
	_id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
	_created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
	_created_by TEXT,
	_updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
	_updated_by TEXT,
	_deleted_at TIMESTAMPTZ,
	_deleted_by TEXT,
	user_id TEXT NOT NULL,
	factor_type TEXT NOT NULL,
	label TEXT NOT NULL DEFAULT '',
	status TEXT NOT NULL DEFAULT 'pending',
	secret_enc TEXT NOT NULL,
	confirmed_at TIMESTAMPTZ,
	last_used_at TIMESTAMPTZ,
	CONSTRAINT chk_user_auth_factor_type CHECK (factor_type IN ('totp')),
	CONSTRAINT chk_user_auth_factor_status CHECK (status IN ('pending', 'active', 'disabled'))
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_user_auth_factor_active_unique
	ON _user_auth_factor(user_id, factor_type)
	WHERE status = 'active' AND _deleted_at IS NULL;

CREATE INDEX IF NOT EXISTS idx_user_auth_factor_user_status
	ON _user_auth_factor(user_id, status, factor_type, _created_at DESC);

CREATE TABLE IF NOT EXISTS _user_recovery_code (
	_id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
	_created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
	_created_by TEXT,
	_updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
	_updated_by TEXT,
	_deleted_at TIMESTAMPTZ,
	_deleted_by TEXT,
	factor_id UUID NOT NULL REFERENCES _user_auth_factor(_id) ON DELETE CASCADE,
	user_id TEXT NOT NULL,
	code_hash TEXT NOT NULL,
	used_at TIMESTAMPTZ
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_user_recovery_code_factor_hash
	ON _user_recovery_code(factor_id, code_hash);

CREATE INDEX IF NOT EXISTS idx_user_recovery_code_lookup
	ON _user_recovery_code(user_id, factor_id, used_at, _created_at DESC);
