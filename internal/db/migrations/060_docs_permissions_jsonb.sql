CREATE OR REPLACE FUNCTION _convert_docs_roles_column(target_table TEXT, target_column TEXT)
RETURNS VOID
LANGUAGE plpgsql
AS $$
DECLARE
	column_udt_name TEXT;
BEGIN
	SELECT udt_name
	INTO column_udt_name
	FROM information_schema.columns
	WHERE table_schema = current_schema()
	  AND table_name = target_table
	  AND column_name = target_column;

	IF column_udt_name IS NULL THEN
		EXECUTE format(
			'ALTER TABLE %I ADD COLUMN %I JSONB NOT NULL DEFAULT ''[]''::jsonb',
			target_table,
			target_column
		);
		RETURN;
	END IF;

	IF column_udt_name = '_text' THEN
		EXECUTE format(
			'ALTER TABLE %I RENAME COLUMN %I TO %I',
			target_table,
			target_column,
			target_column || '_legacy_array'
		);
		EXECUTE format(
			'ALTER TABLE %I ADD COLUMN %I JSONB NOT NULL DEFAULT ''[]''::jsonb',
			target_table,
			target_column
		);
		EXECUTE format(
			'UPDATE %I SET %I = to_jsonb(COALESCE(%I, ARRAY[]::TEXT[]))',
			target_table,
			target_column,
			target_column || '_legacy_array'
		);
		EXECUTE format(
			'ALTER TABLE %I DROP COLUMN %I',
			target_table,
			target_column || '_legacy_array'
		);
	END IF;
END;
$$;

SELECT _convert_docs_roles_column('_docs_library', 'create_roles');
SELECT _convert_docs_roles_column('_docs_library', 'read_roles');
SELECT _convert_docs_roles_column('_docs_library', 'edit_roles');
SELECT _convert_docs_roles_column('_docs_library', 'delete_roles');
SELECT _convert_docs_roles_column('_docs_article', 'read_roles');
SELECT _convert_docs_roles_column('_docs_article', 'edit_roles');
SELECT _convert_docs_roles_column('_docs_article', 'delete_roles');

DROP FUNCTION _convert_docs_roles_column(TEXT, TEXT);
