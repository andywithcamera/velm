DO $$
BEGIN
	IF to_regclass('_column') IS NULL THEN
		RAISE NOTICE 'Skipping _column validation metadata enrichment: _column does not exist';
		RETURN;
	END IF;

	ALTER TABLE _column
		ADD COLUMN IF NOT EXISTS is_unique BOOLEAN NOT NULL DEFAULT FALSE,
		ADD COLUMN IF NOT EXISTS is_indexed BOOLEAN NOT NULL DEFAULT FALSE,
		ADD COLUMN IF NOT EXISTS max_length INTEGER;

	-- Core identifiers are generally unique and indexed.
	UPDATE _column c
	SET
		is_unique = TRUE,
		is_indexed = TRUE,
		max_length = COALESCE(max_length, 64)
	FROM _table t
	WHERE c.table_id = t._id
	  AND c.name = 'number'
	  AND t.name IN ('work_item', 'asset_item');

	-- Common short text constraints.
	UPDATE _column c
	SET max_length = COALESCE(max_length, 160)
	FROM _table t
	WHERE c.table_id = t._id
	  AND c.name IN ('title', 'name')
	  AND t.name IN ('work_item', 'asset_item');

	UPDATE _column c
	SET
		max_length = COALESCE(max_length, 32),
		is_indexed = TRUE
	FROM _table t
	WHERE c.table_id = t._id
	  AND c.name IN ('status', 'priority', 'criticality', 'item_type', 'asset_type')
	  AND t.name IN ('work_item', 'asset_item');
END
$$;
