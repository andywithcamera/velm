CREATE TABLE IF NOT EXISTS _group (
	_id BIGSERIAL PRIMARY KEY,
	name TEXT NOT NULL UNIQUE,
	description TEXT,
	is_system BOOLEAN NOT NULL DEFAULT FALSE,
	_created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
	_updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS _group_membership (
	_id BIGSERIAL PRIMARY KEY,
	group_id BIGINT NOT NULL REFERENCES _group(_id) ON DELETE CASCADE,
	user_id TEXT NOT NULL,
	_created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
	_updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
	UNIQUE (group_id, user_id)
);

CREATE INDEX IF NOT EXISTS idx_group_membership_group_id ON _group_membership(group_id);
CREATE INDEX IF NOT EXISTS idx_group_membership_user_id ON _group_membership(user_id);

CREATE TABLE IF NOT EXISTS _group_role (
	_id BIGSERIAL PRIMARY KEY,
	group_id BIGINT NOT NULL REFERENCES _group(_id) ON DELETE CASCADE,
	role_id BIGINT NOT NULL REFERENCES _role(_id) ON DELETE CASCADE,
	app_id TEXT NOT NULL DEFAULT '',
	_created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
	_updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
	UNIQUE (group_id, role_id, app_id)
);

CREATE INDEX IF NOT EXISTS idx_group_role_group_id ON _group_role(group_id);
CREATE INDEX IF NOT EXISTS idx_group_role_role_id ON _group_role(role_id);
CREATE INDEX IF NOT EXISTS idx_group_role_app_id ON _group_role(app_id);

CREATE TABLE IF NOT EXISTS _role_inheritance (
	_id BIGSERIAL PRIMARY KEY,
	role_id BIGINT NOT NULL REFERENCES _role(_id) ON DELETE CASCADE,
	inherits_role_id BIGINT NOT NULL REFERENCES _role(_id) ON DELETE CASCADE,
	_created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
	_updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
	CONSTRAINT chk_role_inheritance_not_self CHECK (role_id <> inherits_role_id),
	UNIQUE (role_id, inherits_role_id)
);

CREATE INDEX IF NOT EXISTS idx_role_inheritance_role_id ON _role_inheritance(role_id);
CREATE INDEX IF NOT EXISTS idx_role_inheritance_inherits_role_id ON _role_inheritance(inherits_role_id);

INSERT INTO _role_inheritance (role_id, inherits_role_id)
SELECT child._id, parent._id
FROM _role child
JOIN _role parent ON parent.name = 'operator'
WHERE child.name = 'admin'
ON CONFLICT DO NOTHING;

INSERT INTO _role_inheritance (role_id, inherits_role_id)
SELECT child._id, parent._id
FROM _role child
JOIN _role parent ON parent.name = 'viewer'
WHERE child.name = 'operator'
ON CONFLICT DO NOTHING;
