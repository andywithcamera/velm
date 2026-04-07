ALTER TABLE _docs_library
	ADD COLUMN IF NOT EXISTS app_name TEXT NOT NULL DEFAULT '',
	ADD COLUMN IF NOT EXISTS is_default BOOLEAN NOT NULL DEFAULT FALSE,
	ADD COLUMN IF NOT EXISTS create_roles TEXT[] NOT NULL DEFAULT '{}',
	ADD COLUMN IF NOT EXISTS read_roles TEXT[] NOT NULL DEFAULT '{}',
	ADD COLUMN IF NOT EXISTS edit_roles TEXT[] NOT NULL DEFAULT '{}',
	ADD COLUMN IF NOT EXISTS delete_roles TEXT[] NOT NULL DEFAULT '{}';

ALTER TABLE _docs_article
	ADD COLUMN IF NOT EXISTS read_roles TEXT[] NOT NULL DEFAULT '{}',
	ADD COLUMN IF NOT EXISTS edit_roles TEXT[] NOT NULL DEFAULT '{}',
	ADD COLUMN IF NOT EXISTS delete_roles TEXT[] NOT NULL DEFAULT '{}';

CREATE INDEX IF NOT EXISTS idx_docs_library_app_name ON _docs_library(app_name);

CREATE UNIQUE INDEX IF NOT EXISTS idx_docs_library_default_app
	ON _docs_library(app_name)
	WHERE is_default = TRUE AND app_name <> '';
