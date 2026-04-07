DO $$
BEGIN
	IF to_regclass('base_work_order') IS NOT NULL AND to_regclass('base_task') IS NULL THEN
		EXECUTE 'ALTER TABLE base_work_order RENAME TO base_task';
	END IF;
	IF to_regclass('base_work_order_entity') IS NOT NULL AND to_regclass('base_task_entity') IS NULL THEN
		EXECUTE 'ALTER TABLE base_work_order_entity RENAME TO base_task_entity';
	END IF;
	IF to_regclass('base_work_order_number_counter') IS NOT NULL AND to_regclass('base_task_number_counter') IS NULL THEN
		EXECUTE 'ALTER TABLE base_work_order_number_counter RENAME TO base_task_number_counter';
	END IF;
	IF to_regclass('base_work_order_number_seq') IS NOT NULL AND to_regclass('base_task_number_seq') IS NULL THEN
		EXECUTE 'ALTER SEQUENCE base_work_order_number_seq RENAME TO base_task_number_seq';
	END IF;
END;
$$;

DO $$
BEGIN
	IF EXISTS (
		SELECT 1
		FROM information_schema.columns
		WHERE table_schema = current_schema()
		  AND table_name = 'base_task'
		  AND column_name = 'parent_work_order_id'
	) AND NOT EXISTS (
		SELECT 1
		FROM information_schema.columns
		WHERE table_schema = current_schema()
		  AND table_name = 'base_task'
		  AND column_name = 'parent_task_id'
	) THEN
		EXECUTE 'ALTER TABLE base_task RENAME COLUMN parent_work_order_id TO parent_task_id';
	END IF;

	IF EXISTS (
		SELECT 1
		FROM information_schema.columns
		WHERE table_schema = current_schema()
		  AND table_name = 'base_task_entity'
		  AND column_name = 'work_order_id'
	) AND NOT EXISTS (
		SELECT 1
		FROM information_schema.columns
		WHERE table_schema = current_schema()
		  AND table_name = 'base_task_entity'
		  AND column_name = 'task_id'
	) THEN
		EXECUTE 'ALTER TABLE base_task_entity RENAME COLUMN work_order_id TO task_id';
	END IF;
END;
$$;

CREATE TABLE IF NOT EXISTS base_task_number_counter (
	table_name TEXT PRIMARY KEY,
	next_value BIGINT NOT NULL CHECK (next_value > 0)
);

INSERT INTO base_task_number_counter (table_name, next_value)
SELECT
	'base_task',
	next_value
FROM base_task_number_counter
WHERE table_name = 'base_work_order'
ON CONFLICT (table_name) DO UPDATE
SET next_value = GREATEST(base_task_number_counter.next_value, EXCLUDED.next_value);

DELETE FROM base_task_number_counter
WHERE table_name = 'base_work_order';

