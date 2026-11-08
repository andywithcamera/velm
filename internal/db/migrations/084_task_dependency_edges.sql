-- Dependency edges between container children (issue #209).
--
-- base_task_definition_child orders children with sort_order and nothing
-- else; order cannot express fan-in, parallel branches, or rejection loops.
-- depends_on references child DEFINITION ids (declarative on the template,
-- evaluated per instance); depends_on_reason restricts which closure reason
-- satisfies the edge ('' = completed/cancelled — success).
--
-- Also (both listed as #209 depends-on):
--  - widened chk_base_task_closure_reason with 'rejected' and 'failed'
--    (rejection loops re-spawn implementation).
--  - 077 required an assignee before 'pending'; engine-spawned children are
--    pending by design (#206) and route later, so pending no longer requires
--    one. in_progress / ready_to_close still do.

ALTER TABLE base_task_definition_child
	ADD COLUMN IF NOT EXISTS depends_on UUID[] NOT NULL DEFAULT '{}',
	ADD COLUMN IF NOT EXISTS depends_on_reason TEXT NOT NULL DEFAULT '';

CREATE INDEX IF NOT EXISTS idx_base_task_definition_child_depends
	ON base_task_definition_child USING gin (depends_on);

ALTER TABLE base_task
	DROP CONSTRAINT IF EXISTS chk_base_task_closure_reason;
ALTER TABLE base_task
	ADD CONSTRAINT chk_base_task_closure_reason CHECK (
		(state <> 'closed' AND closure_reason IS NULL)
		OR (state = 'closed' AND closure_reason IN
			('completed', 'cancelled', 'rejected', 'failed'))
	);

-- Relax the 077 assignment gate: pending is a waiting state, assignment
-- happens at promotion to ready (group routing) or claim.
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

	IF next_state IN ('in_progress', 'ready_to_close') AND (NEW.assignment_group_id IS NULL OR NEW.assigned_user_id IS NULL) THEN
		RAISE EXCEPTION 'task must be assigned before leaving new';
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

-- Cycle rejection at definition save: a container's child edges must form a
-- DAG. Fires on the template row, so bad wiring cannot exist for any
-- instance to trip over at runtime.
CREATE OR REPLACE FUNCTION base_task_child_edge_acyclic()
RETURNS trigger
LANGUAGE plpgsql
AS $$
DECLARE
	container UUID;
BEGIN
	container := NEW.container_definition_id;

	-- Walk forward from the touched child through depends_on edges inside
	-- this container; returning to the touched child means a cycle.
	IF EXISTS (
		WITH RECURSIVE walk AS (
			SELECT unnest(NEW.depends_on) AS node
			UNION
			SELECT unnest(c.depends_on)
			FROM base_task_definition_child c
			JOIN walk w ON c.child_definition_id = w.node
			WHERE c.container_definition_id = container
		)
		SELECT 1 FROM walk WHERE node = NEW.child_definition_id
	) THEN
		RAISE EXCEPTION 'dependency cycle in container % via child %', container, NEW.child_definition_id;
	END IF;

	RETURN NEW;
END;
$$;

DROP TRIGGER IF EXISTS trg_base_task_child_edge_acyclic ON base_task_definition_child;
CREATE TRIGGER trg_base_task_child_edge_acyclic
BEFORE INSERT OR UPDATE OF depends_on ON base_task_definition_child
FOR EACH ROW
EXECUTE FUNCTION base_task_child_edge_acyclic();
