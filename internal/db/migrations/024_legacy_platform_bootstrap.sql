CREATE EXTENSION IF NOT EXISTS pgcrypto;

DO $$
BEGIN
	IF to_regclass('knowledge_library') IS NULL AND to_regclass('_docs_library') IS NULL THEN
		EXECUTE $sql$
			CREATE TABLE knowledge_library (
				_id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
				slug TEXT NOT NULL UNIQUE,
				name TEXT NOT NULL,
				description TEXT NOT NULL DEFAULT '',
				visibility TEXT NOT NULL DEFAULT 'private',
				owner_user_id TEXT NOT NULL,
				status TEXT NOT NULL DEFAULT 'active',
				_created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
				_updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
				CONSTRAINT chk_knowledge_library_visibility CHECK (visibility IN ('private', 'app', 'public')),
				CONSTRAINT chk_knowledge_library_status CHECK (status IN ('active', 'archived'))
			)
		$sql$;
	END IF;

	IF to_regclass('knowledge_article') IS NULL
		AND to_regclass('_docs_article') IS NULL
		AND to_regclass('_docs_library') IS NULL THEN
		EXECUTE $sql$
			CREATE TABLE knowledge_article (
				_id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
				library_id UUID NOT NULL REFERENCES knowledge_library(_id) ON DELETE CASCADE,
				slug TEXT NOT NULL,
				title TEXT NOT NULL,
				markdown_body TEXT NOT NULL DEFAULT '',
				rendered_html TEXT NOT NULL DEFAULT '',
				status TEXT NOT NULL DEFAULT 'draft',
				tags TEXT NOT NULL DEFAULT '',
				version_num INTEGER NOT NULL DEFAULT 1,
				published_at TIMESTAMPTZ,
				owner_user_id TEXT NOT NULL,
				_created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
				_updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
				CONSTRAINT chk_knowledge_article_status CHECK (status IN ('draft', 'published', 'archived')),
				UNIQUE(library_id, slug)
			)
		$sql$;
	END IF;

	IF to_regclass('knowledge_article_version') IS NULL
		AND to_regclass('_docs_article_version') IS NULL
		AND to_regclass('_docs_article') IS NULL THEN
		EXECUTE $sql$
			CREATE TABLE knowledge_article_version (
				_id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
				article_id UUID NOT NULL REFERENCES knowledge_article(_id) ON DELETE CASCADE,
				version_num INTEGER NOT NULL,
				markdown_body TEXT NOT NULL,
				rendered_html TEXT NOT NULL,
				status TEXT NOT NULL,
				created_by TEXT NOT NULL,
				_created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
				UNIQUE(article_id, version_num)
			)
		$sql$;
	END IF;
END
$$;

DO $$
BEGIN
	IF to_regclass('knowledge_library') IS NOT NULL THEN
		EXECUTE 'CREATE INDEX IF NOT EXISTS idx_knowledge_library_visibility ON knowledge_library(visibility)';
	END IF;

	IF to_regclass('knowledge_article') IS NOT NULL THEN
		EXECUTE 'CREATE INDEX IF NOT EXISTS idx_knowledge_article_library ON knowledge_article(library_id, status)';
		EXECUTE 'CREATE INDEX IF NOT EXISTS idx_knowledge_article_published ON knowledge_article(published_at DESC)';
	END IF;
END
$$;

CREATE TABLE IF NOT EXISTS _app (
	_id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
	_created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
	_updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
	_deleted_at TIMESTAMPTZ,
	_created_by UUID,
	_updated_by UUID,
	_deleted_by UUID,
	name TEXT NOT NULL UNIQUE,
	-- NOT unique: namespace is a table-name prefix shared by several apps.
	-- 'base' carries both the base app (047) and the task_definition app (076).
	-- A UNIQUE here made every schema from migration 076 onward fail on a fresh
	-- database ("duplicate key value violates unique constraint
	-- _app_namespace_key"). Installations created before this file existed kept
	-- their older _app table because of IF NOT EXISTS, which is why only fresh
	-- installs broke. 081 drops the constraint on databases that still have it.
	namespace TEXT NOT NULL,
	label TEXT NOT NULL,
	description TEXT NOT NULL DEFAULT '',
	status TEXT NOT NULL DEFAULT 'active',
	entry_page_slug TEXT,
	definition_yaml TEXT NOT NULL DEFAULT '',
	CONSTRAINT chk_app_name CHECK (name ~ '^[a-z][a-z0-9_]{1,62}$'),
	CONSTRAINT chk_app_namespace CHECK (namespace ~ '^[a-z][a-z0-9_]{1,62}$'),
	CONSTRAINT chk_app_status CHECK (status IN ('active', 'inactive', 'archived'))
);

ALTER TABLE _app
	ADD COLUMN IF NOT EXISTS definition_yaml TEXT NOT NULL DEFAULT '';

CREATE INDEX IF NOT EXISTS idx_app_status ON _app(status);
