-- 102: run_script blocks, and the branch discriminator an `if` needs
-- (Process Designer, Plan slice 4 / Requirements AC-55, AC-56).
--
-- 098 gave an `if` one scope. Slice 4 needs two: THEN and ELSE. `branch` is
-- NULL for every row that is not a child of an `if`; existing children of an
-- `if` are THEN, so no row is dropped or orphaned by the upgrade.
ALTER TABLE base_task_definition_child
	ADD COLUMN IF NOT EXISTS branch TEXT NULL;

UPDATE base_task_definition_child c
SET branch = 'then'
FROM base_task_definition_child p
WHERE c.parent_block_id = p._id
  AND p.block_kind = 'control'
  AND p.block_type = 'if'
  AND c.branch IS NULL;

ALTER TABLE base_task_definition_child
	DROP CONSTRAINT IF EXISTS chk_base_task_definition_child_branch;
ALTER TABLE base_task_definition_child
	ADD CONSTRAINT chk_base_task_definition_child_branch CHECK (
		branch IS NULL OR branch IN ('then', 'else')
	);

-- AC-56: run_script is a control shape, and the later slices' run_task and
-- retry shapes are admitted by the same check. A non-control block still has
-- no block_type.
ALTER TABLE base_task_definition_child
	DROP CONSTRAINT IF EXISTS chk_base_task_definition_child_block_type;
ALTER TABLE base_task_definition_child
	ADD CONSTRAINT chk_base_task_definition_child_block_type CHECK (
		(block_kind = 'control' AND block_type IN
			('if', 'for_each', 'parallel', 'wait', 'end', 'run_script', 'run_task', 'retry'))
		OR (block_kind <> 'control' AND block_type IS NULL)
	);
