ALTER TABLE _docs_article
	ADD COLUMN IF NOT EXISTS number TEXT;

CREATE SEQUENCE IF NOT EXISTS docs_article_doc_number_seq START WITH 1 INCREMENT BY 1;

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

WITH numbered_articles AS (
	SELECT
		_id,
		'DOC' || LPAD(ROW_NUMBER() OVER (ORDER BY _id)::TEXT, 6, '0') AS next_number
	FROM _docs_article
	WHERE COALESCE(BTRIM(number), '') = ''
)
UPDATE _docs_article AS article
SET number = numbered_articles.next_number
FROM numbered_articles
WHERE article._id = numbered_articles._id;

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
END;
$$;

ALTER TABLE _docs_article
	ALTER COLUMN number SET NOT NULL;

CREATE UNIQUE INDEX IF NOT EXISTS idx_docs_article_number
	ON _docs_article(number);

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

DROP TRIGGER IF EXISTS trg_docs_article_assign_number ON _docs_article;
CREATE TRIGGER trg_docs_article_assign_number
BEFORE INSERT OR UPDATE ON _docs_article
FOR EACH ROW
EXECUTE FUNCTION docs_article_assign_number();
