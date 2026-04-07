CREATE EXTENSION IF NOT EXISTS pgcrypto;

CREATE TABLE IF NOT EXISTS work_item (
	_id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
	item_type TEXT NOT NULL DEFAULT 'task',
	number TEXT,
	title TEXT NOT NULL,
	description TEXT,
	status TEXT NOT NULL DEFAULT 'open',
	priority TEXT NOT NULL DEFAULT 'medium',
	requested_by TEXT,
	assigned_to TEXT,
	due_at TIMESTAMPTZ,
	opened_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
	resolved_at TIMESTAMPTZ,
	closed_at TIMESTAMPTZ,
	_created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
	_updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
	CONSTRAINT chk_work_item_priority CHECK (priority IN ('low', 'medium', 'high', 'critical')),
	CONSTRAINT chk_work_item_dates CHECK (
		(closed_at IS NULL OR resolved_at IS NULL OR closed_at >= resolved_at)
		AND (resolved_at IS NULL OR resolved_at >= opened_at)
		AND (closed_at IS NULL OR closed_at >= opened_at)
	)
);

CREATE TABLE IF NOT EXISTS asset_item (
	_id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
	asset_type TEXT NOT NULL DEFAULT 'generic',
	number TEXT,
	name TEXT NOT NULL,
	description TEXT,
	status TEXT NOT NULL DEFAULT 'active',
	criticality TEXT,
	owner TEXT,
	lifecycle_state TEXT,
	_created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
	_updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
	CONSTRAINT chk_asset_item_criticality CHECK (criticality IS NULL OR criticality IN ('low', 'medium', 'high', 'critical'))
);

CREATE INDEX IF NOT EXISTS idx_work_item_type_status ON work_item(item_type, status);
CREATE INDEX IF NOT EXISTS idx_work_item_number ON work_item(number);
CREATE INDEX IF NOT EXISTS idx_work_item_assigned_to ON work_item(assigned_to);
CREATE INDEX IF NOT EXISTS idx_work_item_due_at ON work_item(due_at);

CREATE INDEX IF NOT EXISTS idx_asset_item_type_status ON asset_item(asset_type, status);
CREATE INDEX IF NOT EXISTS idx_asset_item_number ON asset_item(number);
CREATE INDEX IF NOT EXISTS idx_asset_item_name ON asset_item(name);

DO $$
DECLARE
	seed_user_id uuid;
BEGIN
	IF to_regclass('_table') IS NULL OR to_regclass('_column') IS NULL THEN
		RETURN;
	END IF;

	seed_user_id := COALESCE(
		(SELECT _id FROM _user ORDER BY _created_at ASC LIMIT 1),
		'e2402d0b-f30a-49b3-bc6c-5c8982fe6cc5'::uuid
	);

	WITH core_tables(name, label_singular, label_plural, description) AS (
		VALUES
			('work_item', 'Work Item', 'Work Items', 'Global/core table for tasks, to-dos, and operational work'),
			('asset_item', 'Asset', 'Assets', 'Global/core table for assets, configuration items, and things')
	)
	INSERT INTO _table (name, _updated_by, label_singular, label_plural, description)
	SELECT ct.name, seed_user_id, ct.label_singular, ct.label_plural, ct.description
	FROM core_tables ct
	WHERE NOT EXISTS (SELECT 1 FROM _table t WHERE t.name = ct.name);
END
$$;
