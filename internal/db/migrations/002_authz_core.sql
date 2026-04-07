CREATE TABLE IF NOT EXISTS _role (
	_id BIGSERIAL PRIMARY KEY,
	name TEXT NOT NULL UNIQUE,
	description TEXT,
	is_system BOOLEAN NOT NULL DEFAULT TRUE,
	priority INTEGER NOT NULL DEFAULT 100,
	_created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
	_updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS _permission (
	_id BIGSERIAL PRIMARY KEY,
	resource TEXT NOT NULL,
	action TEXT NOT NULL,
	scope TEXT NOT NULL DEFAULT 'global',
	description TEXT,
	_created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
	_updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
	UNIQUE(resource, action, scope)
);

CREATE TABLE IF NOT EXISTS _role_permission (
	role_id BIGINT NOT NULL REFERENCES _role(_id) ON DELETE CASCADE,
	permission_id BIGINT NOT NULL REFERENCES _permission(_id) ON DELETE CASCADE,
	_created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
	PRIMARY KEY (role_id, permission_id)
);

CREATE TABLE IF NOT EXISTS _user_role (
	user_id TEXT NOT NULL,
	role_id BIGINT NOT NULL REFERENCES _role(_id) ON DELETE CASCADE,
	app_id TEXT NOT NULL DEFAULT '',
	_created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
	PRIMARY KEY (user_id, role_id, app_id)
);

CREATE INDEX IF NOT EXISTS idx_user_role_user_id ON _user_role(user_id);
CREATE INDEX IF NOT EXISTS idx_permission_lookup ON _permission(resource, action, scope);

UPDATE _user_role SET app_id = '' WHERE app_id IS NULL;

INSERT INTO _role (name, description, is_system, priority)
VALUES
	('admin', 'Full access', TRUE, 10),
	('operator', 'Read/write access', TRUE, 20),
	('viewer', 'Read-only access', TRUE, 30)
ON CONFLICT (name)
DO UPDATE SET
	description = EXCLUDED.description,
	priority = EXCLUDED.priority;

INSERT INTO _permission (resource, action, scope, description)
VALUES
	('platform', 'view', 'global', 'View platform content'),
	('platform', 'write', 'global', 'Create and update platform content'),
	('platform', 'admin', 'global', 'Administrative platform actions')
ON CONFLICT (resource, action, scope)
DO UPDATE SET description = EXCLUDED.description;

INSERT INTO _role_permission (role_id, permission_id)
SELECT r._id, p._id
FROM _role r
JOIN _permission p ON p.resource = 'platform' AND p.scope = 'global'
WHERE (r.name = 'admin' AND p.action IN ('view', 'write', 'admin'))
   OR (r.name = 'operator' AND p.action IN ('view', 'write'))
   OR (r.name = 'viewer' AND p.action IN ('view'))
ON CONFLICT DO NOTHING;

DO $$
BEGIN
	IF to_regclass('_user') IS NOT NULL THEN
		INSERT INTO _user_role (user_id, role_id, app_id)
		SELECT u._id::text, r._id, ''
		FROM _user u
		JOIN _role r ON r.name = 'admin'
		WHERE NOT EXISTS (
			SELECT 1
			FROM _user_role ur
			WHERE ur.user_id = u._id::text
		);
	END IF;
END
$$;
