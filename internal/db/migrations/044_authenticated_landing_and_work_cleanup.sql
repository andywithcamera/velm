DO $$
BEGIN
	IF to_regclass('_page') IS NOT NULL THEN
		INSERT INTO _page (name, slug, content, editor_mode, status, published_at)
		SELECT
			'Landing',
			'landing',
			'<section><h1>Welcome</h1><p>This is the default landing page for signed-in users.</p><p>Use the navigation to open tables, pages, and admin tools. Update the <code>authenticated_root_route_target</code> system property to send users somewhere else after login.</p></section>',
			'html',
			'published',
			NOW()
		WHERE NOT EXISTS (SELECT 1 FROM _page WHERE slug = 'landing');
	END IF;

	IF to_regclass('_property') IS NOT NULL THEN
		INSERT INTO _property (key, value)
		SELECT 'authenticated_root_route_target', '/task'
		WHERE NOT EXISTS (
			SELECT 1
			FROM _property
			WHERE key = 'authenticated_root_route_target'
		);
	END IF;

	IF to_regclass('_saved_view') IS NOT NULL THEN
		DELETE FROM _saved_view
		WHERE table_name = '_work';
	END IF;

	IF to_regclass('_user_preference') IS NOT NULL THEN
		DELETE FROM _user_preference
		WHERE namespace = 'list_view'
		  AND split_part(pref_key, ':', 2) = '_work';
	END IF;

	IF to_regclass('_record_comment') IS NOT NULL THEN
		DELETE FROM _record_comment
		WHERE table_name = '_work';
	END IF;

	IF to_regclass('_audit_data_change') IS NOT NULL THEN
		DELETE FROM _audit_data_change
		WHERE table_name = '_work';
	END IF;

	IF to_regclass('_column') IS NOT NULL AND to_regclass('_table') IS NOT NULL THEN
		DELETE FROM _column
		WHERE table_id IN (
			SELECT _id
			FROM _table
			WHERE name = '_work'
		);
	END IF;

	IF to_regclass('_table') IS NOT NULL THEN
		DELETE FROM _table
		WHERE name = '_work';
	END IF;

	DROP TABLE IF EXISTS _work CASCADE;
END
$$;
