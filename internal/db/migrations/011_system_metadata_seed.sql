DO $$
DECLARE
	seed_user_id uuid;
BEGIN
	IF to_regclass('_table') IS NULL OR to_regclass('_column') IS NULL THEN
		RAISE NOTICE 'Skipping system metadata seed: _table or _column does not exist';
		RETURN;
	END IF;

	seed_user_id := COALESCE(
		(SELECT _id FROM _user ORDER BY _created_at ASC LIMIT 1),
		'e2402d0b-f30a-49b3-bc6c-5c8982fe6cc5'::uuid
	);

	WITH system_tables(name, label_singular, label_plural, description) AS (
		VALUES
			('_table', 'Table', 'Tables', 'System registry of data tables'),
			('_column', 'Column', 'Columns', 'System registry of table column definitions'),
			('_menu', 'Menu', 'Menus', 'System navigation configuration'),
			('_property', 'Property', 'Properties', 'System and application runtime properties'),
			('_role', 'Role', 'Roles', 'Authorization roles'),
			('_permission', 'Permission', 'Permissions', 'Authorization permissions'),
			('_role_permission', 'Role Permission', 'Role Permissions', 'Role-to-permission joins'),
			('_user_role', 'User Role', 'User Roles', 'User-to-role assignments'),
			('_audit_log', 'Audit Request Log', 'Audit Request Logs', 'Request-level audit trail'),
			('_audit_data_change', 'Audit Data Change', 'Audit Data Changes', 'Record-level before/after audit trail'),
			('_security_log', 'Security Log', 'Security Logs', 'Immutable security events'),
			('_user_preference', 'User Preference', 'User Preferences', 'Per-user saved view and UI settings'),
			('script_def', 'Script Definition', 'Script Definitions', 'Business automation script registry'),
			('script_version', 'Script Version', 'Script Versions', 'Versioned script source and publish metadata'),
			('script_binding', 'Script Binding', 'Script Bindings', 'Event bindings between scripts and tables'),
			('script_execution_log', 'Script Execution Log', 'Script Execution Logs', 'Runtime outcomes for script executions'),
			('work_item', 'Work Item', 'Work Items', 'Global/core table for tasks, to-dos, and operational work'),
			('asset_item', 'Asset', 'Assets', 'Global/core table for assets, configuration items, and things')
	)
	INSERT INTO _table (name, _updated_by, label_singular, label_plural, description)
	SELECT st.name, seed_user_id, st.label_singular, st.label_plural, st.description
	FROM system_tables st
	WHERE NOT EXISTS (SELECT 1 FROM _table t WHERE t.name = st.name);

	WITH system_tables(name, label_singular, label_plural, description) AS (
		VALUES
			('_table', 'Table', 'Tables', 'System registry of data tables'),
			('_column', 'Column', 'Columns', 'System registry of table column definitions'),
			('_menu', 'Menu', 'Menus', 'System navigation configuration'),
			('_property', 'Property', 'Properties', 'System and application runtime properties'),
			('_role', 'Role', 'Roles', 'Authorization roles'),
			('_permission', 'Permission', 'Permissions', 'Authorization permissions'),
			('_role_permission', 'Role Permission', 'Role Permissions', 'Role-to-permission joins'),
			('_user_role', 'User Role', 'User Roles', 'User-to-role assignments'),
			('_audit_log', 'Audit Request Log', 'Audit Request Logs', 'Request-level audit trail'),
			('_audit_data_change', 'Audit Data Change', 'Audit Data Changes', 'Record-level before/after audit trail'),
			('_security_log', 'Security Log', 'Security Logs', 'Immutable security events'),
			('_user_preference', 'User Preference', 'User Preferences', 'Per-user saved view and UI settings'),
			('script_def', 'Script Definition', 'Script Definitions', 'Business automation script registry'),
			('script_version', 'Script Version', 'Script Versions', 'Versioned script source and publish metadata'),
			('script_binding', 'Script Binding', 'Script Bindings', 'Event bindings between scripts and tables'),
			('script_execution_log', 'Script Execution Log', 'Script Execution Logs', 'Runtime outcomes for script executions'),
			('work_item', 'Work Item', 'Work Items', 'Global/core table for tasks, to-dos, and operational work'),
			('asset_item', 'Asset', 'Assets', 'Global/core table for assets, configuration items, and things')
	)
	UPDATE _table t
	SET
		label_singular = st.label_singular,
		label_plural = st.label_plural,
		description = st.description,
		_updated_by = seed_user_id,
		_updated_at = NOW()
	FROM system_tables st
	WHERE t.name = st.name;

	WITH column_meta(table_name, name, label, data_type, is_nullable, default_value, validation_regex) AS (
		VALUES
			('work_item', 'item_type', 'Type', 'text', FALSE, 'task', '^[a-z][a-z0-9_\\-]{1,63}$'),
			('work_item', 'number', 'Number', 'text', TRUE, NULL::text, NULL::text),
			('work_item', 'title', 'Title', 'text', FALSE, NULL::text, NULL::text),
			('work_item', 'status', 'Status', 'text', FALSE, 'open', NULL::text),
			('work_item', 'priority', 'Priority', 'text', FALSE, 'medium', '^(low|medium|high|critical)$'),
			('asset_item', 'asset_type', 'Asset Type', 'text', FALSE, 'generic', '^[a-z][a-z0-9_\\-]{1,63}$'),
			('asset_item', 'name', 'Name', 'text', FALSE, NULL::text, NULL::text),
			('asset_item', 'status', 'Status', 'text', FALSE, 'active', NULL::text),
			('asset_item', 'criticality', 'Criticality', 'text', TRUE, NULL::text, '^(low|medium|high|critical)$')
	)
	UPDATE _column c
	SET
		label = t.label,
		data_type = t.data_type,
		is_nullable = t.is_nullable,
		default_value = t.default_value,
		validation_regex = t.validation_regex,
		_updated_at = NOW(),
		_updated_by = seed_user_id
	FROM (
		SELECT tbl._id AS table_id, cm.*
		FROM column_meta cm
		JOIN _table tbl ON tbl.name = cm.table_name
	) t
	WHERE c.table_id = t.table_id
	  AND c.name = t.name;

	WITH column_meta(table_name, name, label, data_type, is_nullable, default_value, validation_regex) AS (
		VALUES
			('work_item', 'item_type', 'Type', 'text', FALSE, 'task', '^[a-z][a-z0-9_\\-]{1,63}$'),
			('work_item', 'number', 'Number', 'text', TRUE, NULL::text, NULL::text),
			('work_item', 'title', 'Title', 'text', FALSE, NULL::text, NULL::text),
			('work_item', 'status', 'Status', 'text', FALSE, 'open', NULL::text),
			('work_item', 'priority', 'Priority', 'text', FALSE, 'medium', '^(low|medium|high|critical)$'),
			('asset_item', 'asset_type', 'Asset Type', 'text', FALSE, 'generic', '^[a-z][a-z0-9_\\-]{1,63}$'),
			('asset_item', 'name', 'Name', 'text', FALSE, NULL::text, NULL::text),
			('asset_item', 'status', 'Status', 'text', FALSE, 'active', NULL::text),
			('asset_item', 'criticality', 'Criticality', 'text', TRUE, NULL::text, '^(low|medium|high|critical)$')
	)
	INSERT INTO _column (table_id, name, label, data_type, is_nullable, default_value, validation_regex, _updated_by)
	SELECT tbl._id, cm.name, cm.label, cm.data_type, cm.is_nullable, cm.default_value, cm.validation_regex, seed_user_id
	FROM column_meta cm
	JOIN _table tbl ON tbl.name = cm.table_name
	WHERE NOT EXISTS (
		SELECT 1
		FROM _column c
		WHERE c.table_id = tbl._id
		  AND c.name = cm.name
	);

	IF to_regclass('_menu') IS NOT NULL THEN
		WITH seed_menu(title, href, sort_order) AS (
			VALUES
				('Work', '/t/work_item', 100),
				('Assets', '/t/asset_item', 110)
		)
		INSERT INTO _menu (title, href, "order")
		SELECT s.title, s.href, s.sort_order
		FROM seed_menu s
		WHERE NOT EXISTS (SELECT 1 FROM _menu m WHERE m.href = s.href);

		WITH seed_menu(title, href, sort_order) AS (
			VALUES
				('Work', '/t/work_item', 100),
				('Assets', '/t/asset_item', 110)
		)
		UPDATE _menu m
		SET
			title = s.title,
			"order" = s.sort_order
		FROM seed_menu s
		WHERE m.href = s.href;
	END IF;

	IF to_regclass('_property') IS NOT NULL THEN
		WITH props(key, value) AS (
			VALUES
				('app_name', 'Velm'),
				('security_log_retention_days', '365'),
				('audit_enabled', 'true')
		)
		INSERT INTO _property (key, value)
		SELECT p.key, p.value
		FROM props p
		WHERE NOT EXISTS (SELECT 1 FROM _property pr WHERE pr.key = p.key);
	END IF;
END
$$;
