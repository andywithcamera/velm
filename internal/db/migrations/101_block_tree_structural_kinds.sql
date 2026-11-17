-- 101: every structural block kind may call no definition (Process Designer,
-- Plan T6 / Requirements AC-84).
--
-- 099 allowed child_definition_id to be NULL for trigger, control and stage.
-- A gate is also structure: it has no child definition of its own, it names
-- its exits. AC-84 requires a gate row to persist, so the same allowance is
-- extended to action and gate. The block-kind check in 098 already lists all
-- six kinds; this only aligns the NULL rule with it.

ALTER TABLE base_task_definition_child
	DROP CONSTRAINT IF EXISTS chk_base_task_definition_child_child_definition;
ALTER TABLE base_task_definition_child
	ADD CONSTRAINT chk_base_task_definition_child_child_definition CHECK (
		child_definition_id IS NOT NULL
		OR block_kind IN ('trigger', 'control', 'stage', 'action', 'gate')
	);
