-- Container child wiring (issue #112). A container definition declares an
-- ordered list of child definitions; children spawn at parent spawn, in
-- pending. Inputs flow into children when the parent starts (engine binds
-- from the parent's variable scope via input_mapping).

CREATE TABLE IF NOT EXISTS _task_definition_child (
	_id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
	container_definition_id UUID NOT NULL REFERENCES _task_definition(_id) ON DELETE CASCADE,
	child_definition_id UUID NOT NULL REFERENCES _task_definition(_id) ON DELETE CASCADE,
	sort_order INT NOT NULL DEFAULT 0,
	-- { "child_input_name": "$parent.outputs.x" | literal }
	input_mapping JSONB NOT NULL DEFAULT '{}'::jsonb,
	_created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
	CONSTRAINT uq_task_definition_child UNIQUE (container_definition_id, child_definition_id)
);

CREATE INDEX IF NOT EXISTS idx_task_definition_child_container
	ON _task_definition_child(container_definition_id, sort_order);
