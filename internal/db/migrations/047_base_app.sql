CREATE EXTENSION IF NOT EXISTS pgcrypto;

CREATE SEQUENCE IF NOT EXISTS base_task_number_seq START WITH 1 INCREMENT BY 1;
CREATE SEQUENCE IF NOT EXISTS base_entity_number_seq START WITH 1 INCREMENT BY 1;

CREATE TABLE IF NOT EXISTS base_task_state (
	_id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
	_created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
	_updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
	_update_count BIGINT NOT NULL DEFAULT 0,
	_deleted_at TIMESTAMPTZ,
	_created_by UUID,
	_updated_by UUID,
	_deleted_by UUID,
	code TEXT NOT NULL UNIQUE,
	label TEXT NOT NULL,
	description TEXT,
	category TEXT NOT NULL DEFAULT 'queue',
	board_lane TEXT NOT NULL DEFAULT '',
	sort_order INTEGER NOT NULL DEFAULT 100,
	is_initial BOOLEAN NOT NULL DEFAULT FALSE,
	is_terminal BOOLEAN NOT NULL DEFAULT FALSE,
	is_closed BOOLEAN NOT NULL DEFAULT FALSE,
	CONSTRAINT chk_base_task_state_category CHECK (category IN ('queue', 'active', 'blocked', 'done', 'cancelled'))
);

CREATE TABLE IF NOT EXISTS base_entity (
	_id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
	_created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
	_updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
	_update_count BIGINT NOT NULL DEFAULT 0,
	_deleted_at TIMESTAMPTZ,
	_created_by UUID,
	_updated_by UUID,
	_deleted_by UUID,
	number TEXT NOT NULL,
	name TEXT NOT NULL,
	description TEXT,
	entity_type TEXT NOT NULL DEFAULT 'item',
	lifecycle_state TEXT NOT NULL DEFAULT 'active',
	operational_status TEXT NOT NULL DEFAULT 'operational',
	criticality TEXT NOT NULL DEFAULT 'p3',
	source_system TEXT,
	source_record_id TEXT,
	external_ref TEXT,
	asset_tag TEXT,
	serial_number TEXT,
	owner_entity_id UUID REFERENCES base_entity(_id) ON DELETE SET NULL,
	responsible_group_id UUID REFERENCES _group(_id) ON DELETE SET NULL,
	responsible_user_id UUID REFERENCES _user(_id) ON DELETE SET NULL,
	CONSTRAINT chk_base_entity_number CHECK (BTRIM(number) <> ''),
	CONSTRAINT chk_base_entity_name CHECK (BTRIM(name) <> ''),
	CONSTRAINT chk_base_entity_type CHECK (BTRIM(entity_type) <> ''),
	CONSTRAINT chk_base_entity_lifecycle_state CHECK (lifecycle_state IN ('planned', 'active', 'maintenance', 'retired', 'disposed')),
	CONSTRAINT chk_base_entity_operational_status CHECK (operational_status IN ('operational', 'degraded', 'offline', 'standby', 'unknown')),
	CONSTRAINT chk_base_entity_criticality CHECK (criticality IN ('p1', 'p2', 'p3', 'p4', 'p5')),
	CONSTRAINT chk_base_entity_source_pair CHECK (
		(source_system IS NULL AND source_record_id IS NULL)
		OR (source_system IS NOT NULL AND source_record_id IS NOT NULL)
	)
);

CREATE TABLE IF NOT EXISTS base_task_transition (
	_id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
	_created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
	_updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
	_update_count BIGINT NOT NULL DEFAULT 0,
	_deleted_at TIMESTAMPTZ,
	_created_by UUID,
	_updated_by UUID,
	_deleted_by UUID,
	name TEXT NOT NULL,
	description TEXT,
	from_state_id UUID NOT NULL REFERENCES base_task_state(_id) ON DELETE CASCADE,
	to_state_id UUID NOT NULL REFERENCES base_task_state(_id) ON DELETE CASCADE,
	require_assignment BOOLEAN NOT NULL DEFAULT FALSE,
	CONSTRAINT chk_base_task_transition_name CHECK (BTRIM(name) <> ''),
	CONSTRAINT uq_base_task_transition UNIQUE (from_state_id, to_state_id)
);

