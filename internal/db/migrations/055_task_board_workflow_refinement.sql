ALTER TABLE base_task
	DROP CONSTRAINT IF EXISTS chk_base_task_state;

ALTER TABLE base_task
	DROP CONSTRAINT IF EXISTS chk_base_task_closure_reason;

DROP TRIGGER IF EXISTS trg_base_task_before_write ON base_task;

UPDATE base_task
SET
	state = CASE COALESCE(NULLIF(BTRIM(state), ''), 'new')
		WHEN 'ready_to_close' THEN 'ready_to_close'
		WHEN 'closed' THEN 'closed'
		WHEN 'done' THEN 'closed'
		WHEN 'cancelled' THEN 'closed'
		WHEN 'ready' THEN 'new'
		WHEN 'triage' THEN 'new'
		WHEN 'blocked' THEN 'pending'
		WHEN 'pending' THEN 'pending'
		WHEN 'in_progress' THEN 'in_progress'
		WHEN 'new' THEN 'new'
		ELSE 'new'
	END,
	closure_reason = CASE COALESCE(NULLIF(BTRIM(state), ''), 'new')
		WHEN 'cancelled' THEN 'cancelled'
		WHEN 'done' THEN 'completed'
		WHEN 'closed' THEN COALESCE(NULLIF(BTRIM(closure_reason), ''), 'completed')
		ELSE NULL
	END,
	closed_at = CASE COALESCE(NULLIF(BTRIM(state), ''), 'new')
		WHEN 'done' THEN COALESCE(closed_at, state_changed_at, _updated_at, _created_at)
		WHEN 'cancelled' THEN COALESCE(closed_at, state_changed_at, _updated_at, _created_at)
		WHEN 'closed' THEN COALESCE(closed_at, state_changed_at, _updated_at, _created_at)
		ELSE NULL
	END;

ALTER TABLE base_task
	ALTER COLUMN state SET DEFAULT 'new';

ALTER TABLE base_task
	ALTER COLUMN state SET NOT NULL;

ALTER TABLE base_task
	ADD CONSTRAINT chk_base_task_state CHECK (
		state IN ('new', 'pending', 'in_progress', 'ready_to_close', 'closed')
	);

ALTER TABLE base_task
	ADD CONSTRAINT chk_base_task_closure_reason CHECK (
		(state <> 'closed' AND closure_reason IS NULL)
		OR (state = 'closed' AND closure_reason IN ('completed', 'cancelled'))
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
		WHEN 'new', 'pending', 'in_progress', 'ready_to_close', 'closed' THEN
			NULL;
		ELSE
			RAISE EXCEPTION 'invalid task state %', next_state;
	END CASE;

	IF next_state <> 'new' AND (NEW.assignment_group_id IS NULL OR NEW.assigned_user_id IS NULL) THEN
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
		IF next_state = 'closed' THEN
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
		IF next_state = 'closed' THEN
			NEW.closed_at := COALESCE(OLD.closed_at, NEW.state_changed_at);
		ELSE
			NEW.closed_at := NULL;
		END IF;
	ELSE
		NEW.state_changed_at := OLD.state_changed_at;
		NEW.started_at := OLD.started_at;
		IF next_state = 'closed' THEN
			NEW.closed_at := COALESCE(OLD.closed_at, NEW.closed_at, OLD.state_changed_at);
		ELSE
			NEW.closed_at := NULL;
		END IF;
	END IF;

	RETURN NEW;
END;
$$;

CREATE TRIGGER trg_base_task_before_write
BEFORE INSERT OR UPDATE ON base_task
FOR EACH ROW
EXECUTE FUNCTION base_task_before_write();
