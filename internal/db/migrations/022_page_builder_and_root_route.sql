DO $$
BEGIN
	IF to_regclass('_page') IS NOT NULL THEN
		ALTER TABLE _page
			ADD COLUMN IF NOT EXISTS editor_mode TEXT NOT NULL DEFAULT 'wysiwyg';

		IF NOT EXISTS (
			SELECT 1 FROM pg_constraint WHERE conname = 'chk_page_editor_mode'
		) THEN
			ALTER TABLE _page
				ADD CONSTRAINT chk_page_editor_mode CHECK (editor_mode IN ('wysiwyg', 'html'));
		END IF;
	END IF;

	IF to_regclass('_property') IS NOT NULL THEN
		INSERT INTO _property (key, value)
		SELECT 'root_route_target', 'login'
		WHERE NOT EXISTS (SELECT 1 FROM _property WHERE key = 'root_route_target');
	END IF;
END
$$;
