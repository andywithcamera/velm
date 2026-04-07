DO $$
DECLARE
	itsm_app_ids TEXT[] := ARRAY[]::TEXT[];
	legacy_script_def_ids BIGINT[] := ARRAY[]::BIGINT[];
	table_row RECORD;
BEGIN
	IF to_regclass('_app') IS NOT NULL THEN
		SELECT COALESCE(array_agg(_id::text), ARRAY[]::TEXT[])
		INTO itsm_app_ids
		FROM _app
		WHERE name = 'itsm' OR namespace = 'itsm';
	END IF;

	IF to_regclass('script_def') IS NOT NULL THEN
		SELECT COALESCE(array_agg(d._id), ARRAY[]::BIGINT[])
		INTO legacy_script_def_ids
		FROM script_def d
		WHERE d.scope = 'itsm'
		   OR EXISTS (
				SELECT 1
				FROM script_binding b
				WHERE b.script_def_id = d._id
				  AND (
						b.app_id = 'itsm'
						OR b.table_name LIKE 'itsm\_%' ESCAPE '\'
				  )
			);

		IF to_regclass('script_execution_log') IS NOT NULL AND cardinality(legacy_script_def_ids) > 0 THEN
			DELETE FROM script_execution_log
			WHERE script_def_id = ANY(legacy_script_def_ids);
		END IF;

		IF cardinality(legacy_script_def_ids) > 0 THEN
			DELETE FROM script_def
			WHERE _id = ANY(legacy_script_def_ids);
		END IF;
	END IF;

	IF to_regclass('_script') IS NOT NULL THEN
		DELETE FROM _script
		WHERE scope = 'itsm'
		   OR table_name LIKE 'itsm\_%' ESCAPE '\';
	END IF;

	IF to_regclass('_record_comment') IS NOT NULL THEN
		DELETE FROM _record_comment
		WHERE table_name LIKE 'itsm\_%' ESCAPE '\';
	END IF;

	IF to_regclass('_audit_data_change') IS NOT NULL THEN
		DELETE FROM _audit_data_change
		WHERE table_name LIKE 'itsm\_%' ESCAPE '\';
	END IF;

	IF to_regclass('_saved_view') IS NOT NULL THEN
		DELETE FROM _saved_view
		WHERE table_name LIKE 'itsm\_%' ESCAPE '\'
		   OR app_id = 'itsm'
		   OR app_id = ANY(itsm_app_ids);
	END IF;

	IF to_regclass('_builder_schema_job_step') IS NOT NULL AND to_regclass('_builder_schema_job') IS NOT NULL THEN
		DELETE FROM _builder_schema_job_step
		WHERE job_id IN (
			SELECT _id
			FROM _builder_schema_job
			WHERE table_name LIKE 'itsm\_%' ESCAPE '\'
		);
	END IF;

	IF to_regclass('_builder_schema_job') IS NOT NULL THEN
		DELETE FROM _builder_schema_job
		WHERE table_name LIKE 'itsm\_%' ESCAPE '\';
	END IF;

	IF to_regclass('_builder_schema_change') IS NOT NULL THEN
		DELETE FROM _builder_schema_change
		WHERE table_name LIKE 'itsm\_%' ESCAPE '\';
	END IF;

	IF to_regclass('_page_version') IS NOT NULL THEN
		DELETE FROM _page_version
		WHERE page_slug IN ('incident-ops', 'problem-mgmt', 'change-control', 'request-fulfilment', 'cmdb');
	END IF;

	IF to_regclass('_page') IS NOT NULL THEN
		DELETE FROM _page
		WHERE slug IN ('incident-ops', 'problem-mgmt', 'change-control', 'request-fulfilment', 'cmdb');
	END IF;

	FOR table_row IN
		SELECT tablename
		FROM pg_tables
		WHERE schemaname = 'public'
		  AND tablename LIKE 'itsm\_%' ESCAPE '\'
	LOOP
		EXECUTE format('DROP TABLE IF EXISTS %I CASCADE', table_row.tablename);
	END LOOP;

	IF to_regclass('_app') IS NOT NULL THEN
		DELETE FROM _app
		WHERE name = 'itsm' OR namespace = 'itsm';
	END IF;
END
$$;
