CREATE TABLE IF NOT EXISTS _script (
	_id BIGSERIAL PRIMARY KEY,
	_created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
	_updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
	_deleted_at TIMESTAMPTZ,
	_created_by TEXT,
	_updated_by TEXT,
	_deleted_by TEXT,
	name TEXT NOT NULL,
	scope TEXT NOT NULL DEFAULT 'global',
	description TEXT NOT NULL DEFAULT '',
	trigger_type TEXT NOT NULL DEFAULT 'manual',
	table_name TEXT NOT NULL DEFAULT '',
	event_name TEXT NOT NULL DEFAULT '',
	condition_expr TEXT NOT NULL DEFAULT '',
	language TEXT NOT NULL DEFAULT 'javascript',
	script TEXT NOT NULL DEFAULT '',
	enabled BOOLEAN NOT NULL DEFAULT TRUE,
	status TEXT NOT NULL DEFAULT 'draft',
	version_num INTEGER NOT NULL DEFAULT 0,
	checksum TEXT NOT NULL DEFAULT '',
	owner_user_id TEXT NOT NULL DEFAULT '',
	published_at TIMESTAMPTZ,
	last_run_at TIMESTAMPTZ,
	legacy_script_def_id BIGINT UNIQUE,
	CONSTRAINT uq__script_name_scope UNIQUE (name, scope),
	CONSTRAINT chk__script_status CHECK (status IN ('draft', 'published', 'disabled', 'archived')),
	CONSTRAINT chk__script_version_num CHECK (version_num >= 0)
);

CREATE INDEX IF NOT EXISTS idx__script_scope_status ON _script(scope, status);
CREATE INDEX IF NOT EXISTS idx__script_enabled ON _script(enabled);
CREATE INDEX IF NOT EXISTS idx__script_trigger_type ON _script(trigger_type);

DO $$
DECLARE
	seed_user_id uuid;
BEGIN
	IF to_regclass('script_def') IS NOT NULL THEN
		INSERT INTO _script (
			name,
			scope,
			description,
			trigger_type,
			table_name,
			event_name,
			condition_expr,
			language,
			script,
			enabled,
			status,
			version_num,
			checksum,
			owner_user_id,
			published_at,
			last_run_at,
			legacy_script_def_id,
			_created_at,
			_updated_at,
			_created_by,
			_updated_by
		)
		SELECT
			d.name,
			COALESCE(NULLIF(d.scope, ''), 'global'),
			'',
			COALESCE(NULLIF(d.trigger_type, ''), 'manual'),
			COALESCE(first_binding.table_name, ''),
			COALESCE(first_binding.event_name, ''),
			COALESCE(first_binding.condition_expr, ''),
			'javascript',
			COALESCE(latest_version.code, ''),
			d.enabled,
			CASE
				WHEN d.enabled = false THEN 'disabled'
				WHEN latest_version.published_at IS NOT NULL THEN 'published'
				ELSE 'draft'
			END,
			COALESCE(latest_version.version_num, 0),
			COALESCE(latest_version.checksum, ''),
			COALESCE(d.owner_user_id, ''),
			latest_version.published_at,
			last_exec.finished_at,
			d._id,
			COALESCE(latest_version._created_at, d._created_at, NOW()),
			COALESCE(latest_version._created_at, d._updated_at, NOW()),
			COALESCE(NULLIF(latest_version.created_by, ''), d.owner_user_id, ''),
			COALESCE(NULLIF(latest_version.created_by, ''), d.owner_user_id, '')
		FROM script_def d
		LEFT JOIN LATERAL (
			SELECT v.version_num, v.code, COALESCE(v.checksum, '') AS checksum, v.created_by, v.published_at, v._created_at
			FROM script_version v
			WHERE v.script_def_id = d._id
			ORDER BY v.version_num DESC
			LIMIT 1
		) latest_version ON TRUE
		LEFT JOIN LATERAL (
			SELECT b.table_name, b.event_name, COALESCE(b.condition_expr, '') AS condition_expr
			FROM script_binding b
			WHERE b.script_def_id = d._id
			ORDER BY b._id ASC
			LIMIT 1
		) first_binding ON TRUE
		LEFT JOIN LATERAL (
			SELECT e.finished_at
			FROM script_execution_log e
			WHERE e.script_def_id = d._id
			  AND e.finished_at IS NOT NULL
			ORDER BY e.finished_at DESC
			LIMIT 1
		) last_exec ON TRUE
		WHERE NOT EXISTS (
			SELECT 1
			FROM _script s
			WHERE s.legacy_script_def_id = d._id
		);
	END IF;

	IF to_regclass('_table') IS NULL OR to_regclass('_column') IS NULL THEN
		RETURN;
	END IF;

	seed_user_id := COALESCE(
		(SELECT _id FROM _user ORDER BY _created_at ASC LIMIT 1),
		'e2402d0b-f30a-49b3-bc6c-5c8982fe6cc5'::uuid
	);

	INSERT INTO _table (name, _updated_by, label_singular, label_plural, description)
	SELECT '_script', seed_user_id, 'Script', 'Scripts', 'Core script registry with trigger configuration and source content'
	WHERE NOT EXISTS (SELECT 1 FROM _table WHERE name = '_script');

	UPDATE _table
	SET label_singular = 'Script',
		label_plural = 'Scripts',
		description = 'Core script registry with trigger configuration and source content',
		_updated_by = seed_user_id,
		_updated_at = NOW()
	WHERE name = '_script';

	INSERT INTO _column (table_id, name, label, data_type, is_nullable, default_value, _updated_by)
	SELECT t._id, c.name, c.label, c.data_type, c.is_nullable, c.default_value, seed_user_id
	FROM _table t
	JOIN (
		VALUES
			('name', 'Name', 'text', FALSE, NULL::text),
			('scope', 'Scope', 'text', FALSE, 'global'),
			('description', 'Description', 'text', FALSE, ''::text),
			('trigger_type', 'Trigger Type', 'text', FALSE, 'manual'),
			('table_name', 'Table Name', 'text', FALSE, ''::text),
			('event_name', 'Event Name', 'text', FALSE, ''::text),
			('condition_expr', 'Condition Expression', 'text', FALSE, ''::text),
			('language', 'Language', 'text', FALSE, 'javascript'),
			('script', 'Script', 'text', FALSE, ''::text),
			('enabled', 'Enabled', 'bool', FALSE, 'true'),
			('status', 'Status', 'text', FALSE, 'draft'),
			('version_num', 'Version', 'int', FALSE, '0'),
			('checksum', 'Checksum', 'text', FALSE, ''::text),
			('owner_user_id', 'Owner User ID', 'text', FALSE, ''::text),
			('published_at', 'Published At', 'timestamp', TRUE, NULL::text),
			('last_run_at', 'Last Run At', 'timestamp', TRUE, NULL::text)
	) AS c(name, label, data_type, is_nullable, default_value) ON TRUE
	WHERE t.name = '_script'
	  AND NOT EXISTS (
		SELECT 1
		FROM _column existing
		WHERE existing.table_id = t._id
		  AND existing.name = c.name
	  );
END
$$;
