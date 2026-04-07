ALTER TABLE base_task
	ADD COLUMN IF NOT EXISTS state TEXT;

UPDATE base_task AS task
SET state = CASE COALESCE(state_item.code, '')
	WHEN 'new' THEN 'pending'
	WHEN 'triage' THEN 'pending'
	WHEN 'ready' THEN 'ready'
	WHEN 'in_progress' THEN 'in_progress'
	WHEN 'blocked' THEN 'blocked'
	WHEN 'done' THEN 'done'
	WHEN 'cancelled' THEN 'cancelled'
	ELSE 'pending'
END
FROM base_task_state AS state_item
WHERE task.state_id = state_item._id;

UPDATE base_task
SET state = 'pending'
WHERE state IS NULL
   OR BTRIM(state) = '';

ALTER TABLE base_task
	ALTER COLUMN state SET DEFAULT 'pending';

ALTER TABLE base_task
	ALTER COLUMN state SET NOT NULL;

ALTER TABLE base_task
	DROP CONSTRAINT IF EXISTS chk_base_task_state;

ALTER TABLE base_task
	ADD CONSTRAINT chk_base_task_state CHECK (
		state IN ('pending', 'ready', 'in_progress', 'blocked', 'done', 'cancelled')
	);

CREATE OR REPLACE FUNCTION base_task_before_write()
RETURNS trigger
LANGUAGE plpgsql
AS $$
DECLARE
	next_state TEXT;
	next_state_closed BOOLEAN := FALSE;
	next_state_terminal BOOLEAN := FALSE;
BEGIN
	IF COALESCE(BTRIM(NEW.number), '') = '' THEN
		NEW.number := 'WO-' || LPAD(nextval('base_task_number_seq')::text, 6, '0');
	END IF;

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
		NEW.state_changed_at := COALESCE(NEW.state_changed_at, NOW());
		IF NEW.started_at IS NULL AND next_state IN ('in_progress', 'blocked', 'done', 'cancelled') THEN
			NEW.started_at := NEW.state_changed_at;
		END IF;
		IF next_state_terminal AND NEW.resolved_at IS NULL THEN
			NEW.resolved_at := NEW.state_changed_at;
		END IF;
		IF next_state_closed AND NEW.closed_at IS NULL THEN
			NEW.closed_at := NEW.state_changed_at;
		END IF;
		RETURN NEW;
	END IF;

	IF NEW.state IS DISTINCT FROM OLD.state THEN
		NEW.state_changed_at := NOW();
		IF NEW.started_at IS NULL AND next_state IN ('in_progress', 'blocked', 'done', 'cancelled') THEN
			NEW.started_at := NEW.state_changed_at;
		END IF;
		IF next_state_terminal THEN
			IF NEW.resolved_at IS NULL THEN
				NEW.resolved_at := NEW.state_changed_at;
			END IF;
		ELSE
			NEW.resolved_at := NULL;
		END IF;
		IF next_state_closed THEN
			IF NEW.closed_at IS NULL THEN
				NEW.closed_at := NEW.state_changed_at;
			END IF;
		ELSE
			NEW.closed_at := NULL;
		END IF;
	END IF;

	RETURN NEW;
END;
$$;

DROP INDEX IF EXISTS idx_base_task_state;

ALTER TABLE base_task
	DROP COLUMN IF EXISTS state_id;

DROP TABLE IF EXISTS base_task_transition;
DROP TABLE IF EXISTS base_task_state;

CREATE INDEX IF NOT EXISTS idx_base_task_state
	ON base_task(state, priority, board_rank);