DO $$
BEGIN
	IF EXISTS (
		SELECT 1
		FROM pg_constraint
		WHERE conname = 'base_work_order_pkey'
	) AND NOT EXISTS (
		SELECT 1
		FROM pg_constraint
		WHERE conname = 'base_task_pkey'
	) THEN
		EXECUTE 'ALTER TABLE base_task RENAME CONSTRAINT base_work_order_pkey TO base_task_pkey';
	END IF;
	IF EXISTS (
		SELECT 1
		FROM pg_constraint
		WHERE conname = 'chk_base_work_order_number'
	) AND NOT EXISTS (
		SELECT 1
		FROM pg_constraint
		WHERE conname = 'chk_base_task_number'
	) THEN
		EXECUTE 'ALTER TABLE base_task RENAME CONSTRAINT chk_base_work_order_number TO chk_base_task_number';
	END IF;
	IF EXISTS (
		SELECT 1
		FROM pg_constraint
		WHERE conname = 'chk_base_work_order_title'
	) AND NOT EXISTS (
		SELECT 1
		FROM pg_constraint
		WHERE conname = 'chk_base_task_title'
	) THEN
		EXECUTE 'ALTER TABLE base_task RENAME CONSTRAINT chk_base_work_order_title TO chk_base_task_title';
	END IF;
	IF EXISTS (
		SELECT 1
		FROM pg_constraint
		WHERE conname = 'chk_base_work_order_type'
	) AND NOT EXISTS (
		SELECT 1
		FROM pg_constraint
		WHERE conname = 'chk_base_task_type'
	) THEN
		EXECUTE 'ALTER TABLE base_task RENAME CONSTRAINT chk_base_work_order_type TO chk_base_task_type';
	END IF;
	IF EXISTS (
		SELECT 1
		FROM pg_constraint
		WHERE conname = 'chk_base_work_order_priority'
	) AND NOT EXISTS (
		SELECT 1
		FROM pg_constraint
		WHERE conname = 'chk_base_task_priority'
	) THEN
		EXECUTE 'ALTER TABLE base_task RENAME CONSTRAINT chk_base_work_order_priority TO chk_base_task_priority';
	END IF;
	IF EXISTS (
		SELECT 1
		FROM pg_constraint
		WHERE conname = 'chk_base_work_order_assignment'
	) AND NOT EXISTS (
		SELECT 1
		FROM pg_constraint
		WHERE conname = 'chk_base_task_assignment'
	) THEN
		EXECUTE 'ALTER TABLE base_task RENAME CONSTRAINT chk_base_work_order_assignment TO chk_base_task_assignment';
	END IF;
	IF EXISTS (
		SELECT 1
		FROM pg_constraint
		WHERE conname = 'chk_base_work_order_state'
	) AND NOT EXISTS (
		SELECT 1
		FROM pg_constraint
		WHERE conname = 'chk_base_task_state'
	) THEN
		EXECUTE 'ALTER TABLE base_task RENAME CONSTRAINT chk_base_work_order_state TO chk_base_task_state';
	END IF;
	IF EXISTS (
		SELECT 1
		FROM pg_constraint
		WHERE conname = 'base_work_order_parent_work_order_id_fkey'
	) AND NOT EXISTS (
		SELECT 1
		FROM pg_constraint
		WHERE conname = 'base_task_parent_task_id_fkey'
	) THEN
		EXECUTE 'ALTER TABLE base_task RENAME CONSTRAINT base_work_order_parent_work_order_id_fkey TO base_task_parent_task_id_fkey';
	END IF;
	IF EXISTS (
		SELECT 1
		FROM pg_constraint
		WHERE conname = 'base_work_order_assignment_group_id_fkey'
	) AND NOT EXISTS (
		SELECT 1
		FROM pg_constraint
		WHERE conname = 'base_task_assignment_group_id_fkey'
	) THEN
		EXECUTE 'ALTER TABLE base_task RENAME CONSTRAINT base_work_order_assignment_group_id_fkey TO base_task_assignment_group_id_fkey';
	END IF;
	IF EXISTS (
		SELECT 1
		FROM pg_constraint
		WHERE conname = 'base_work_order_assigned_user_id_fkey'
	) AND NOT EXISTS (
		SELECT 1
		FROM pg_constraint
		WHERE conname = 'base_task_assigned_user_id_fkey'
	) THEN
		EXECUTE 'ALTER TABLE base_task RENAME CONSTRAINT base_work_order_assigned_user_id_fkey TO base_task_assigned_user_id_fkey';
	END IF;
	IF EXISTS (
		SELECT 1
		FROM pg_constraint
		WHERE conname = 'base_work_order_requested_by_user_id_fkey'
	) AND NOT EXISTS (
		SELECT 1
		FROM pg_constraint
		WHERE conname = 'base_task_requested_by_user_id_fkey'
	) THEN
		EXECUTE 'ALTER TABLE base_task RENAME CONSTRAINT base_work_order_requested_by_user_id_fkey TO base_task_requested_by_user_id_fkey';
	END IF;
	IF EXISTS (
		SELECT 1
		FROM pg_constraint
		WHERE conname = 'base_work_order_entity_pkey'
	) AND NOT EXISTS (
		SELECT 1
		FROM pg_constraint
		WHERE conname = 'base_task_entity_pkey'
	) THEN
		EXECUTE 'ALTER TABLE base_task_entity RENAME CONSTRAINT base_work_order_entity_pkey TO base_task_entity_pkey';
	END IF;
	IF EXISTS (
		SELECT 1
		FROM pg_constraint
		WHERE conname = 'uq_base_work_order_entity'
	) AND NOT EXISTS (
		SELECT 1
		FROM pg_constraint
		WHERE conname = 'uq_base_task_entity'
	) THEN
		EXECUTE 'ALTER TABLE base_task_entity RENAME CONSTRAINT uq_base_work_order_entity TO uq_base_task_entity';
	END IF;
	IF EXISTS (
		SELECT 1
		FROM pg_constraint
		WHERE conname = 'base_work_order_entity_work_order_id_fkey'
	) AND NOT EXISTS (
		SELECT 1
		FROM pg_constraint
		WHERE conname = 'base_task_entity_task_id_fkey'
	) THEN
		EXECUTE 'ALTER TABLE base_task_entity RENAME CONSTRAINT base_work_order_entity_work_order_id_fkey TO base_task_entity_task_id_fkey';
	END IF;
	IF EXISTS (
		SELECT 1
		FROM pg_constraint
		WHERE conname = 'base_work_order_entity_entity_id_fkey'
	) AND NOT EXISTS (
		SELECT 1
		FROM pg_constraint
		WHERE conname = 'base_task_entity_entity_id_fkey'
	) THEN
		EXECUTE 'ALTER TABLE base_task_entity RENAME CONSTRAINT base_work_order_entity_entity_id_fkey TO base_task_entity_entity_id_fkey';
	END IF;
	IF EXISTS (
		SELECT 1
		FROM pg_constraint
		WHERE conname = 'base_work_order_number_counter_pkey'
	) AND NOT EXISTS (
		SELECT 1
		FROM pg_constraint
		WHERE conname = 'base_task_number_counter_pkey'
	) THEN
		EXECUTE 'ALTER TABLE base_task_number_counter RENAME CONSTRAINT base_work_order_number_counter_pkey TO base_task_number_counter_pkey';
	END IF;
