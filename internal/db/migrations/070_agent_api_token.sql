-- Agent API tokens: scoped bearer tokens bound to a real _user identity.
--
-- Principle (issue #68): agents are users without a body. An agent is an
-- ordinary _user row; the ONLY difference is authN. Humans get a session via
-- cookie/magic link; agents present a scoped bearer token that resolves to the
-- same request context, so authZ (roles, org membership) is identical.
--
-- We store only a SHA-256 hash of the raw secret -- never the plaintext token.

-- user_id is the platform's canonical identity key: TEXT (_id::text), the same
-- join key used by _user_role. Agents are plain _user rows; the token binding
-- is what marks an API-authenticated identity (issue #68: authN differs, authZ identical).
CREATE TABLE IF NOT EXISTS _agent_api_token (
	_id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
	user_id TEXT NOT NULL,
	token_hash TEXT NOT NULL UNIQUE,
	label TEXT NOT NULL DEFAULT '',
	-- scopes: pipe-separated permission gates (resource:action), e.g.
	-- 'platform:write|base_task:write'. Empty = all permissions the user has.
	scopes TEXT NOT NULL DEFAULT '',
	is_revoked BOOLEAN NOT NULL DEFAULT FALSE,
	_created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
	last_used_at TIMESTAMPTZ,
	expires_at TIMESTAMPTZ
);

CREATE INDEX IF NOT EXISTS idx_agent_api_token_user_id
	ON _agent_api_token(user_id);
CREATE INDEX IF NOT EXISTS idx_agent_api_token_hash
	ON _agent_api_token(token_hash);

-- Agent tokens are API-storage only; they never appear in normal UI queries.
-- Human records stay in _user; the token table is the only place agent-ness lives.
