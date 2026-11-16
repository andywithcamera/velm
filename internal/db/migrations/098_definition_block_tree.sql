-- 098: a definition is a tree, not a flat list (Process Designer, Plan T3).
--
-- base_task_definition_child gains nesting (parent_block_id), sibling order
-- (position), a block kind/type, a JSONB config, per-block assignment, and
-- edges that name a block row rather than a definition id. Existing rows are
-- backfilled in place: block_kind='task', block_type=NULL,
-- position=sort_order, parent_block_id=NULL, config='{}'. Nothing is
-- rewritten, so a definition authored before 098 keeps spawning exactly as
-- it did -- proved by TestPreMigrationDefinitionSpawnsIdentically.
--
-- Also here because the tree needs them:
--  - uq_base_task_definition_child goes: a container may legitimately hold
--    the same child definition twice (two reboot_server blocks), and
--    depends_on/child_definition_id could no longer identify one row.
--  - the acyclicity trigger checks block edges (depends_on_block_id) as well
--    as the legacy definition edges (depends_on), so the v1 editor and the
--    tree cannot disagree about what a cycle is.
--  - base_task.work_type widens to TASK | CONTROL. Design Vision 5
--    materialises control blocks as base_task rows; 056's check constraint
--    still allows only 'TASK' at this point (057's trigger stamping was
--    already removed by 077), so the constraint alone made CONTROL
--    impossible and is the one this migration replaces.

-- ---------------------------------------------------------------------------
-- 1. The block tree.
-- ---------------------------------------------------------------------------
ALTER TABLE base_task_definition_child
	ADD COLUMN IF NOT EXISTS parent_block_id UUID NULL REFERENCES base_task_definition_child(_id),
	ADD COLUMN IF NOT EXISTS position INT NOT NULL DEFAULT 0,
	ADD COLUMN IF NOT EXISTS block_kind TEXT NOT NULL DEFAULT 'task',
	ADD COLUMN IF NOT EXISTS block_type TEXT NULL,
	ADD COLUMN IF NOT EXISTS config JSONB NOT NULL DEFAULT '{}'::jsonb,
	ADD COLUMN IF NOT EXISTS assignment_group_id UUID NULL REFERENCES _group(_id) ON DELETE SET NULL,
	ADD COLUMN IF NOT EXISTS assignment_strategy TEXT NULL,
	ADD COLUMN IF NOT EXISTS assigned_user_id UUID NULL REFERENCES _user(_id) ON DELETE SET NULL,
	ADD COLUMN IF NOT EXISTS depends_on_block_id UUID[] NOT NULL DEFAULT '{}';

ALTER TABLE base_task_definition_child
	DROP CONSTRAINT IF EXISTS chk_base_task_definition_child_block_kind;
ALTER TABLE base_task_definition_child
	ADD CONSTRAINT chk_base_task_definition_child_block_kind CHECK (
		block_kind IN ('trigger', 'task', 'action', 'gate', 'control', 'stage')
	);

-- block_type is a control-block property and nothing else: NULL everywhere
-- else, and one of the five control shapes when block_kind='control'.
ALTER TABLE base_task_definition_child
	DROP CONSTRAINT IF EXISTS chk_base_task_definition_child_block_type;
ALTER TABLE base_task_definition_child
	ADD CONSTRAINT chk_base_task_definition_child_block_type CHECK (
		(block_kind = 'control' AND block_type IN ('if', 'for_each', 'parallel', 'wait', 'end'))
		OR (block_kind <> 'control' AND block_type IS NULL)
	);

ALTER TABLE base_task_definition_child
	DROP CONSTRAINT IF EXISTS chk_base_task_definition_child_assignment_strategy;
ALTER TABLE base_task_definition_child
	ADD CONSTRAINT chk_base_task_definition_child_assignment_strategy CHECK (
		assignment_strategy IS NULL OR assignment_strategy IN
			('unassigned', 'named_user', 'requester', 'from_input', 'random_member', 'round_robin')
	);

-- ---------------------------------------------------------------------------
-- 2. Backfill. Existing rows are top-level task blocks, in their real order.
-- ---------------------------------------------------------------------------
UPDATE base_task_definition_child
SET block_kind = 'task',
	block_type = NULL,
	position = sort_order
WHERE parent_block_id IS NULL;

