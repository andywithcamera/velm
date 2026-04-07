DO $$
BEGIN
	IF to_regclass('_column') IS NULL THEN
		RETURN;
	END IF;

	ALTER TABLE _column
		ADD COLUMN IF NOT EXISTS condition_expr TEXT,
		ADD COLUMN IF NOT EXISTS validation_message TEXT;
END
$$;
