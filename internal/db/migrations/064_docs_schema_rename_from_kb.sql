DO $$
DECLARE
	rec RECORD;
BEGIN
	CREATE TEMP TABLE tmp_docs_rename_map (
		old_name TEXT PRIMARY KEY,
		new_name TEXT NOT NULL
	) ON COMMIT DROP;

	INSERT INTO tmp_docs_rename_map (old_name, new_name)
	VALUES
		('_kb_library', '_docs_library'),
		('_kb_article', '_docs_article'),
		('_kb_article_version', '_docs_article_version');

	FOR rec IN
		SELECT old_name, new_name
		FROM tmp_docs_rename_map
	LOOP
		IF to_regclass(rec.old_name) IS NOT NULL AND to_regclass(rec.new_name) IS NULL THEN
			EXECUTE format('ALTER TABLE %I RENAME TO %I', rec.old_name, rec.new_name);
		END IF;
	END LOOP;

	IF to_regclass('_table') IS NOT NULL THEN
		UPDATE _table t
		SET name = m.new_name
		FROM tmp_docs_rename_map m
		WHERE t.name = m.old_name;
	END IF;

	IF to_regclass('_menu') IS NOT NULL THEN
		UPDATE _menu mnu
		SET href = regexp_replace(mnu.href, '^/t/' || m.old_name || '$', '/t/' || m.new_name)
		FROM tmp_docs_rename_map m
		WHERE mnu.href = '/t/' || m.old_name;
	END IF;

	IF to_regclass('_saved_view') IS NOT NULL THEN
		UPDATE _saved_view sv
		SET table_name = m.new_name
		FROM tmp_docs_rename_map m
		WHERE sv.table_name = m.old_name;
	END IF;

	IF to_regclass('_page') IS NOT NULL THEN
		UPDATE _page pg
		SET content = replace(
			replace(pg.content, '/t/' || m.old_name, '/t/' || m.new_name),
			'/f/' || m.old_name,
			'/f/' || m.new_name
		)
		FROM tmp_docs_rename_map m
		WHERE pg.content LIKE '%' || m.old_name || '%';
	END IF;

	IF to_regclass('_page_version') IS NOT NULL THEN
		UPDATE _page_version pg
		SET content = replace(
			replace(pg.content, '/t/' || m.old_name, '/t/' || m.new_name),
			'/f/' || m.old_name,
			'/f/' || m.new_name
		)
		FROM tmp_docs_rename_map m
		WHERE pg.content LIKE '%' || m.old_name || '%';
	END IF;
END
$$;

DO $$
BEGIN
	IF EXISTS (
		SELECT 1
		FROM information_schema.columns
		WHERE table_schema = current_schema()
		  AND table_name = '_docs_article'
		  AND column_name = 'library_id'
	) AND NOT EXISTS (
		SELECT 1
		FROM information_schema.columns
		WHERE table_schema = current_schema()
		  AND table_name = '_docs_article'
		  AND column_name = 'docs_library_id'
	) THEN
		ALTER TABLE _docs_article RENAME COLUMN library_id TO docs_library_id;
	END IF;

	IF EXISTS (
		SELECT 1
		FROM information_schema.columns
		WHERE table_schema = current_schema()
		  AND table_name = '_docs_article_version'
		  AND column_name = 'article_id'
	) AND NOT EXISTS (
		SELECT 1
		FROM information_schema.columns
		WHERE table_schema = current_schema()
		  AND table_name = '_docs_article_version'
		  AND column_name = 'docs_article_id'
	) THEN
		ALTER TABLE _docs_article_version RENAME COLUMN article_id TO docs_article_id;
	END IF;
END
$$;

