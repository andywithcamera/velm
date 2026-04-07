DO $$
BEGIN
	IF to_regclass('_script') IS NOT NULL AND EXISTS (
		SELECT 1
		FROM _script
		WHERE _deleted_at IS NULL
	) THEN
		RAISE EXCEPTION 'cannot drop _script while active script rows still exist; migrate them into app YAML first';
	END IF;

	IF to_regclass('script_def') IS NOT NULL AND EXISTS (
		SELECT 1
		FROM script_def
	) THEN
		RAISE EXCEPTION 'cannot drop legacy script tables while script_def rows still exist; migrate them into app YAML first';
	END IF;

	IF to_regclass('_saved_view') IS NOT NULL THEN
		DELETE FROM _saved_view
		WHERE table_name IN ('_script', 'script_def', 'script_version', 'script_binding', 'script_execution_log');
	END IF;

	IF to_regclass('_user_preference') IS NOT NULL THEN
		DELETE FROM _user_preference
		WHERE namespace = 'list_view'
		  AND split_part(pref_key, ':', 2) IN ('_script', 'script_def', 'script_version', 'script_binding', 'script_execution_log');
	END IF;

	IF to_regclass('_record_comment') IS NOT NULL THEN
		DELETE FROM _record_comment
		WHERE table_name IN ('_script', 'script_def', 'script_version', 'script_binding', 'script_execution_log');
	END IF;

	IF to_regclass('_audit_data_change') IS NOT NULL THEN
		DELETE FROM _audit_data_change
		WHERE table_name IN ('_script', 'script_def', 'script_version', 'script_binding', 'script_execution_log');
	END IF;

	IF to_regclass('_column') IS NOT NULL AND to_regclass('_table') IS NOT NULL THEN
		DELETE FROM _column
		WHERE table_id IN (
			SELECT _id
			FROM _table
			WHERE name IN ('_script', 'script_def', 'script_version', 'script_binding', 'script_execution_log')
		);
	END IF;

	IF to_regclass('_table') IS NOT NULL THEN
		DELETE FROM _table
		WHERE name IN ('_script', 'script_def', 'script_version', 'script_binding', 'script_execution_log');
	END IF;

	DROP TABLE IF EXISTS _script CASCADE;
	DROP TABLE IF EXISTS script_execution_log CASCADE;
	DROP TABLE IF EXISTS script_binding CASCADE;
	DROP TABLE IF EXISTS script_version CASCADE;
	DROP TABLE IF EXISTS script_def CASCADE;
END
$$;
