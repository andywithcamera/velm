CREATE OR REPLACE FUNCTION _touch_record_version()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
	IF NEW IS DISTINCT FROM OLD THEN
		IF NEW._updated_at IS NOT DISTINCT FROM OLD._updated_at THEN
			NEW._updated_at := NOW();
		END IF;
		NEW._update_count := COALESCE(OLD._update_count, 0) + 1;
	END IF;
	RETURN NEW;
END;
$$;

CREATE OR REPLACE FUNCTION _ensure_record_version_trigger(target_table TEXT)
RETURNS void
LANGUAGE plpgsql
AS $$
DECLARE
	normalized_table TEXT := LOWER(TRIM(target_table));
	has_id BOOLEAN := FALSE;
	has_updated_at BOOLEAN := FALSE;
BEGIN
	IF normalized_table = '' OR to_regclass(normalized_table) IS NULL THEN
		RETURN;
	END IF;

	SELECT EXISTS (
		SELECT 1
		FROM information_schema.columns
		WHERE table_schema = current_schema()
		  AND table_name = normalized_table
		  AND column_name = '_id'
	)
	INTO has_id;

	SELECT EXISTS (
		SELECT 1
		FROM information_schema.columns
		WHERE table_schema = current_schema()
		  AND table_name = normalized_table
		  AND column_name = '_updated_at'
	)
	INTO has_updated_at;

	IF NOT has_id OR NOT has_updated_at THEN
		RETURN;
	END IF;

	EXECUTE format(
		'ALTER TABLE %I ADD COLUMN IF NOT EXISTS _update_count BIGINT NOT NULL DEFAULT 0',
		normalized_table
	);
	EXECUTE format(
		'DROP TRIGGER IF EXISTS trg_touch_record_version ON %I',
		normalized_table
	);
	EXECUTE format(
		'CREATE TRIGGER trg_touch_record_version BEFORE UPDATE ON %I FOR EACH ROW EXECUTE FUNCTION _touch_record_version()',
		normalized_table
	);
END;
$$;

DO $$
DECLARE
	rec RECORD;
BEGIN
	FOR rec IN
		SELECT table_name
		FROM information_schema.columns
		WHERE table_schema = current_schema()
		GROUP BY table_name
		HAVING bool_or(column_name = '_id')
		   AND bool_or(column_name = '_updated_at')
	LOOP
		PERFORM _ensure_record_version_trigger(rec.table_name);
	END LOOP;
END
$$;