CREATE TABLE IF NOT EXISTS base_task (
	_id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
	_created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
	_updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
	_update_count BIGINT NOT NULL DEFAULT 0,
	_deleted_at TIMESTAMPTZ,
	_created_by UUID,
	_updated_by UUID,
	_deleted_by UUID,
	number TEXT NOT NULL,
	title TEXT NOT NULL,
	description TEXT,
	work_type TEXT NOT NULL DEFAULT 'TASK',
	state_id UUID NOT NULL DEFAULT '5d674765-1626-49a5-9b9f-470dcdb3f701' REFERENCES base_task_state(_id),
	priority TEXT NOT NULL DEFAULT 'p3',
	entity_id UUID REFERENCES base_entity(_id) ON DELETE SET NULL,
	parent_task_id UUID REFERENCES base_task(_id) ON DELETE SET NULL,
	assignment_group_id UUID REFERENCES _group(_id) ON DELETE SET NULL,
	assigned_user_id UUID REFERENCES _user(_id) ON DELETE SET NULL,
	requested_by_user_id UUID REFERENCES _user(_id) ON DELETE SET NULL,
	state_changed_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
	planned_start_at TIMESTAMPTZ,
	due_at TIMESTAMPTZ,
	started_at TIMESTAMPTZ,
	resolved_at TIMESTAMPTZ,
	closed_at TIMESTAMPTZ,
	board_rank NUMERIC NOT NULL DEFAULT 1000,
	CONSTRAINT chk_base_task_number CHECK (BTRIM(number) <> ''),
	CONSTRAINT chk_base_task_title CHECK (BTRIM(title) <> ''),
	CONSTRAINT chk_base_task_type CHECK (BTRIM(work_type) <> ''),
	CONSTRAINT chk_base_task_priority CHECK (priority IN ('p1', 'p2', 'p3', 'p4', 'p5')),
	CONSTRAINT chk_base_task_assignment CHECK (assigned_user_id IS NULL OR assignment_group_id IS NOT NULL)
);

CREATE TABLE IF NOT EXISTS base_entity_relationship (
	_id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
	_created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
	_updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
	_update_count BIGINT NOT NULL DEFAULT 0,
	_deleted_at TIMESTAMPTZ,
	_created_by UUID,
	_updated_by UUID,
	_deleted_by UUID,
	source_entity_id UUID NOT NULL REFERENCES base_entity(_id) ON DELETE CASCADE,
	target_entity_id UUID NOT NULL REFERENCES base_entity(_id) ON DELETE CASCADE,
	relationship_type TEXT NOT NULL,
	status TEXT NOT NULL DEFAULT 'active',
	description TEXT,
	effective_from TIMESTAMPTZ,
	effective_to TIMESTAMPTZ,
	CONSTRAINT chk_base_entity_relationship_type CHECK (BTRIM(relationship_type) <> ''),
	CONSTRAINT chk_base_entity_relationship_status CHECK (status IN ('planned', 'active', 'inactive', 'retired')),
	CONSTRAINT chk_base_entity_relationship_distinct CHECK (source_entity_id <> target_entity_id)
);

SELECT _ensure_record_version_trigger('base_task_state');
SELECT _ensure_record_version_trigger('base_entity');
SELECT _ensure_record_version_trigger('base_task_transition');
SELECT _ensure_record_version_trigger('base_task');
SELECT _ensure_record_version_trigger('base_entity_relationship');

CREATE OR REPLACE FUNCTION base_assign_entity_number()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
	IF COALESCE(BTRIM(NEW.number), '') = '' THEN
		NEW.number := 'ENT-' || LPAD(nextval('base_entity_number_seq')::text, 6, '0');
	END IF;
	RETURN NEW;
END;
$$;

CREATE OR REPLACE FUNCTION base_task_before_write()
RETURNS trigger
LANGUAGE plpgsql
AS $$
DECLARE
	next_state_category TEXT;
	next_state_closed BOOLEAN;
	next_state_terminal BOOLEAN;
	requires_assignment BOOLEAN := FALSE;
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

	SELECT s.category, s.is_closed, s.is_terminal
	INTO next_state_category, next_state_closed, next_state_terminal
	FROM base_task_state s
	WHERE s._id = NEW.state_id
	  AND s._deleted_at IS NULL;
	IF NOT FOUND THEN
		RAISE EXCEPTION 'state_id references an inactive task state';
	END IF;

	IF TG_OP = 'INSERT' THEN
		NEW.state_changed_at := COALESCE(NEW.state_changed_at, NOW());
		IF NEW.started_at IS NULL AND next_state_category IN ('active', 'blocked', 'done', 'cancelled') THEN
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

	IF NEW.state_id IS DISTINCT FROM OLD.state_id THEN
		SELECT t.require_assignment
		INTO requires_assignment
		FROM base_task_transition t
		WHERE t.from_state_id = OLD.state_id
		  AND t.to_state_id = NEW.state_id
		  AND t._deleted_at IS NULL;
		IF NOT FOUND THEN
			RAISE EXCEPTION 'invalid task state transition';
		END IF;
		IF requires_assignment AND (NEW.assignment_group_id IS NULL OR NEW.assigned_user_id IS NULL) THEN
			RAISE EXCEPTION 'transition requires a group and user assignment';
		END IF;

		NEW.state_changed_at := NOW();
		IF NEW.started_at IS NULL AND next_state_category IN ('active', 'blocked', 'done', 'cancelled') THEN
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

DROP TRIGGER IF EXISTS trg_base_entity_before_write ON base_entity;
CREATE TRIGGER trg_base_entity_before_write
BEFORE INSERT OR UPDATE ON base_entity
FOR EACH ROW
EXECUTE FUNCTION base_assign_entity_number();

