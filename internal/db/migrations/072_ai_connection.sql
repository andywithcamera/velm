-- AI connection: per-account credential bound to a provider. Instance-global
-- in P1 (no org entity exists yet; see issue #93 discussion). Secret-bearing:
-- api_key is encrypted at rest using the SecretCipher (MFASecretCipher, #102).
-- We also store a non-secret fingerprint prefix for UI display.
CREATE TABLE IF NOT EXISTS _ai_connection (
	_id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
	provider_id UUID NOT NULL REFERENCES _ai_provider(_id),
	label TEXT NOT NULL DEFAULT '',
	api_key_enc TEXT NOT NULL,          -- encrypted, never plaintext
	api_key_fingerprint TEXT NOT NULL DEFAULT '',  -- e.g. 'sk-...4f2a' for display
	account_info JSONB NOT NULL DEFAULT '{}'::jsonb,
	is_active BOOLEAN NOT NULL DEFAULT TRUE,
	last_verified_at TIMESTAMPTZ,
	models_synced_at TIMESTAMPTZ,
	_created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
	_updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_ai_connection_provider_id
	ON _ai_connection(provider_id);
