ALTER TABLE base_task
	DROP CONSTRAINT IF EXISTS chk_base_task_priority;

UPDATE base_task
SET priority = CASE priority
	WHEN 'p1' THEN 'very_high'
	WHEN 'p2' THEN 'high'
	WHEN 'p3' THEN 'medium'
	WHEN 'p4' THEN 'low'
	WHEN 'p5' THEN 'very_low'
	ELSE priority
END
WHERE priority IN ('p1', 'p2', 'p3', 'p4', 'p5');

ALTER TABLE base_task
	ALTER COLUMN priority SET DEFAULT 'medium';

ALTER TABLE base_task
	ADD CONSTRAINT chk_base_task_priority CHECK (priority IN ('very_low', 'low', 'medium', 'high', 'very_high'));

DROP INDEX IF EXISTS idx_base_task_state;
CREATE INDEX IF NOT EXISTS idx_base_task_state
	ON base_task(state, priority);

ALTER TABLE base_task
	DROP COLUMN IF EXISTS board_rank;

ALTER TABLE base_task
	DROP COLUMN IF EXISTS resolved_at;

CREATE OR REPLACE FUNCTION base_task_before_write()
RETURNS trigger
LANGUAGE plpgsql
AS $$
DECLARE
	next_state TEXT;
	next_state_closed BOOLEAN := FALSE;
	next_state_terminal BOOLEAN := FALSE;
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
			next_state_terminal := FALSE;
			next_state_closed := FALSE;
		WHEN 'done', 'cancelled' THEN
			next_state_terminal := TRUE;
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