DROP TRIGGER IF EXISTS trg_base_task_before_write ON base_task;
CREATE TRIGGER trg_base_task_before_write
BEFORE INSERT OR UPDATE ON base_task
FOR EACH ROW
EXECUTE FUNCTION base_task_before_write();

CREATE UNIQUE INDEX IF NOT EXISTS idx_base_entity_number ON base_entity(number);
CREATE UNIQUE INDEX IF NOT EXISTS idx_base_task_number ON base_task(number);
CREATE UNIQUE INDEX IF NOT EXISTS idx_base_entity_source_record
	ON base_entity(source_system, source_record_id)
	WHERE source_system IS NOT NULL
	  AND source_record_id IS NOT NULL
	  AND _deleted_at IS NULL;
CREATE UNIQUE INDEX IF NOT EXISTS idx_base_entity_relationship_active
	ON base_entity_relationship(source_entity_id, target_entity_id, relationship_type)
	WHERE _deleted_at IS NULL;
CREATE UNIQUE INDEX IF NOT EXISTS idx_base_task_state_initial
	ON base_task_state(is_initial)
	WHERE is_initial = TRUE
	  AND _deleted_at IS NULL;

CREATE INDEX IF NOT EXISTS idx_base_entity_lookup ON base_entity(entity_type, lifecycle_state, operational_status);
CREATE INDEX IF NOT EXISTS idx_base_entity_owner ON base_entity(owner_entity_id);
CREATE INDEX IF NOT EXISTS idx_base_entity_group ON base_entity(responsible_group_id);
CREATE INDEX IF NOT EXISTS idx_base_entity_user ON base_entity(responsible_user_id);
CREATE INDEX IF NOT EXISTS idx_base_entity_asset_tag ON base_entity(asset_tag) WHERE asset_tag IS NOT NULL AND _deleted_at IS NULL;
CREATE INDEX IF NOT EXISTS idx_base_entity_serial_number ON base_entity(serial_number) WHERE serial_number IS NOT NULL AND _deleted_at IS NULL;

CREATE INDEX IF NOT EXISTS idx_base_task_state_lane ON base_task_state(category, sort_order);
CREATE INDEX IF NOT EXISTS idx_base_task_transition_from ON base_task_transition(from_state_id);
CREATE INDEX IF NOT EXISTS idx_base_task_transition_to ON base_task_transition(to_state_id);
CREATE INDEX IF NOT EXISTS idx_base_task_state ON base_task(state_id, priority, board_rank);
CREATE INDEX IF NOT EXISTS idx_base_task_assignment ON base_task(assignment_group_id, assigned_user_id);
CREATE INDEX IF NOT EXISTS idx_base_task_entity ON base_task(entity_id);
CREATE INDEX IF NOT EXISTS idx_base_task_due_at ON base_task(due_at);
CREATE INDEX IF NOT EXISTS idx_base_task_parent ON base_task(parent_task_id);

CREATE INDEX IF NOT EXISTS idx_base_entity_relationship_source ON base_entity_relationship(source_entity_id, status);
CREATE INDEX IF NOT EXISTS idx_base_entity_relationship_target ON base_entity_relationship(target_entity_id, status);

INSERT INTO base_task_state (
	_id,
	code,
	label,
	description,
	category,
	board_lane,
	sort_order,
	is_initial,
	is_terminal,
	is_closed
)
VALUES
	('5d674765-1626-49a5-9b9f-470dcdb3f701', 'new', 'New', 'Freshly created work waiting for intake.', 'queue', 'New', 10, TRUE, FALSE, FALSE),
	('5d674765-1626-49a5-9b9f-470dcdb3f702', 'triage', 'Triage', 'Needs initial review and shaping.', 'queue', 'Triage', 20, FALSE, FALSE, FALSE),
	('5d674765-1626-49a5-9b9f-470dcdb3f703', 'ready', 'Ready', 'Ready to pull into active work.', 'queue', 'Ready', 30, FALSE, FALSE, FALSE),
	('5d674765-1626-49a5-9b9f-470dcdb3f704', 'in_progress', 'In Progress', 'Actively being worked.', 'active', 'In Progress', 40, FALSE, FALSE, FALSE),
	('5d674765-1626-49a5-9b9f-470dcdb3f705', 'blocked', 'Blocked', 'Waiting on something before progress can continue.', 'blocked', 'Blocked', 50, FALSE, FALSE, FALSE),
	('5d674765-1626-49a5-9b9f-470dcdb3f706', 'done', 'Done', 'Work is complete and ready to close out.', 'done', 'Done', 60, FALSE, TRUE, TRUE),
	('5d674765-1626-49a5-9b9f-470dcdb3f707', 'cancelled', 'Cancelled', 'Work was cancelled before completion.', 'cancelled', 'Cancelled', 70, FALSE, TRUE, TRUE)
