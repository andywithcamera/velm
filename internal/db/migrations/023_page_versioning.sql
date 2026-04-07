DO $$
BEGIN
	IF to_regclass('_page') IS NULL THEN
		RETURN;
	END IF;

	ALTER TABLE _page
		ADD COLUMN IF NOT EXISTS status TEXT NOT NULL DEFAULT 'draft',
		ADD COLUMN IF NOT EXISTS published_at TIMESTAMPTZ;

	IF NOT EXISTS (
		SELECT 1 FROM pg_constraint WHERE conname = 'chk_page_status'
	) THEN
		ALTER TABLE _page
			ADD CONSTRAINT chk_page_status CHECK (status IN ('draft', 'published'));
	END IF;

	CREATE TABLE IF NOT EXISTS _page_version (
		_id BIGSERIAL PRIMARY KEY,
		page_slug TEXT NOT NULL,
		version_num INTEGER NOT NULL,
		name TEXT NOT NULL,
		content TEXT NOT NULL,
		editor_mode TEXT NOT NULL DEFAULT 'wysiwyg',
		status TEXT NOT NULL DEFAULT 'draft',
		created_by TEXT,
		_created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
		CONSTRAINT chk_page_version_editor_mode CHECK (editor_mode IN ('wysiwyg', 'html')),
		CONSTRAINT chk_page_version_status CHECK (status IN ('draft', 'published')),
		UNIQUE(page_slug, version_num)
	);

	CREATE INDEX IF NOT EXISTS idx_page_version_slug ON _page_version(page_slug, version_num DESC);
END
$$;
