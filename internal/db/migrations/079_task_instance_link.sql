-- Instance-side link (issue #112). base_task rows spawned from a
-- definition carry definition_id. Nullable: hand-made tasks have none.
-- Closing a definition does not cascade-delete running instances.

ALTER TABLE base_task
	ADD COLUMN IF NOT EXISTS definition_id UUID REFERENCES base_task_definition(_id) ON DELETE SET NULL;

CREATE INDEX IF NOT EXISTS idx_base_task_definition ON base_task(definition_id);

-- 076 omitted the standard soft-delete column on base_task_definition; the
-- engine filters on it.
ALTER TABLE base_task_definition
	ADD COLUMN IF NOT EXISTS _deleted_at TIMESTAMPTZ;

-- Audit + record-version plumbing for the catalog table (009 pattern).
SELECT _ensure_record_version_trigger('base_task_definition');