END;
$$;

DO $$
BEGIN
	IF to_regclass('idx_base_work_order_number') IS NOT NULL AND to_regclass('idx_base_task_number') IS NULL THEN
		EXECUTE 'ALTER INDEX idx_base_work_order_number RENAME TO idx_base_task_number';
	END IF;
	IF to_regclass('idx_base_work_order_state') IS NOT NULL AND to_regclass('idx_base_task_state') IS NULL THEN
		EXECUTE 'ALTER INDEX idx_base_work_order_state RENAME TO idx_base_task_state';
	END IF;
	IF to_regclass('idx_base_work_order_assignment') IS NOT NULL AND to_regclass('idx_base_task_assignment') IS NULL THEN
		EXECUTE 'ALTER INDEX idx_base_work_order_assignment RENAME TO idx_base_task_assignment';
	END IF;
	IF to_regclass('idx_base_work_order_due_at') IS NOT NULL AND to_regclass('idx_base_task_due_at') IS NULL THEN
		EXECUTE 'ALTER INDEX idx_base_work_order_due_at RENAME TO idx_base_task_due_at';
	END IF;
	IF to_regclass('idx_base_work_order_parent') IS NOT NULL AND to_regclass('idx_base_task_parent') IS NULL THEN
		EXECUTE 'ALTER INDEX idx_base_work_order_parent RENAME TO idx_base_task_parent';
	END IF;
	IF to_regclass('idx_base_work_order_entity_work_order') IS NOT NULL AND to_regclass('idx_base_task_entity_task') IS NULL THEN
		EXECUTE 'ALTER INDEX idx_base_work_order_entity_work_order RENAME TO idx_base_task_entity_task';
	END IF;
	IF to_regclass('idx_base_work_order_entity_entity') IS NOT NULL AND to_regclass('idx_base_task_entity_entity') IS NULL THEN
		EXECUTE 'ALTER INDEX idx_base_work_order_entity_entity RENAME TO idx_base_task_entity_entity';
	END IF;
END;
$$;

CREATE OR REPLACE FUNCTION base_task_number_prefix(input_table_name TEXT)
RETURNS TEXT
LANGUAGE plpgsql
IMMUTABLE
AS $$
DECLARE
	normalized TEXT;
	parts TEXT[];
	filtered TEXT[] := ARRAY[]::TEXT[];
	part TEXT;
	prefix TEXT := '';
BEGIN
	normalized := LOWER(BTRIM(COALESCE(input_table_name, '')));
	IF normalized = '' OR normalized = 'base_task' THEN
		RETURN 'WO';
	END IF;

	parts := regexp_split_to_array(regexp_replace(normalized, '^_+', ''), '_+');
	IF COALESCE(array_length(parts, 1), 0) = 0 THEN
		RETURN 'WO';
	END IF;
	IF array_length(parts, 1) > 1 THEN
		parts := parts[2:array_length(parts, 1)];
	END IF;

	FOREACH part IN ARRAY parts LOOP
		part := regexp_replace(COALESCE(part, ''), '[^a-z0-9]+', '', 'g');
		IF part = '' THEN
			CONTINUE;
		END IF;
		filtered := array_append(filtered, part);
	END LOOP;

	IF COALESCE(array_length(filtered, 1), 0) = 0 THEN
		RETURN 'WO';
	END IF;

	IF array_length(filtered, 1) = 1 THEN
		part := filtered[1];
		IF length(part) <= 3 THEN
			prefix := UPPER(part);
		ELSE
			prefix := UPPER(substr(part, 1, 3));
		END IF;
	ELSE
		FOREACH part IN ARRAY filtered LOOP
			prefix := prefix || UPPER(substr(part, 1, 1));
		END LOOP;
	END IF;

	RETURN COALESCE(NULLIF(prefix, ''), 'WO');
