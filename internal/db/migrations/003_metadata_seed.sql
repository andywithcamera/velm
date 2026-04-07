DO $$
BEGIN
	IF to_regclass('_table') IS NULL OR to_regclass('_column') IS NULL THEN
		RAISE NOTICE 'Skipping metadata seed: _table or _column does not exist';
		RETURN;
	END IF;

	INSERT INTO _table (name, label_singular, label_plural, description)
	SELECT t.name, t.label_singular, t.label_plural, t.description
	FROM (
		VALUES
			('work_item', 'Work Item', 'Work Items', 'Global/core table for tasks, work, and tickets'),
			('asset_item', 'Asset Item', 'Asset Items', 'Global/core table for assets, CIs, and tracked things')
	) AS t(name, label_singular, label_plural, description)
	WHERE NOT EXISTS (
		SELECT 1
		FROM _table existing
		WHERE existing.name = t.name
	);

	INSERT INTO _column (table_id, name, label, data_type, is_nullable, default_value, validation_regex)
	SELECT tbl._id, c.name, c.label, c.data_type, c.is_nullable, c.default_value, c.validation_regex
	FROM (
		VALUES
			('work_item', 'number', 'Number', 'text', FALSE, NULL::text, '^WK-[0-9]{6}$'),
			('work_item', 'title', 'Title', 'text', FALSE, NULL::text, NULL::text),
			('work_item', 'description', 'Description', 'text', TRUE, NULL::text, NULL::text),
			('work_item', 'status', 'Status', 'text', FALSE, 'new', '^(new|in_progress|blocked|done|cancelled)$'),
			('work_item', 'priority', 'Priority', 'text', FALSE, 'medium', '^(low|medium|high|critical)$'),
			('work_item', 'requested_by', 'Requested By', 'text', TRUE, NULL::text, NULL::text),
			('work_item', 'assigned_to', 'Assigned To', 'text', TRUE, NULL::text, NULL::text),
			('work_item', 'due_at', 'Due At', 'timestamp', TRUE, NULL::text, NULL::text),
			('work_item', 'work_type', 'Work Type', 'text', TRUE, NULL::text, NULL::text),

			('asset_item', 'name', 'Name', 'text', FALSE, NULL::text, NULL::text),
			('asset_item', 'asset_type', 'Asset Type', 'text', FALSE, NULL::text, NULL::text),
			('asset_item', 'status', 'Status', 'text', FALSE, 'active', '^(active|inactive|retired|maintenance)$'),
			('asset_item', 'owner', 'Owner', 'text', TRUE, NULL::text, NULL::text),
			('asset_item', 'criticality', 'Criticality', 'text', TRUE, NULL::text, '^(low|medium|high|critical)$'),
			('asset_item', 'serial_number', 'Serial Number', 'text', TRUE, NULL::text, NULL::text),
			('asset_item', 'location', 'Location', 'text', TRUE, NULL::text, NULL::text),
			('asset_item', 'vendor', 'Vendor', 'text', TRUE, NULL::text, NULL::text)
	) AS c(table_name, name, label, data_type, is_nullable, default_value, validation_regex)
	JOIN _table tbl ON tbl.name = c.table_name
	WHERE NOT EXISTS (
		SELECT 1
		FROM _column existing
		WHERE existing.table_id = tbl._id
		  AND existing.name = c.name
	);

	IF to_regclass('_menu') IS NOT NULL THEN
		INSERT INTO _menu (title, href, "order")
		SELECT m.title, m.href, m."order"
		FROM (
			VALUES
				('Work Items', '/t/work_item', 100),
				('Asset Items', '/t/asset_item', 110)
		) AS m(title, href, "order")
		WHERE NOT EXISTS (
			SELECT 1
			FROM _menu existing
			WHERE existing.href = m.href
		);
	END IF;
END
$$;
