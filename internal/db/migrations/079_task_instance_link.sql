-- Instance-side link (issue #112). base_task rows spawned from a
-- definition carry definition_id. Nullable: hand-made tasks have none.
-- Closing a definition does not cascade-delete running instances.

ALTER TABLE base_task
	ADD COLUMN IF NOT EXISTS definition_id UUID REFERENCES _task_definition(_id) ON DELETE SET NULL;

CREATE INDEX IF NOT EXISTS idx_base_task_definition ON base_task(definition_id);
