CREATE TABLE IF NOT EXISTS script_def (
	_id BIGSERIAL PRIMARY KEY,
	name TEXT NOT NULL,
	scope TEXT NOT NULL DEFAULT 'global',
	trigger_type TEXT NOT NULL,
	enabled BOOLEAN NOT NULL DEFAULT TRUE,
	owner_user_id TEXT NOT NULL,
	_created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
	_updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
	UNIQUE(name, scope)
);

CREATE TABLE IF NOT EXISTS script_version (
	_id BIGSERIAL PRIMARY KEY,
	script_def_id BIGINT NOT NULL REFERENCES script_def(_id) ON DELETE CASCADE,
	version_num INTEGER NOT NULL,
	code TEXT NOT NULL,
	checksum TEXT,
	created_by TEXT NOT NULL,
	published_at TIMESTAMPTZ,
	_created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
	UNIQUE(script_def_id, version_num)
);

CREATE TABLE IF NOT EXISTS script_binding (
	_id BIGSERIAL PRIMARY KEY,
	script_def_id BIGINT NOT NULL REFERENCES script_def(_id) ON DELETE CASCADE,
	table_name TEXT NOT NULL,
	event_name TEXT NOT NULL,
	condition_expr TEXT,
	app_id TEXT NOT NULL DEFAULT '',
	enabled BOOLEAN NOT NULL DEFAULT TRUE,
	_created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS script_execution_log (
	_id BIGSERIAL PRIMARY KEY,
	script_def_id BIGINT REFERENCES script_def(_id) ON DELETE SET NULL,
	status TEXT NOT NULL,
	duration_ms INTEGER,
	output TEXT,
	error_text TEXT,
	started_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
	finished_at TIMESTAMPTZ
);

CREATE INDEX IF NOT EXISTS idx_script_binding_lookup
	ON script_binding(script_def_id, app_id, table_name, event_name);

CREATE INDEX IF NOT EXISTS idx_script_version_latest
	ON script_version(script_def_id, version_num DESC);

CREATE INDEX IF NOT EXISTS idx_script_execution_started
	ON script_execution_log(started_at DESC);