ON CONFLICT (code) DO UPDATE
SET label = EXCLUDED.label,
	description = EXCLUDED.description,
	category = EXCLUDED.category,
	board_lane = EXCLUDED.board_lane,
	sort_order = EXCLUDED.sort_order,
	is_initial = EXCLUDED.is_initial,
	is_terminal = EXCLUDED.is_terminal,
	is_closed = EXCLUDED.is_closed,
	_deleted_at = NULL,
	_updated_at = NOW();

INSERT INTO base_task_transition (
	name,
	description,
	from_state_id,
	to_state_id,
	require_assignment
)
SELECT
	item.name,
	item.description,
	from_state._id,
	to_state._id,
	item.require_assignment
FROM (
	VALUES
		('new_to_triage', 'Intake starts with triage.', 'new', 'triage', FALSE),
		('new_to_ready', 'Allow lightweight work to skip directly to ready.', 'new', 'ready', FALSE),
		('new_to_cancelled', 'Cancel duplicate or invalid work early.', 'new', 'cancelled', FALSE),
		('triage_to_ready', 'Shaped and ready for execution.', 'triage', 'ready', FALSE),
		('triage_to_in_progress', 'Start work directly from triage when needed.', 'triage', 'in_progress', TRUE),
		('triage_to_cancelled', 'Stop work during triage.', 'triage', 'cancelled', FALSE),
		('ready_to_in_progress', 'Begin execution from the ready queue.', 'ready', 'in_progress', TRUE),
		('ready_to_blocked', 'Mark the item blocked before active execution.', 'ready', 'blocked', TRUE),
		('ready_to_cancelled', 'Cancel before work starts.', 'ready', 'cancelled', FALSE),
		('in_progress_to_blocked', 'Something is preventing progress.', 'in_progress', 'blocked', TRUE),
		('in_progress_to_done', 'Execution completed successfully.', 'in_progress', 'done', TRUE),
		('in_progress_to_cancelled', 'Stop work while it is in flight.', 'in_progress', 'cancelled', FALSE),
		('blocked_to_in_progress', 'Blocker resolved and work can continue.', 'blocked', 'in_progress', TRUE),
		('blocked_to_done', 'Close directly from blocked after external completion.', 'blocked', 'done', TRUE),
		('blocked_to_cancelled', 'Abandon blocked work.', 'blocked', 'cancelled', FALSE),
		('done_to_in_progress', 'Reopen completed work.', 'done', 'in_progress', TRUE),
		('cancelled_to_triage', 'Re-open cancelled work into triage.', 'cancelled', 'triage', FALSE)
) AS item(name, description, from_code, to_code, require_assignment)
JOIN base_task_state from_state ON from_state.code = item.from_code
JOIN base_task_state to_state ON to_state.code = item.to_code
ON CONFLICT (from_state_id, to_state_id) DO UPDATE
SET name = EXCLUDED.name,
	description = EXCLUDED.description,
	require_assignment = EXCLUDED.require_assignment,
	_deleted_at = NULL,
	_updated_at = NOW();