-- Edges used to name child definition ids. In pre-098 data the unique
-- constraint meant one row per child definition per container, so the join
-- is unambiguous. Order the ids by the sibling's own order so a later reader
-- sees the same sequence the flat editor showed.
UPDATE base_task_definition_child c
SET depends_on_block_id = COALESCE((
	SELECT array_agg(b._id ORDER BY b.sort_order, b._created_at)
	FROM base_task_definition_child b
	WHERE b.container_definition_id = c.container_definition_id
	  AND b.child_definition_id = ANY (c.depends_on)
), '{}')
WHERE cardinality(c.depends_on) > 0;

-- ---------------------------------------------------------------------------
-- 3. Edge identity is a row, not a definition.
-- ---------------------------------------------------------------------------
ALTER TABLE base_task_definition_child
	DROP CONSTRAINT IF EXISTS uq_base_task_definition_child;

-- 084's GIN index (`idx_base_task_definition_child_depends`) is named for
-- the concept of a dependency edge, not for one column. The edge of record is
-- now depends_on_block_id, so that index name moves with the meaning; the
-- legacy definition-id column keeps its own GIN index under an explicit
-- `_depends_on` name so the rows view stays indexed too.
DROP INDEX IF EXISTS idx_base_task_definition_child_depends;
CREATE INDEX IF NOT EXISTS idx_base_task_definition_child_depends_on
	ON base_task_definition_child USING gin (depends_on);
CREATE INDEX IF NOT EXISTS idx_base_task_definition_child_depends
	ON base_task_definition_child USING gin (depends_on_block_id);

-- ---------------------------------------------------------------------------
-- 4. Acyclicity over block edges, and over the legacy edges while they exist.
-- ---------------------------------------------------------------------------
CREATE OR REPLACE FUNCTION base_task_child_edge_acyclic()
RETURNS trigger
LANGUAGE plpgsql
AS $$
DECLARE
	container UUID;
BEGIN
	container := NEW.container_definition_id;

	-- Block edges: walk forward from the blocks this row waits for. Full row
	-- identity means two blocks may point at the same definition and still be
	-- told apart.
	IF cardinality(NEW.depends_on_block_id) > 0 AND EXISTS (
		WITH RECURSIVE walk AS (
			SELECT unnest(NEW.depends_on_block_id) AS node
			UNION
			SELECT unnest(c.depends_on_block_id)
			FROM base_task_definition_child c
			JOIN walk w ON c._id = w.node
			WHERE c.container_definition_id = container
		)
		SELECT 1 FROM walk WHERE node = NEW._id
	) THEN
		RAISE EXCEPTION 'dependency cycle in container % via block %', container, NEW._id;
	END IF;

	-- Legacy edges (definition ids): the v1 rows view writes these, and it
	-- must keep getting the same refusal it got from 084.
	IF cardinality(NEW.depends_on) > 0 AND EXISTS (
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
BEFORE INSERT OR UPDATE OF depends_on, depends_on_block_id ON base_task_definition_child
FOR EACH ROW
EXECUTE FUNCTION base_task_child_edge_acyclic();

-- ---------------------------------------------------------------------------
-- 5. Assignment strategy on the definition. The group and the named user stay
-- on default_group_id / default_assignee_id (090); this is the strategy that
-- reads them, not a second group.
-- ---------------------------------------------------------------------------
ALTER TABLE base_task_definition
	ADD COLUMN IF NOT EXISTS assignment_strategy TEXT NULL;

ALTER TABLE base_task_definition
	DROP CONSTRAINT IF EXISTS chk_base_task_definition_assignment_strategy;
ALTER TABLE base_task_definition
	ADD CONSTRAINT chk_base_task_definition_assignment_strategy CHECK (
		assignment_strategy IS NULL OR assignment_strategy IN
			('unassigned', 'named_user', 'requester', 'from_input', 'random_member', 'round_robin')
	);

-- ---------------------------------------------------------------------------
-- 6. base_task.work_type accepts CONTROL, and the trigger stops stamping it.
-- ---------------------------------------------------------------------------
ALTER TABLE base_task
	DROP CONSTRAINT IF EXISTS chk_base_task_type;
ALTER TABLE base_task
	ADD CONSTRAINT chk_base_task_type CHECK (BTRIM(work_type) IN ('TASK', 'CONTROL'));

-- Restated verbatim from 085. This is deliberate: the trigger is the one
-- place assignment, state and closure invariants live, and re-creating it
-- beside 085's exact body means the widening above cannot quietly drop one.
-- (085's function no longer stamps work_type at all; 057's `NEW.work_type :=
-- 'TASK'` was removed by 077, so only the CHECK had to change.)
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
