-- Simplify the task state model to four user-visible states (2026-11-09):
-- pending -> ready -> in_progress -> closed. 'skipped' stays as an
-- engine-internal terminal (run_if evaluated false) but is not offered in
-- the UI. Legacy values map forward: 'new' -> 'pending',
-- 'ready_to_close' -> 'ready'. 'skipped' rows keep their state (terminal,
-- semantically distinct from a completed close).

UPDATE base_task SET state = 'pending' WHERE state = 'new';
UPDATE base_task SET state = 'ready'  WHERE state = 'ready_to_close';

ALTER TABLE base_task ALTER COLUMN state SET DEFAULT 'pending';

ALTER TABLE base_task
	DROP CONSTRAINT IF EXISTS chk_base_task_state;
ALTER TABLE base_task
	ADD CONSTRAINT chk_base_task_state CHECK (
		state IN ('pending', 'ready', 'in_progress', 'closed', 'skipped')
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

	next_state := COALESCE(NULLIF(BTRIM(NEW.state), ''), 'pending');
	NEW.state := next_state;

	CASE next_state
		WHEN 'pending', 'ready', 'in_progress', 'closed', 'skipped' THEN
			NULL;
		ELSE
			RAISE EXCEPTION 'invalid task state %', next_state;
	END CASE;

	-- 084 relaxation stands: pending is a waiting state; assignment happens
	-- at promotion to ready or claim. Only in_progress requires an assignee.
	IF next_state = 'in_progress' AND (NEW.assignment_group_id IS NULL OR NEW.assigned_user_id IS NULL) THEN
		RAISE EXCEPTION 'task must be assigned before starting work';
	END IF;

	next_closure_reason := NULLIF(BTRIM(COALESCE(NEW.closure_reason, '')), '');
	IF next_state = 'closed' THEN
		next_closure_reason := COALESCE(next_closure_reason, 'completed');
		CASE next_closure_reason
			WHEN 'completed', 'cancelled', 'rejected', 'failed' THEN
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
		IF next_state = 'in_progress' AND NEW.started_at IS NULL THEN
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
		IF NEW.started_at IS NULL AND next_state = 'in_progress' THEN
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
			NEW.closed_at := COALESCE(NEW.closed_at, OLD.closed_at, OLD.state_changed_at);
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