DO $$
BEGIN
	IF to_regclass('_kb_library__id_seq') IS NOT NULL AND to_regclass('_docs_library__id_seq') IS NULL THEN
		ALTER SEQUENCE _kb_library__id_seq RENAME TO _docs_library__id_seq;
	END IF;
	IF to_regclass('_kb_article__id_seq') IS NOT NULL AND to_regclass('_docs_article__id_seq') IS NULL THEN
		ALTER SEQUENCE _kb_article__id_seq RENAME TO _docs_article__id_seq;
	END IF;
	IF to_regclass('_kb_article_version__id_seq') IS NOT NULL AND to_regclass('_docs_article_version__id_seq') IS NULL THEN
		ALTER SEQUENCE _kb_article_version__id_seq RENAME TO _docs_article_version__id_seq;
	END IF;

	IF to_regclass('idx_kb_library_app_name') IS NOT NULL AND to_regclass('idx_docs_library_app_name') IS NULL THEN
		ALTER INDEX idx_kb_library_app_name RENAME TO idx_docs_library_app_name;
	END IF;
	IF to_regclass('idx_kb_library_default_app') IS NOT NULL AND to_regclass('idx_docs_library_default_app') IS NULL THEN
		ALTER INDEX idx_kb_library_default_app RENAME TO idx_docs_library_default_app;
	END IF;
	IF to_regclass('idx_kb_article_number') IS NOT NULL AND to_regclass('idx_docs_article_number') IS NULL THEN
		ALTER INDEX idx_kb_article_number RENAME TO idx_docs_article_number;
	END IF;
	IF to_regclass('idx_knowledge_library_visibility') IS NOT NULL AND to_regclass('idx_docs_library_visibility') IS NULL THEN
		ALTER INDEX idx_knowledge_library_visibility RENAME TO idx_docs_library_visibility;
	END IF;
	IF to_regclass('idx_knowledge_article_library') IS NOT NULL AND to_regclass('idx_docs_article_library') IS NULL THEN
		ALTER INDEX idx_knowledge_article_library RENAME TO idx_docs_article_library;
	END IF;
	IF to_regclass('idx_knowledge_article_published') IS NOT NULL AND to_regclass('idx_docs_article_published') IS NULL THEN
		ALTER INDEX idx_knowledge_article_published RENAME TO idx_docs_article_published;
	END IF;
END
$$;

DO $$
BEGIN
	IF to_regclass('kb_article_doc_number_seq') IS NOT NULL AND to_regclass('docs_article_doc_number_seq') IS NULL THEN
		ALTER SEQUENCE kb_article_doc_number_seq RENAME TO docs_article_doc_number_seq;
	END IF;
END
$$;

CREATE SEQUENCE IF NOT EXISTS docs_article_doc_number_seq START WITH 1 INCREMENT BY 1;

DO $$
DECLARE
	max_number BIGINT;
BEGIN
	SELECT MAX(substring(number FROM '([0-9]+)$')::BIGINT)
	INTO max_number
	FROM _docs_article
	WHERE number ~ '^DOC[0-9]{6,}$';

	IF max_number IS NULL OR max_number < 1 THEN
		PERFORM setval('docs_article_doc_number_seq', 1, FALSE);
	ELSE
		PERFORM setval('docs_article_doc_number_seq', max_number, TRUE);
	END IF;
END
$$;

DROP TRIGGER IF EXISTS trg_kb_article_assign_number ON _docs_article;
DROP TRIGGER IF EXISTS trg_docs_article_assign_number ON _docs_article;
DROP FUNCTION IF EXISTS kb_article_assign_number();
DROP FUNCTION IF EXISTS kb_next_doc_number();

CREATE OR REPLACE FUNCTION docs_next_doc_number()
RETURNS TEXT
LANGUAGE plpgsql
AS $$
DECLARE
	next_value BIGINT;
BEGIN
	next_value := nextval('docs_article_doc_number_seq');
	RETURN 'DOC' || LPAD(next_value::TEXT, 6, '0');
END;
$$;

CREATE OR REPLACE FUNCTION docs_article_assign_number()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
	IF COALESCE(BTRIM(NEW.number), '') = '' THEN
		NEW.number := docs_next_doc_number();
	END IF;
	RETURN NEW;
END;
$$;

CREATE TRIGGER trg_docs_article_assign_number
BEFORE INSERT OR UPDATE ON _docs_article
FOR EACH ROW
EXECUTE FUNCTION docs_article_assign_number();
