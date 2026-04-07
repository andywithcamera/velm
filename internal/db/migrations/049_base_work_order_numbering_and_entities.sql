CREATE TABLE IF NOT EXISTS base_task_number_counter (
	table_name TEXT PRIMARY KEY,
	next_value BIGINT NOT NULL CHECK (next_value > 0)
);

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

CREATE TABLE IF NOT EXISTS base_task_entity (
	_id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
	_created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
	_updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
	_update_count BIGINT NOT NULL DEFAULT 0,
	_deleted_at TIMESTAMPTZ,
	_created_by UUID,
	_updated_by UUID,
	_deleted_by UUID,
	task_id UUID NOT NULL REFERENCES base_task(_id) ON DELETE CASCADE,
	entity_id UUID NOT NULL REFERENCES base_entity(_id) ON DELETE CASCADE,
	CONSTRAINT uq_base_task_entity UNIQUE (task_id, entity_id)
);

SELECT _ensure_record_version_trigger('base_task_entity');

INSERT INTO base_task_entity (
	_created_at,
	_updated_at,
	_created_by,
	_updated_by,
	task_id,
	entity_id
)
SELECT
	COALESCE(_created_at, NOW()),
	COALESCE(_updated_at, NOW()),
	_created_by,
	_updated_by,
	_id,
	entity_id
FROM base_task
WHERE entity_id IS NOT NULL
ON CONFLICT (task_id, entity_id) DO NOTHING;

DROP INDEX IF EXISTS idx_base_task_entity;

ALTER TABLE base_task
	DROP COLUMN IF EXISTS entity_id;

CREATE INDEX IF NOT EXISTS idx_base_task_entity_task
	ON base_task_entity(task_id);

CREATE INDEX IF NOT EXISTS idx_base_task_entity_entity
	ON base_task_entity(entity_id);
