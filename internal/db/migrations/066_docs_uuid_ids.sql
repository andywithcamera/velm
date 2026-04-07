CREATE EXTENSION IF NOT EXISTS pgcrypto;

DO $$
DECLARE
	library_id_type TEXT;
	doc_map RECORD;
	pattern TEXT;
	replacement TEXT;
BEGIN
	IF to_regclass('_docs_library') IS NULL
		OR to_regclass('_docs_article') IS NULL
		OR to_regclass('_docs_article_version') IS NULL THEN
		RETURN;
	END IF;

	SELECT data_type
	INTO library_id_type
	FROM information_schema.columns
	WHERE table_schema = current_schema()
	  AND table_name = '_docs_library'
	  AND column_name = '_id';

	IF library_id_type IS NULL OR library_id_type = 'uuid' THEN
		RETURN;
	END IF;

	ALTER TABLE _docs_library ADD COLUMN IF NOT EXISTS _id_uuid UUID;
	UPDATE _docs_library
	SET _id_uuid = COALESCE(_id_uuid, gen_random_uuid())
	WHERE _id_uuid IS NULL;
	ALTER TABLE _docs_library ALTER COLUMN _id_uuid SET NOT NULL;
	ALTER TABLE _docs_library ALTER COLUMN _id_uuid SET DEFAULT gen_random_uuid();

	ALTER TABLE _docs_article ADD COLUMN IF NOT EXISTS _id_uuid UUID;
	UPDATE _docs_article
	SET _id_uuid = COALESCE(_id_uuid, gen_random_uuid())
	WHERE _id_uuid IS NULL;
	ALTER TABLE _docs_article ALTER COLUMN _id_uuid SET NOT NULL;
	ALTER TABLE _docs_article ALTER COLUMN _id_uuid SET DEFAULT gen_random_uuid();

	ALTER TABLE _docs_article_version ADD COLUMN IF NOT EXISTS _id_uuid UUID;
	UPDATE _docs_article_version
	SET _id_uuid = COALESCE(_id_uuid, gen_random_uuid())
	WHERE _id_uuid IS NULL;
	ALTER TABLE _docs_article_version ALTER COLUMN _id_uuid SET NOT NULL;
	ALTER TABLE _docs_article_version ALTER COLUMN _id_uuid SET DEFAULT gen_random_uuid();

	ALTER TABLE _docs_article ADD COLUMN IF NOT EXISTS docs_library_id_uuid UUID;
	UPDATE _docs_article AS article
	SET docs_library_id_uuid = library._id_uuid
	FROM _docs_library AS library
	WHERE article.docs_library_id_uuid IS NULL
	  AND article.docs_library_id = library._id;
	ALTER TABLE _docs_article ALTER COLUMN docs_library_id_uuid SET NOT NULL;

	ALTER TABLE _docs_article_version ADD COLUMN IF NOT EXISTS docs_article_id_uuid UUID;
	UPDATE _docs_article_version AS version_row
	SET docs_article_id_uuid = article._id_uuid
	FROM _docs_article AS article
	WHERE version_row.docs_article_id_uuid IS NULL
	  AND version_row.docs_article_id = article._id;
	ALTER TABLE _docs_article_version ALTER COLUMN docs_article_id_uuid SET NOT NULL;

	FOR doc_map IN
		SELECT _id::text AS old_id, _id_uuid::text AS new_id
		FROM _docs_article
	LOOP
		pattern := '(^|[^0-9A-Za-z])(/d/)' || doc_map.old_id || '([^0-9]|$)';
		replacement := E'\\1\\2' || doc_map.new_id || E'\\3';

		UPDATE _docs_article
		SET markdown_body = regexp_replace(markdown_body, pattern, replacement, 'g'),
			rendered_html = regexp_replace(rendered_html, pattern, replacement, 'g')
		WHERE markdown_body ~ pattern
		   OR rendered_html ~ pattern;

		UPDATE _docs_article_version
		SET markdown_body = regexp_replace(markdown_body, pattern, replacement, 'g'),
			rendered_html = regexp_replace(rendered_html, pattern, replacement, 'g')
		WHERE markdown_body ~ pattern
		   OR rendered_html ~ pattern;

		IF to_regclass('_page') IS NOT NULL THEN
			UPDATE _page
			SET content = regexp_replace(content, pattern, replacement, 'g')
			WHERE COALESCE(content, '') ~ pattern;
		END IF;

		IF to_regclass('_page_version') IS NOT NULL THEN
			UPDATE _page_version
			SET content = regexp_replace(content, pattern, replacement, 'g')
			WHERE COALESCE(content, '') ~ pattern;
		END IF;
	END LOOP;

	ALTER TABLE _docs_article_version DROP CONSTRAINT IF EXISTS knowledge_article_version_article_id_fkey;
	ALTER TABLE _docs_article_version DROP CONSTRAINT IF EXISTS _kb_article_version_article_id_fkey;
	ALTER TABLE _docs_article_version DROP CONSTRAINT IF EXISTS _docs_article_version_article_id_fkey;
	ALTER TABLE _docs_article_version DROP CONSTRAINT IF EXISTS _docs_article_version_docs_article_id_fkey;
	ALTER TABLE _docs_article_version DROP CONSTRAINT IF EXISTS knowledge_article_version_article_id_version_num_key;
	ALTER TABLE _docs_article_version DROP CONSTRAINT IF EXISTS _kb_article_version_article_id_version_num_key;
	ALTER TABLE _docs_article_version DROP CONSTRAINT IF EXISTS _docs_article_version_article_id_version_num_key;
	ALTER TABLE _docs_article_version DROP CONSTRAINT IF EXISTS _docs_article_version_docs_article_id_version_num_key;

	ALTER TABLE _docs_article DROP CONSTRAINT IF EXISTS knowledge_article_library_id_fkey;
	ALTER TABLE _docs_article DROP CONSTRAINT IF EXISTS _kb_article_library_id_fkey;
	ALTER TABLE _docs_article DROP CONSTRAINT IF EXISTS _docs_article_library_id_fkey;
	ALTER TABLE _docs_article DROP CONSTRAINT IF EXISTS _docs_article_docs_library_id_fkey;
	ALTER TABLE _docs_article DROP CONSTRAINT IF EXISTS knowledge_article_library_id_slug_key;
	ALTER TABLE _docs_article DROP CONSTRAINT IF EXISTS _kb_article_library_id_slug_key;
	ALTER TABLE _docs_article DROP CONSTRAINT IF EXISTS _docs_article_library_id_slug_key;
	ALTER TABLE _docs_article DROP CONSTRAINT IF EXISTS _docs_article_docs_library_id_slug_key;

	ALTER TABLE _docs_article_version DROP CONSTRAINT IF EXISTS knowledge_article_version_pkey;
	ALTER TABLE _docs_article_version DROP CONSTRAINT IF EXISTS _kb_article_version_pkey;
	ALTER TABLE _docs_article_version DROP CONSTRAINT IF EXISTS _docs_article_version_pkey;
	ALTER TABLE _docs_article DROP CONSTRAINT IF EXISTS knowledge_article_pkey;
	ALTER TABLE _docs_article DROP CONSTRAINT IF EXISTS _kb_article_pkey;
	ALTER TABLE _docs_article DROP CONSTRAINT IF EXISTS _docs_article_pkey;
	ALTER TABLE _docs_library DROP CONSTRAINT IF EXISTS knowledge_library_pkey;
	ALTER TABLE _docs_library DROP CONSTRAINT IF EXISTS _kb_library_pkey;
	ALTER TABLE _docs_library DROP CONSTRAINT IF EXISTS _docs_library_pkey;

	DROP INDEX IF EXISTS idx_docs_article_library;
	DROP INDEX IF EXISTS idx_docs_article_slug_per_library;
	DROP INDEX IF EXISTS idx_docs_article_version_article_version_num;

	ALTER TABLE _docs_article_version DROP COLUMN docs_article_id;
	ALTER TABLE _docs_article_version RENAME COLUMN docs_article_id_uuid TO docs_article_id;

	ALTER TABLE _docs_article DROP COLUMN docs_library_id;
	ALTER TABLE _docs_article RENAME COLUMN docs_library_id_uuid TO docs_library_id;

	ALTER TABLE _docs_article_version DROP COLUMN _id;
	ALTER TABLE _docs_article_version RENAME COLUMN _id_uuid TO _id;

	ALTER TABLE _docs_article DROP COLUMN _id;
	ALTER TABLE _docs_article RENAME COLUMN _id_uuid TO _id;

	ALTER TABLE _docs_library DROP COLUMN _id;
	ALTER TABLE _docs_library RENAME COLUMN _id_uuid TO _id;

	ALTER TABLE _docs_library ADD CONSTRAINT _docs_library_pkey PRIMARY KEY (_id);
	ALTER TABLE _docs_article ADD CONSTRAINT _docs_article_pkey PRIMARY KEY (_id);
	ALTER TABLE _docs_article_version ADD CONSTRAINT _docs_article_version_pkey PRIMARY KEY (_id);

	ALTER TABLE _docs_article
		ADD CONSTRAINT _docs_article_docs_library_id_fkey
		FOREIGN KEY (docs_library_id) REFERENCES _docs_library(_id) ON DELETE CASCADE;

	ALTER TABLE _docs_article_version
		ADD CONSTRAINT _docs_article_version_docs_article_id_fkey
		FOREIGN KEY (docs_article_id) REFERENCES _docs_article(_id) ON DELETE CASCADE;

	CREATE UNIQUE INDEX IF NOT EXISTS idx_docs_article_slug_per_library
		ON _docs_article(docs_library_id, slug);

	CREATE UNIQUE INDEX IF NOT EXISTS idx_docs_article_version_article_version_num
		ON _docs_article_version(docs_article_id, version_num);

	CREATE INDEX IF NOT EXISTS idx_docs_article_library
		ON _docs_article(docs_library_id, status);

	DROP SEQUENCE IF EXISTS knowledge_library_id_seq;
	DROP SEQUENCE IF EXISTS knowledge_article_id_seq;
	DROP SEQUENCE IF EXISTS knowledge_article_version_id_seq;
	DROP SEQUENCE IF EXISTS _kb_library__id_seq;
	DROP SEQUENCE IF EXISTS _kb_article__id_seq;
	DROP SEQUENCE IF EXISTS _kb_article_version__id_seq;
	DROP SEQUENCE IF EXISTS _docs_library__id_seq;
	DROP SEQUENCE IF EXISTS _docs_article__id_seq;
	DROP SEQUENCE IF EXISTS _docs_article_version__id_seq;
END
$$;