INSERT INTO _app (
	name,
	namespace,
	label,
	description,
	status,
	definition_yaml,
	published_definition_yaml,
	definition_version,
	published_version
)
VALUES (
	'base',
	'base',
	'Base',
	'Foundational work and entity model for reusable platform apps.',
	'active',
	trim($yaml$
name: base
namespace: base
label: Base
description: Foundational work and entity model for reusable platform apps.
dependencies:
  - system
tables:
  - name: base_task
    extensible: true
    label_singular: Task
    label_plural: Tasks
    description: Bare-bones work records with assignment, priority, state, and entity linkage.
    display_field: number
    columns:
      - name: number
        label: Number
        data_type: text
        is_nullable: false
      - name: title
        label: Title
        data_type: text
        is_nullable: false
      - name: description
        label: Description
        data_type: long_text
        is_nullable: true
      - name: work_type
        label: Task Type
        data_type: text
        is_nullable: false
        default_value: TASK
      - name: state_id
        label: State
        data_type: reference
        is_nullable: false
        reference_table: base_task_state
      - name: priority
        label: Priority
        data_type: choice
        is_nullable: false
        choices:
          - value: p1
            label: P1 Critical
          - value: p2
            label: P2 High
          - value: p3
            label: P3 Standard
          - value: p4
            label: P4 Low
          - value: p5
            label: P5 Planning
      - name: entity_id
        label: Affected Entity
        data_type: reference
        is_nullable: true
        reference_table: base_entity
      - name: parent_task_id
        label: Parent Task
        data_type: reference
        is_nullable: true
        reference_table: base_task
      - name: assignment_group_id
        label: Assignment Group
        data_type: reference
        is_nullable: true
        reference_table: _group
      - name: assigned_user_id
        label: Assignee
        data_type: reference
        is_nullable: true
        reference_table: _user
      - name: requested_by_user_id
        label: Requested By
        data_type: reference
        is_nullable: true
        reference_table: _user
      - name: state_changed_at
        label: State Changed At
        data_type: timestamptz
        is_nullable: false
      - name: planned_start_at
        label: Planned Start
        data_type: timestamptz
        is_nullable: true
      - name: due_at
        label: Due At
        data_type: timestamptz
        is_nullable: true
      - name: started_at
        label: Started At
        data_type: timestamptz
        is_nullable: true
      - name: resolved_at
        label: Resolved At
        data_type: timestamptz
        is_nullable: true
      - name: closed_at
        label: Closed At
        data_type: timestamptz
        is_nullable: true
      - name: board_rank
        label: Board Rank
        data_type: numeric
        is_nullable: false
    forms:
      - name: default
        label: Default
        fields:
          - number
          - title
          - work_type
          - state_id
          - priority
          - entity_id
          - parent_task_id
          - assignment_group_id
          - assigned_user_id
          - requested_by_user_id
          - planned_start_at
          - due_at
          - started_at
          - resolved_at
          - closed_at
          - board_rank
          - description
    lists:
      - name: default
        label: Default
        columns:
          - number
          - title
          - state_id
          - priority
          - assigned_user_id
          - entity_id
          - due_at
          - _updated_at
    related_lists:
      - name: child_tasks
        label: Child Tasks
        table: base_task
        reference_field: parent_task_id
        columns:
          - number
          - title
          - state_id
          - priority
  - name: base_task_state
    label_singular: Task State
    label_plural: Task States
    description: Configurable task states used by the workflow engine.
    display_field: label
    columns:
      - name: code
        label: Code
        data_type: text
        is_nullable: false
      - name: label
        label: Label
        data_type: text
        is_nullable: false
      - name: description
        label: Description
        data_type: long_text
        is_nullable: true
      - name: category
        label: Category
        data_type: choice
        is_nullable: false
        choices:
          - value: queue
            label: Queue
          - value: active
            label: Active
          - value: blocked
            label: Blocked
          - value: done
            label: Done
          - value: cancelled
            label: Cancelled
      - name: board_lane
        label: Board Lane
        data_type: text
        is_nullable: false
      - name: sort_order
        label: Sort Order
        data_type: integer
        is_nullable: false
      - name: is_initial
        label: Initial
        data_type: boolean
        is_nullable: false
      - name: is_terminal
        label: Terminal
        data_type: boolean
        is_nullable: false
      - name: is_closed
        label: Closed
        data_type: boolean
        is_nullable: false
    forms:
      - name: default
        label: Default
        fields:
          - code
          - label
          - category
          - board_lane
          - sort_order
          - is_initial
          - is_terminal
          - is_closed
          - description
    lists:
      - name: default
        label: Default
        columns:
          - label
          - category
          - board_lane
          - sort_order
          - is_initial
          - is_terminal
          - is_closed
    related_lists:
      - name: tasks
        label: Tasks
        table: base_task
        reference_field: state_id
        columns:
          - number
          - title
          - priority
          - assigned_user_id
      - name: outgoing_transitions
        label: Outgoing Transitions
        table: base_task_transition
        reference_field: from_state_id
        columns:
          - name
          - to_state_id
          - require_assignment
      - name: incoming_transitions
        label: Incoming Transitions
        table: base_task_transition
        reference_field: to_state_id
        columns:
          - name
          - from_state_id
          - require_assignment
  - name: base_task_transition
    label_singular: Task Transition
    label_plural: Task Transitions
    description: Allowed state changes for tasks.
    display_field: name
    columns:
      - name: name
        label: Name
        data_type: text
        is_nullable: false
      - name: description
        label: Description
        data_type: long_text
        is_nullable: true
      - name: from_state_id
        label: From State
        data_type: reference
        is_nullable: false
        reference_table: base_task_state
      - name: to_state_id
        label: To State
        data_type: reference
        is_nullable: false
        reference_table: base_task_state
      - name: require_assignment
        label: Require Assignment
        data_type: boolean
        is_nullable: false
    forms:
      - name: default
        label: Default
        fields:
          - name
          - from_state_id
          - to_state_id
          - require_assignment
          - description
    lists:
      - name: default
        label: Default
        columns:
          - name
          - from_state_id
          - to_state_id
          - require_assignment
  - name: base_entity
    extensible: true
    label_singular: Entity
    label_plural: Entities
    description: External people, systems, assets, services, and other objects that work can affect.
    display_field: name
    columns:
      - name: number
        label: Number
        data_type: text
        is_nullable: false
      - name: name
        label: Name
        data_type: text
        is_nullable: false
      - name: description
        label: Description
        data_type: long_text
        is_nullable: true
      - name: entity_type
        label: Entity Type
        data_type: text
        is_nullable: false
      - name: lifecycle_state
        label: Lifecycle State
        data_type: choice
        is_nullable: false
        choices:
          - value: planned
            label: Planned
          - value: active
            label: Active
          - value: maintenance
            label: Maintenance
          - value: retired
            label: Retired
          - value: disposed
            label: Disposed
      - name: operational_status
        label: Operational Status
        data_type: choice
        is_nullable: false
        choices:
          - value: operational
            label: Operational
          - value: degraded
            label: Degraded
          - value: offline
            label: Offline
          - value: standby
            label: Standby
          - value: unknown
            label: Unknown
      - name: criticality
        label: Criticality
        data_type: choice
        is_nullable: false
        choices:
          - value: p1
            label: P1 Critical
          - value: p2
            label: P2 High
          - value: p3
            label: P3 Standard
          - value: p4
            label: P4 Low
          - value: p5
            label: P5 Planning
      - name: source_system
        label: Source System
        data_type: text
        is_nullable: true
      - name: source_record_id
        label: Source Record ID
        data_type: text
        is_nullable: true
      - name: external_ref
        label: External Reference
        data_type: text
        is_nullable: true
      - name: asset_tag
        label: Asset Tag
        data_type: text
        is_nullable: true
      - name: serial_number
        label: Serial Number
        data_type: text
        is_nullable: true
      - name: owner_entity_id
        label: Owner Entity
        data_type: reference
        is_nullable: true
        reference_table: base_entity
      - name: responsible_group_id
        label: Responsible Group
        data_type: reference
        is_nullable: true
        reference_table: _group
      - name: responsible_user_id
        label: Responsible User
        data_type: reference
        is_nullable: true
        reference_table: _user
    forms:
      - name: default
        label: Default
        fields:
          - number
          - name
          - entity_type
          - lifecycle_state
          - operational_status
          - criticality
          - source_system
          - source_record_id
          - external_ref
          - asset_tag
          - serial_number
          - owner_entity_id
          - responsible_group_id
          - responsible_user_id
          - description
    lists:
      - name: default
        label: Default
        columns:
          - number
          - name
          - entity_type
          - lifecycle_state
          - operational_status
          - criticality
          - _updated_at
    related_lists:
      - name: tasks
        label: Tasks
        table: base_task
        reference_field: entity_id
        columns:
          - number
          - title
          - state_id
          - priority
      - name: outgoing_relationships
        label: Outgoing Relationships
        table: base_entity_relationship
        reference_field: source_entity_id
        columns:
          - relationship_type
          - target_entity_id
          - status
      - name: incoming_relationships
        label: Incoming Relationships
        table: base_entity_relationship
        reference_field: target_entity_id
        columns:
          - relationship_type
          - source_entity_id
          - status
  - name: base_entity_relationship
    label_singular: Entity Relationship
    label_plural: Entity Relationships
    description: Directed relationships between entities so impact and ownership are explicit.
    display_field: relationship_type
    columns:
      - name: source_entity_id
        label: Source Entity
        data_type: reference
        is_nullable: false
        reference_table: base_entity
      - name: target_entity_id
        label: Target Entity
        data_type: reference
        is_nullable: false
        reference_table: base_entity
      - name: relationship_type
        label: Relationship Type
        data_type: text
        is_nullable: false
      - name: status
        label: Status
        data_type: choice
        is_nullable: false
        choices:
          - value: planned
            label: Planned
          - value: active
            label: Active
          - value: inactive
            label: Inactive
          - value: retired
            label: Retired
      - name: effective_from
        label: Effective From
        data_type: timestamptz
        is_nullable: true
      - name: effective_to
        label: Effective To
        data_type: timestamptz
        is_nullable: true
      - name: description
        label: Description
        data_type: long_text
        is_nullable: true
    forms:
      - name: default
        label: Default
        fields:
          - source_entity_id
          - relationship_type
          - target_entity_id
          - status
          - effective_from
          - effective_to
          - description
    lists:
      - name: default
        label: Default
        columns:
          - relationship_type
          - source_entity_id
          - target_entity_id
          - status
$yaml$),
	trim($yaml$
name: base
namespace: base
label: Base
description: Foundational work and entity model for reusable platform apps.
dependencies:
  - system
tables:
  - name: base_task
    extensible: true
    label_singular: Task
    label_plural: Tasks
    description: Bare-bones work records with assignment, priority, state, and entity linkage.
    display_field: number
    columns:
      - name: number
        label: Number
        data_type: text
        is_nullable: false
      - name: title
        label: Title
        data_type: text
        is_nullable: false
      - name: description
        label: Description
        data_type: long_text
        is_nullable: true
      - name: work_type
        label: Task Type
        data_type: text
        is_nullable: false
        default_value: TASK
      - name: state_id
        label: State
        data_type: reference
        is_nullable: false
        reference_table: base_task_state
      - name: priority
        label: Priority
        data_type: choice
        is_nullable: false
        choices:
          - value: p1
            label: P1 Critical
          - value: p2
            label: P2 High
          - value: p3
            label: P3 Standard
          - value: p4
            label: P4 Low
          - value: p5
            label: P5 Planning
      - name: entity_id
        label: Affected Entity
        data_type: reference
        is_nullable: true
        reference_table: base_entity
      - name: parent_task_id
        label: Parent Task
        data_type: reference
        is_nullable: true
        reference_table: base_task
      - name: assignment_group_id
        label: Assignment Group
        data_type: reference
        is_nullable: true
        reference_table: _group
      - name: assigned_user_id
        label: Assignee
        data_type: reference
        is_nullable: true
        reference_table: _user
      - name: requested_by_user_id
        label: Requested By
        data_type: reference
        is_nullable: true
        reference_table: _user
      - name: state_changed_at
        label: State Changed At
        data_type: timestamptz
        is_nullable: false
      - name: planned_start_at
        label: Planned Start
        data_type: timestamptz
        is_nullable: true
      - name: due_at
        label: Due At
        data_type: timestamptz
        is_nullable: true
      - name: started_at
        label: Started At
        data_type: timestamptz
        is_nullable: true
      - name: resolved_at
        label: Resolved At
        data_type: timestamptz
        is_nullable: true
      - name: closed_at
        label: Closed At
        data_type: timestamptz
        is_nullable: true
      - name: board_rank
        label: Board Rank
        data_type: numeric
        is_nullable: false
    forms:
      - name: default
        label: Default
        fields:
          - number
          - title
          - work_type
          - state_id
          - priority
          - entity_id
          - parent_task_id
          - assignment_group_id
          - assigned_user_id
          - requested_by_user_id
          - planned_start_at
          - due_at
          - started_at
          - resolved_at
          - closed_at
          - board_rank
          - description
    lists:
      - name: default
        label: Default
        columns:
          - number
          - title
          - state_id
          - priority
          - assigned_user_id
          - entity_id
          - due_at
          - _updated_at
    related_lists:
      - name: child_tasks
        label: Child Tasks
        table: base_task
        reference_field: parent_task_id
        columns:
          - number
          - title
          - state_id
          - priority
  - name: base_task_state
    label_singular: Task State
    label_plural: Task States
    description: Configurable task states used by the workflow engine.
    display_field: label
    columns:
      - name: code
        label: Code
        data_type: text
        is_nullable: false
      - name: label
        label: Label
        data_type: text
        is_nullable: false
      - name: description
        label: Description
        data_type: long_text
        is_nullable: true
      - name: category
        label: Category
        data_type: choice
        is_nullable: false
        choices:
          - value: queue
            label: Queue
          - value: active
            label: Active
          - value: blocked
            label: Blocked
          - value: done
            label: Done
          - value: cancelled
            label: Cancelled
      - name: board_lane
        label: Board Lane
        data_type: text
        is_nullable: false
      - name: sort_order
        label: Sort Order
        data_type: integer
        is_nullable: false
      - name: is_initial
        label: Initial
        data_type: boolean
        is_nullable: false
      - name: is_terminal
        label: Terminal
        data_type: boolean
        is_nullable: false
      - name: is_closed
        label: Closed
        data_type: boolean
        is_nullable: false
    forms:
      - name: default
        label: Default
        fields:
          - code
          - label
          - category
          - board_lane
          - sort_order
          - is_initial
          - is_terminal
          - is_closed
          - description
    lists:
      - name: default
        label: Default
        columns:
          - label
          - category
          - board_lane
          - sort_order
          - is_initial
          - is_terminal
          - is_closed
    related_lists:
      - name: tasks
        label: Tasks
        table: base_task
        reference_field: state_id
        columns:
          - number
          - title
          - priority
          - assigned_user_id
      - name: outgoing_transitions
        label: Outgoing Transitions
        table: base_task_transition
        reference_field: from_state_id
        columns:
          - name
          - to_state_id
          - require_assignment
      - name: incoming_transitions
        label: Incoming Transitions
        table: base_task_transition
        reference_field: to_state_id
        columns:
          - name
          - from_state_id
          - require_assignment
  - name: base_task_transition
    label_singular: Task Transition
    label_plural: Task Transitions
    description: Allowed state changes for tasks.
    display_field: name
    columns:
      - name: name
        label: Name
        data_type: text
        is_nullable: false
      - name: description
        label: Description
        data_type: long_text
        is_nullable: true
      - name: from_state_id
        label: From State
        data_type: reference
        is_nullable: false
        reference_table: base_task_state
      - name: to_state_id
        label: To State
        data_type: reference
        is_nullable: false
        reference_table: base_task_state
      - name: require_assignment
        label: Require Assignment
        data_type: boolean
        is_nullable: false
    forms:
      - name: default
        label: Default
        fields:
          - name
          - from_state_id
          - to_state_id
          - require_assignment
          - description
    lists:
      - name: default
        label: Default
        columns:
          - name
          - from_state_id
          - to_state_id
          - require_assignment
  - name: base_entity
    extensible: true
    label_singular: Entity
    label_plural: Entities
    description: External people, systems, assets, services, and other objects that work can affect.
    display_field: name
    columns:
      - name: number
        label: Number
        data_type: text
        is_nullable: false
      - name: name
        label: Name
        data_type: text
        is_nullable: false
      - name: description
        label: Description
        data_type: long_text
        is_nullable: true
      - name: entity_type
        label: Entity Type
        data_type: text
        is_nullable: false
      - name: lifecycle_state
        label: Lifecycle State
        data_type: choice
        is_nullable: false
        choices:
          - value: planned
            label: Planned
          - value: active
            label: Active
          - value: maintenance
            label: Maintenance
          - value: retired
            label: Retired
          - value: disposed
            label: Disposed
      - name: operational_status
        label: Operational Status
        data_type: choice
        is_nullable: false
        choices:
          - value: operational
            label: Operational
          - value: degraded
            label: Degraded
          - value: offline
            label: Offline
          - value: standby
            label: Standby
          - value: unknown
            label: Unknown
      - name: criticality
        label: Criticality
        data_type: choice
        is_nullable: false
        choices:
          - value: p1
            label: P1 Critical
          - value: p2
            label: P2 High
          - value: p3
            label: P3 Standard
          - value: p4
            label: P4 Low
          - value: p5
            label: P5 Planning
      - name: source_system
        label: Source System
        data_type: text
        is_nullable: true
      - name: source_record_id
        label: Source Record ID
        data_type: text
        is_nullable: true
      - name: external_ref
        label: External Reference
        data_type: text
        is_nullable: true
      - name: asset_tag
        label: Asset Tag
        data_type: text
        is_nullable: true
      - name: serial_number
        label: Serial Number
        data_type: text
        is_nullable: true
      - name: owner_entity_id
        label: Owner Entity
        data_type: reference
        is_nullable: true
        reference_table: base_entity
      - name: responsible_group_id
        label: Responsible Group
        data_type: reference
        is_nullable: true
        reference_table: _group
      - name: responsible_user_id
        label: Responsible User
        data_type: reference
        is_nullable: true
        reference_table: _user
    forms:
      - name: default
        label: Default
        fields:
          - number
          - name
          - entity_type
          - lifecycle_state
          - operational_status
          - criticality
          - source_system
          - source_record_id
          - external_ref
          - asset_tag
          - serial_number
          - owner_entity_id
          - responsible_group_id
          - responsible_user_id
          - description
    lists:
      - name: default
        label: Default
        columns:
          - number
          - name
          - entity_type
          - lifecycle_state
          - operational_status
          - criticality
          - _updated_at
    related_lists:
      - name: tasks
        label: Tasks
        table: base_task
        reference_field: entity_id
        columns:
          - number
          - title
          - state_id
          - priority
      - name: outgoing_relationships
        label: Outgoing Relationships
        table: base_entity_relationship
        reference_field: source_entity_id
        columns:
          - relationship_type
          - target_entity_id
          - status
      - name: incoming_relationships
        label: Incoming Relationships
        table: base_entity_relationship
        reference_field: target_entity_id
        columns:
          - relationship_type
          - source_entity_id
          - status
  - name: base_entity_relationship
    label_singular: Entity Relationship
    label_plural: Entity Relationships
    description: Directed relationships between entities so impact and ownership are explicit.
    display_field: relationship_type
    columns:
      - name: source_entity_id
        label: Source Entity
        data_type: reference
        is_nullable: false
        reference_table: base_entity
      - name: target_entity_id
        label: Target Entity
        data_type: reference
        is_nullable: false
        reference_table: base_entity
      - name: relationship_type
        label: Relationship Type
        data_type: text
        is_nullable: false
      - name: status
        label: Status
        data_type: choice
        is_nullable: false
        choices:
          - value: planned
            label: Planned
          - value: active
            label: Active
          - value: inactive
            label: Inactive
          - value: retired
            label: Retired
      - name: effective_from
        label: Effective From
        data_type: timestamptz
        is_nullable: true
      - name: effective_to
        label: Effective To
        data_type: timestamptz
        is_nullable: true
      - name: description
        label: Description
        data_type: long_text
        is_nullable: true
    forms:
      - name: default
        label: Default
        fields:
          - source_entity_id
          - relationship_type
          - target_entity_id
          - status
          - effective_from
          - effective_to
          - description
    lists:
      - name: default
        label: Default
        columns:
          - relationship_type
          - source_entity_id
          - target_entity_id
          - status
$yaml$),
	1,
	1
)
ON CONFLICT (name) DO UPDATE
SET label = EXCLUDED.label,
	description = EXCLUDED.description,
	status = EXCLUDED.status,
	definition_yaml = EXCLUDED.definition_yaml,
	published_definition_yaml = EXCLUDED.published_definition_yaml,
	definition_version = CASE
		WHEN COALESCE(_app.definition_yaml, '') = EXCLUDED.definition_yaml THEN _app.definition_version
		ELSE GREATEST(_app.definition_version, _app.published_version) + 1
	END,
	published_version = CASE
		WHEN COALESCE(_app.published_definition_yaml, '') = EXCLUDED.published_definition_yaml THEN _app.published_version
		ELSE GREATEST(_app.definition_version, _app.published_version) + 1
	END,
	_updated_at = NOW();
