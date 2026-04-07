ALTER TABLE base_task
	DROP COLUMN IF EXISTS resolved_at;

CREATE OR REPLACE FUNCTION base_task_before_write()
RETURNS trigger
LANGUAGE plpgsql
AS $$
DECLARE
	next_state TEXT;
	next_state_closed BOOLEAN := FALSE;
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
		WHEN 'pending', 'ready', 'in_progress', 'blocked' THEN
			next_state_closed := FALSE;
		WHEN 'done', 'cancelled' THEN
			next_state_closed := TRUE;
		ELSE
			RAISE EXCEPTION 'invalid task state %', next_state;
	END CASE;

	IF TG_OP = 'INSERT' THEN
		NEW.state_changed_at := NOW();
		NEW.started_at := NULL;
		NEW.closed_at := NULL;
		IF next_state IN ('in_progress', 'blocked', 'done', 'cancelled') THEN
			NEW.started_at := NEW.state_changed_at;
		END IF;
		IF next_state_closed THEN
			NEW.closed_at := NEW.state_changed_at;
		END IF;
		RETURN NEW;
	END IF;

	IF NEW.state IS DISTINCT FROM OLD.state THEN
		NEW.state_changed_at := NOW();
		NEW.started_at := OLD.started_at;
		IF NEW.started_at IS NULL AND next_state IN ('in_progress', 'blocked', 'done', 'cancelled') THEN
			NEW.started_at := NEW.state_changed_at;
		END IF;
		NEW.closed_at := NULL;
		IF next_state_closed THEN
			NEW.closed_at := NEW.state_changed_at;
		END IF;
	ELSE
		NEW.state_changed_at := OLD.state_changed_at;
		NEW.started_at := OLD.started_at;
		NEW.closed_at := OLD.closed_at;
	END IF;

	RETURN NEW;
END;
$$;

DROP TRIGGER IF EXISTS trg_base_task_before_write ON base_task;
CREATE TRIGGER trg_base_task_before_write
BEFORE INSERT OR UPDATE ON base_task
FOR EACH ROW
EXECUTE FUNCTION base_task_before_write();
