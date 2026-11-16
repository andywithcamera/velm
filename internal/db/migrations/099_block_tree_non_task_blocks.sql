-- 099: a structural block calls no definition (Process Designer, Plan T4).
--
-- 078 made child_definition_id NOT NULL because every row was a step calling a
-- definition. A definition tree also holds blocks that call nothing: the
-- trigger that starts the run, an `if`/`for_each`/`end` control block, and a
-- `stage` lane. Those rows still need a position and a parent, so they belong
-- in the same table; what they do not have is a child definition.
--
-- The runtime identifies them by block_kind and stamps them into the run tree
-- as base_task rows, so nothing downstream reads this column for them.

ALTER TABLE base_task_definition_child
	ALTER COLUMN child_definition_id DROP NOT NULL;

ALTER TABLE base_task_definition_child
	DROP CONSTRAINT IF EXISTS chk_base_task_definition_child_child_definition;
ALTER TABLE base_task_definition_child
	ADD CONSTRAINT chk_base_task_definition_child_child_definition CHECK (
		child_definition_id IS NOT NULL
		OR block_kind IN ('trigger', 'control', 'stage')
	);
