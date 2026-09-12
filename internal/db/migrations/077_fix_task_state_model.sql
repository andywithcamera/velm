-- Fix-forward for 076 (issue #112). 076 assumed base_task.state was a
-- CHECK-constrained enum; the real model (047) uses base_task_state rows
-- referenced by state_id, with a base_task_transition table. There is no
-- 'state' column, so 076's ALTER ... CHECK block was a no-op against a
-- non-existent column. This migration supplies the real implementation:
--
-- 1. 'skipped' as a first-class terminal task state (run_if false).
-- 2. Transitions ready -> skipped (the engine skips from the waiting
--    state), in_progress -> skipped (mid-flight bail-out), and
--    blocked -> skipped. Terminal: publishes nothing, no reopen edge.
-- 3. No DROP of 076's bogus CHECK (it never bound: the column does not
--    exist, so the ALTER would have failed loudly if it ran — kept here
--    as documentation of the mistake).

DO $$
DECLARE
	skipped_id UUID;
	ready_id UUID;
	inprog_id UUID;
	blocked_id UUID;
BEGIN
	-- Terminal 'skipped' state, board lane after cancelled.
	INSERT INTO base_task_state (code, label, description, category, board_lane, sort_order, is_initial, is_terminal, is_closed)
	SELECT 'skipped', 'Skipped', 'Task did not run: run_if condition evaluated false.', 'cancelled', 'cancelled', 60, FALSE, TRUE, TRUE
	WHERE NOT EXISTS (SELECT 1 FROM base_task_state WHERE code = 'skipped');

	SELECT _id INTO skipped_id FROM base_task_state WHERE code = 'skipped';
	SELECT _id INTO ready_id FROM base_task_state WHERE code = 'ready';
	SELECT _id INTO inprog_id FROM base_task_state WHERE code = 'in_progress';
	SELECT _id INTO blocked_id FROM base_task_state WHERE code = 'blocked';

	INSERT INTO base_task_transition (name, description, from_state_id, to_state_id, require_assignment)
	SELECT 'ready_to_skipped', 'run_if condition false: task does not run.', ready_id, skipped_id, FALSE
	WHERE NOT EXISTS (SELECT 1 FROM base_task_transition WHERE from_state_id = ready_id AND to_state_id = skipped_id);

	INSERT INTO base_task_transition (name, description, from_state_id, to_state_id, require_assignment)
	SELECT 'in_progress_to_skipped', 'Bail out mid-flight: condition or script aborted the task.', inprog_id, skipped_id, FALSE
	WHERE NOT EXISTS (SELECT 1 FROM base_task_transition WHERE from_state_id = inprog_id AND to_state_id = skipped_id);

	INSERT INTO base_task_transition (name, description, from_state_id, to_state_id, require_assignment)
	SELECT 'blocked_to_skipped', 'Blocked work abandoned as skipped.', blocked_id, skipped_id, FALSE
	WHERE NOT EXISTS (SELECT 1 FROM base_task_transition WHERE from_state_id = blocked_id AND to_state_id = skipped_id);
END $$;
