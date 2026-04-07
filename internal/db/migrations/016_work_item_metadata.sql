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

	WITH tables_meta(name, label_singular, label_plural, description) AS (
		VALUES
			('work_item', 'Work Item', 'Work Items', 'Global/core table for tasks, to-dos, and operational work'),
			('asset_item', 'Asset', 'Assets', 'Global/core table for assets, configuration items, and things')
	)
	UPDATE _table t
	SET
		label_singular = m.label_singular,
		label_plural = m.label_plural,
		description = m.description,
		_updated_by = seed_user_id,
		_updated_at = NOW()
	FROM tables_meta m
	WHERE t.name = m.name;

	WITH cols(table_name, name, label, data_type, is_nullable, default_value, validation_regex) AS (
		VALUES
			('work_item', 'item_type', 'Type', 'text', FALSE, 'task', '^[a-z][a-z0-9_\\-]{1,63}$'),
			('work_item', 'number', 'Number', 'text', TRUE, NULL::text, NULL::text),
			('work_item', 'title', 'Title', 'text', FALSE, NULL::text, NULL::text),
			('work_item', 'description', 'Description', 'text', TRUE, NULL::text, NULL::text),
			('work_item', 'status', 'Status', 'text', FALSE, 'open', NULL::text),
			('work_item', 'priority', 'Priority', 'text', FALSE, 'medium', '^(low|medium|high|critical)$'),
			('work_item', 'requested_by', 'Requested By', 'text', TRUE, NULL::text, NULL::text),
			('work_item', 'assigned_to', 'Assigned To', 'text', TRUE, NULL::text, NULL::text),
			('work_item', 'due_at', 'Due At', 'timestamp', TRUE, NULL::text, NULL::text),
			('asset_item', 'asset_type', 'Asset Type', 'text', FALSE, 'generic', '^[a-z][a-z0-9_\\-]{1,63}$'),
			('asset_item', 'number', 'Number', 'text', TRUE, NULL::text, NULL::text),
			('asset_item', 'name', 'Name', 'text', FALSE, NULL::text, NULL::text),
			('asset_item', 'description', 'Description', 'text', TRUE, NULL::text, NULL::text),
			('asset_item', 'status', 'Status', 'text', FALSE, 'active', NULL::text),
			('asset_item', 'criticality', 'Criticality', 'text', TRUE, NULL::text, '^(low|medium|high|critical)$'),
			('asset_item', 'owner', 'Owner', 'text', TRUE, NULL::text, NULL::text),
			('asset_item', 'lifecycle_state', 'Lifecycle State', 'text', TRUE, NULL::text, NULL::text)
	)
	INSERT INTO _column (table_id, name, label, data_type, is_nullable, default_value, validation_regex, _updated_by)
	SELECT t._id, c.name, c.label, c.data_type, c.is_nullable, c.default_value, c.validation_regex, seed_user_id
	FROM cols c
	JOIN _table t ON t.name = c.table_name
	WHERE NOT EXISTS (
		SELECT 1
		FROM _column existing
		WHERE existing.table_id = t._id
		  AND existing.name = c.name
	);

	WITH cols(table_name, name, label, data_type, is_nullable, default_value, validation_regex) AS (
		VALUES
			('work_item', 'item_type', 'Type', 'text', FALSE, 'task', '^[a-z][a-z0-9_\\-]{1,63}$'),
			('work_item', 'number', 'Number', 'text', TRUE, NULL::text, NULL::text),
			('work_item', 'title', 'Title', 'text', FALSE, NULL::text, NULL::text),
			('work_item', 'description', 'Description', 'text', TRUE, NULL::text, NULL::text),
			('work_item', 'status', 'Status', 'text', FALSE, 'open', NULL::text),
			('work_item', 'priority', 'Priority', 'text', FALSE, 'medium', '^(low|medium|high|critical)$'),
			('work_item', 'requested_by', 'Requested By', 'text', TRUE, NULL::text, NULL::text),
			('work_item', 'assigned_to', 'Assigned To', 'text', TRUE, NULL::text, NULL::text),
			('work_item', 'due_at', 'Due At', 'timestamp', TRUE, NULL::text, NULL::text),
			('asset_item', 'asset_type', 'Asset Type', 'text', FALSE, 'generic', '^[a-z][a-z0-9_\\-]{1,63}$'),
			('asset_item', 'number', 'Number', 'text', TRUE, NULL::text, NULL::text),
			('asset_item', 'name', 'Name', 'text', FALSE, NULL::text, NULL::text),
			('asset_item', 'description', 'Description', 'text', TRUE, NULL::text, NULL::text),
			('asset_item', 'status', 'Status', 'text', FALSE, 'active', NULL::text),
			('asset_item', 'criticality', 'Criticality', 'text', TRUE, NULL::text, '^(low|medium|high|critical)$'),
			('asset_item', 'owner', 'Owner', 'text', TRUE, NULL::text, NULL::text),
			('asset_item', 'lifecycle_state', 'Lifecycle State', 'text', TRUE, NULL::text, NULL::text)
	)
	UPDATE _column cc
	SET
		label = c.label,
		data_type = c.data_type,
		is_nullable = c.is_nullable,
		default_value = c.default_value,
		validation_regex = c.validation_regex,
		_updated_at = NOW(),
		_updated_by = seed_user_id
	FROM cols c
	JOIN _table t ON t.name = c.table_name
	WHERE cc.table_id = t._id
	  AND cc.name = c.name;

	IF to_regclass('_menu') IS NOT NULL THEN
		INSERT INTO _menu (title, href, "order")
		SELECT 'Work Items', '/t/work_item', 90
		WHERE NOT EXISTS (SELECT 1 FROM _menu m WHERE m.href = '/t/work_item');

		INSERT INTO _menu (title, href, "order")
		SELECT 'Assets', '/t/asset_item', 95
		WHERE NOT EXISTS (SELECT 1 FROM _menu m WHERE m.href = '/t/asset_item');
	END IF;
END
$$;
