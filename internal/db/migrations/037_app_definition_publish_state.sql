ALTER TABLE _app
	ADD COLUMN IF NOT EXISTS published_definition_yaml TEXT NOT NULL DEFAULT '',
	ADD COLUMN IF NOT EXISTS definition_version BIGINT NOT NULL DEFAULT 0,
	ADD COLUMN IF NOT EXISTS published_version BIGINT NOT NULL DEFAULT 0;

UPDATE _app
SET published_definition_yaml = COALESCE(NULLIF(published_definition_yaml, ''), definition_yaml),
	definition_version = CASE
		WHEN COALESCE(NULLIF(definition_yaml, ''), '') = '' THEN definition_version
		WHEN definition_version > 0 THEN definition_version
		ELSE 1
	END,
	published_version = CASE
		WHEN COALESCE(NULLIF(COALESCE(NULLIF(published_definition_yaml, ''), definition_yaml), ''), '') = '' THEN published_version
		WHEN published_version > 0 THEN published_version
		ELSE GREATEST(definition_version, 1)
	END
WHERE _deleted_at IS NULL;
