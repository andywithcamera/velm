-- Fix-forward for 076/077 (issue #112). The task engine (#114) and this
-- migration's first draft assumed the 047 model (base_task_state rows
-- referenced by state_id, with a base_task_transition table). That model
-- was dropped by 048 and simplified by 054/055 into a text `state`
-- column with a CHECK constraint and a before-write trigger.
--
-- This migration reconciles the two on the real (055) model:
--
-- 1. 'ready' — spawn state for engine-created tasks; the design doc (rev 3)
--    container spawn here. (055's CHECK already folds 'ready' into
--    'new' during migration, but the live CHECK does not accept it.)
-- 2. 'skipped' — terminal state for tasks whose run_if evaluated false.
--    Publishes nothing, no reopen edge, mirrors 'cancelled' semantics.
--
-- Both flow through the existing trigger: 'ready' must satisfy the
-- same assignment rule as any non-new state, 'skipped' is terminal.
ALTER TABLE base_task
	DROP CONSTRAINT IF EXISTS chk_base_task_state;

ALTER TABLE base_task
	ADD CONSTRAINT chk_base_task_state CHECK (
		state IN ('new', 'pending', 'ready', 'in_progress', 'ready_to_close', 'closed', 'skipped')
	);

CREATE OR REPLACE FUNCTION base_task_before_write()
RETURNS trigger
LANGUAGE plpgsql
AS $$
DECLARE
	next_state TEXT;
	next_closure_reason TEXT;
BEGIN
	IF NEW.assigned_user_id IS NOT NULL THEN
		IF NEW.assignment_group_id IS NULL THEN
			RAISE EXCEPTION 'assigned_user_id requires assignment_group_id';
		END IF;
		IF NOT EXISTS (
			SELECT 1
			FROM _group_membership gm
			WHERE gm.group_id = NEW.assignment_group_id
			  AND gm.user_id = NEW.assigned_user_id::text
		) THEN
			RAISE EXCEPTION 'assigned_user_id must be a member of assignment_group_id';
		END IF;
	END IF;

	next_state := COALESCE(NULLIF(BTRIM(NEW.state), ''), 'new');
	NEW.state := next_state;

	CASE next_state
		WHEN 'new', 'pending', 'ready', 'in_progress', 'ready_to_close', 'closed', 'skipped' THEN
			NULL;
		ELSE
			RAISE EXCEPTION 'invalid task state %', next_state;
	END CASE;

	IF next_state IN ('pending', 'in_progress', 'ready_to_close') AND (NEW.assignment_group_id IS NULL OR NEW.assigned_user_id IS NULL) THEN
		RAISE EXCEPTION 'task must be assigned before leaving new';
	END IF;

	next_closure_reason := NULLIF(BTRIM(COALESCE(NEW.closure_reason, '')), '');
	IF next_state = 'closed' THEN
		next_closure_reason := COALESCE(next_closure_reason, 'completed');
		CASE next_closure_reason
			WHEN 'completed', 'cancelled' THEN
				NULL;
			ELSE
				RAISE EXCEPTION 'invalid task closure_reason %', next_closure_reason;
		END CASE;
		NEW.closure_reason := next_closure_reason;
	ELSE
		NEW.closure_reason := NULL;
	END IF;

	IF TG_OP = 'INSERT' THEN
		NEW.state_changed_at := NOW();
		IF next_state IN ('in_progress', 'ready_to_close') AND NEW.started_at IS NULL THEN
			NEW.started_at := NEW.state_changed_at;
		END IF;
		IF next_state IN ('closed', 'skipped') THEN
			NEW.closed_at := COALESCE(NEW.closed_at, NEW.state_changed_at);
		ELSE
			NEW.closed_at := NULL;
		END IF;
		RETURN NEW;
	END IF;

	IF NEW.state IS DISTINCT FROM OLD.state THEN
		NEW.state_changed_at := NOW();
		NEW.started_at := OLD.started_at;
		IF NEW.started_at IS NULL AND next_state IN ('in_progress', 'ready_to_close') THEN
			NEW.started_at := NEW.state_changed_at;
		END IF;
		IF next_state IN ('closed', 'skipped') THEN
			NEW.closed_at := COALESCE(OLD.closed_at, NEW.state_changed_at);
		ELSE
			NEW.closed_at := NULL;
		END IF;
	ELSE
		NEW.state_changed_at := OLD.state_changed_at;
		NEW.started_at := OLD.started_at;
		IF next_state IN ('closed', 'skipped') THEN
			NEW.closed_at := COALESCE(OLD.closed_at, NEW.closed_at, OLD.state_changed_at);
		ELSE
			NEW.closed_at := NULL;
		END IF;
	END IF;

	RETURN NEW;
END;
$$;

DROP TRIGGER IF EXISTS trg_base_task_before_write ON base_task;

CREATE TRIGGER trg_base_task_before_write
BEFORE INSERT OR UPDATE ON base_task
FOR EACH ROW
EXECUTE FUNCTION base_task_before_write();
