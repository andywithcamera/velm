-- Catalog category for task definitions (task-catalog page, 2026-11-09).
-- A free label the catalog page groups by; 'general' is the fallback bucket.

ALTER TABLE base_task_definition
	ADD COLUMN IF NOT EXISTS category TEXT NOT NULL DEFAULT 'general';

-- Backfill from slug prefixes so the first catalog render is grouped.
UPDATE base_task_definition SET category = CASE
	WHEN slug ~ '^(write-doc|publish-doc|docs)' THEN 'documentation'
	WHEN slug ~ '^(qa|test)' THEN 'quality'
	WHEN slug ~ '^(deploy|release|rollback|hotfix)' THEN 'release'
	WHEN slug ~ '^(security|vuln|audit|access)' THEN 'security'
	WHEN slug ~ '^(design|ux|brand)' THEN 'design'
	WHEN slug ~ '^(reboot|provision|backup|ops|monitor)' THEN 'operations'
	WHEN slug ~ '^(feature|bug|chore|spike|refactor|research)' THEN 'product'
	ELSE 'general'
END
WHERE category = 'general';