END;
$$;

CREATE OR REPLACE FUNCTION base_sync_task_number_counter(input_table_name TEXT, input_number TEXT)
RETURNS VOID
LANGUAGE plpgsql
AS $$
DECLARE
	normalized_table TEXT;
	prefix TEXT;
	suffix BIGINT;
BEGIN
	normalized_table := LOWER(BTRIM(COALESCE(input_table_name, '')));
	input_number := BTRIM(COALESCE(input_number, ''));
	IF normalized_table = '' OR input_number = '' THEN
		RETURN;
	END IF;

	prefix := base_task_number_prefix(normalized_table);
	IF input_number !~ ('^' || prefix || '-[0-9]+$') THEN
		RETURN;
	END IF;

	suffix := substring(input_number FROM '([0-9]+)$')::BIGINT + 1;
	INSERT INTO base_task_number_counter (table_name, next_value)
	VALUES (normalized_table, suffix)
	ON CONFLICT (table_name) DO UPDATE
	SET next_value = GREATEST(base_task_number_counter.next_value, EXCLUDED.next_value);
END;
$$;

CREATE OR REPLACE FUNCTION base_next_task_number(input_table_name TEXT)
RETURNS TEXT
LANGUAGE plpgsql
AS $$
DECLARE
	normalized_table TEXT;
	prefix TEXT;
	next_value BIGINT;
BEGIN
	normalized_table := LOWER(BTRIM(COALESCE(input_table_name, '')));
	IF normalized_table = '' THEN
		normalized_table := 'base_task';
	END IF;

	prefix := base_task_number_prefix(normalized_table);

	INSERT INTO base_task_number_counter (table_name, next_value)
	VALUES (normalized_table, 2)
	ON CONFLICT (table_name) DO UPDATE
	SET next_value = base_task_number_counter.next_value + 1
	RETURNING base_task_number_counter.next_value - 1
	INTO next_value;

	RETURN prefix || '-' || LPAD(next_value::TEXT, 6, '0');
END;
$$;

INSERT INTO base_task_number_counter (table_name, next_value)
SELECT
	'base_task',
	COALESCE(MAX(CASE
		WHEN number ~ '[0-9]+$' THEN substring(number FROM '([0-9]+)$')::BIGINT
		ELSE NULL
	END), 0) + 1
FROM base_task
ON CONFLICT (table_name) DO UPDATE
SET next_value = GREATEST(base_task_number_counter.next_value, EXCLUDED.next_value);

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
		NEW.number := base_next_task_number(TG_TABLE_NAME);
	ELSE
		PERFORM base_sync_task_number_counter(TG_TABLE_NAME, NEW.number);
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

DROP TRIGGER IF EXISTS trg_base_task_before_write ON base_task;
DROP TRIGGER IF EXISTS trg_base_work_order_before_write ON base_task;

CREATE TRIGGER trg_base_task_before_write
BEFORE INSERT OR UPDATE ON base_task
FOR EACH ROW
EXECUTE FUNCTION base_task_before_write();

DROP FUNCTION IF EXISTS base_work_order_before_write();
DROP FUNCTION IF EXISTS base_next_work_order_number(TEXT);
DROP FUNCTION IF EXISTS base_sync_work_order_number_counter(TEXT, TEXT);
DROP FUNCTION IF EXISTS base_work_order_number_prefix(TEXT);

SELECT _ensure_record_version_trigger('base_task_entity');

CREATE INDEX IF NOT EXISTS idx_base_task_number ON base_task(number);
CREATE INDEX IF NOT EXISTS idx_base_task_state ON base_task(state, priority, board_rank);
CREATE INDEX IF NOT EXISTS idx_base_task_assignment ON base_task(assignment_group_id, assigned_user_id);
CREATE INDEX IF NOT EXISTS idx_base_task_due_at ON base_task(due_at);
CREATE INDEX IF NOT EXISTS idx_base_task_parent ON base_task(parent_task_id);
CREATE INDEX IF NOT EXISTS idx_base_task_entity_task ON base_task_entity(task_id);
CREATE INDEX IF NOT EXISTS idx_base_task_entity_entity ON base_task_entity(entity_id);
