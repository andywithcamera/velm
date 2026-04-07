CREATE TABLE IF NOT EXISTS base_counters (
	app_prefix TEXT PRIMARY KEY,
	prefix TEXT NOT NULL,
	next_number BIGINT NOT NULL DEFAULT 1,
	CONSTRAINT chk_base_counters_prefix CHECK (prefix ~ '^[A-Z]{4}$'),
	CONSTRAINT chk_base_counters_app_prefix CHECK (app_prefix ~ '^[a-z0-9_]+_[A-Z]{4}$'),
	CONSTRAINT chk_base_counters_next_number CHECK (next_number >= 1)
);

CREATE INDEX IF NOT EXISTS idx_base_counters_prefix ON base_counters(prefix);

CREATE OR REPLACE FUNCTION base_sync_autnumber_counter(input_app_prefix TEXT, input_prefix TEXT, current_value TEXT)
RETURNS VOID
LANGUAGE plpgsql
AS $$
DECLARE
	normalized_app_prefix TEXT := BTRIM(COALESCE(input_app_prefix, ''));
	normalized_prefix TEXT := UPPER(BTRIM(COALESCE(input_prefix, '')));
	normalized_value TEXT := BTRIM(COALESCE(current_value, ''));
	next_value BIGINT;
BEGIN
	IF normalized_value = '' THEN
		RETURN;
	END IF;
	IF normalized_app_prefix = '' THEN
		RAISE EXCEPTION 'app_prefix is required';
	END IF;
	IF normalized_prefix !~ '^[A-Z]{4}$' THEN
		RAISE EXCEPTION 'prefix must be exactly four uppercase letters';
	END IF;
	IF normalized_value !~ ('^' || normalized_prefix || '-[0-9]{6,}$') THEN
		RAISE EXCEPTION 'invalid autnumber value % for prefix %', normalized_value, normalized_prefix;
	END IF;

	next_value := substring(normalized_value FROM '([0-9]+)$')::BIGINT + 1;
	INSERT INTO base_counters (app_prefix, prefix, next_number)
	VALUES (normalized_app_prefix, normalized_prefix, next_value)
	ON CONFLICT (app_prefix) DO UPDATE
	SET prefix = EXCLUDED.prefix,
		next_number = GREATEST(base_counters.next_number, EXCLUDED.next_number);
END;
$$;

CREATE OR REPLACE FUNCTION base_next_autnumber(input_app_prefix TEXT, input_prefix TEXT)
RETURNS TEXT
LANGUAGE plpgsql
AS $$
DECLARE
	normalized_app_prefix TEXT := BTRIM(COALESCE(input_app_prefix, ''));
	normalized_prefix TEXT := UPPER(BTRIM(COALESCE(input_prefix, '')));
	next_value BIGINT;
BEGIN
	IF normalized_app_prefix = '' THEN
		RAISE EXCEPTION 'app_prefix is required';
	END IF;
	IF normalized_prefix !~ '^[A-Z]{4}$' THEN
		RAISE EXCEPTION 'prefix must be exactly four uppercase letters';
	END IF;

	INSERT INTO base_counters (app_prefix, prefix, next_number)
	VALUES (normalized_app_prefix, normalized_prefix, 2)
	ON CONFLICT (app_prefix) DO UPDATE
	SET prefix = EXCLUDED.prefix,
		next_number = base_counters.next_number + 1
	RETURNING base_counters.next_number - 1
	INTO next_value;

	RETURN normalized_prefix || '-' || LPAD(next_value::TEXT, 6, '0');
END;
$$;

CREATE OR REPLACE FUNCTION base_autnumber_before_write()
RETURNS trigger
LANGUAGE plpgsql
AS $$
DECLARE
	config JSONB := '[]'::JSONB;
	item JSONB;
	column_name TEXT;
	column_value TEXT;
	next_value TEXT;
BEGIN
	IF TG_NARGS > 0 AND BTRIM(COALESCE(TG_ARGV[0], '')) <> '' THEN
		config := TG_ARGV[0]::JSONB;
	END IF;

	FOR item IN SELECT value FROM jsonb_array_elements(config) LOOP
		column_name := BTRIM(COALESCE(item ->> 'column', ''));
		IF column_name = '' THEN
			CONTINUE;
		END IF;

		EXECUTE format('SELECT ($1).%I::TEXT', column_name) INTO column_value USING NEW;
		IF COALESCE(BTRIM(column_value), '') = '' THEN
			next_value := base_next_autnumber(item ->> 'app_prefix', item ->> 'prefix');
			NEW := jsonb_populate_record(NEW, jsonb_build_object(column_name, next_value));
		ELSE
			PERFORM base_sync_autnumber_counter(item ->> 'app_prefix', item ->> 'prefix', column_value);
		END IF;
	END LOOP;

	RETURN NEW;
END;
$$;

UPDATE base_task
SET number = 'TASK-' || LPAD(substring(number FROM '([0-9]+)$'), 6, '0')
WHERE number IS NOT NULL
  AND BTRIM(number) <> ''
  AND number !~ '^TASK-[0-9]{6,}$'
  AND number ~ '[0-9]+$';

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

DELETE FROM base_counters WHERE app_prefix = 'base_TASK';

SELECT base_sync_autnumber_counter('base_TASK', 'TASK', number)
FROM base_task
WHERE number IS NOT NULL
  AND BTRIM(number) <> '';

UPDATE base_task
SET number = base_next_autnumber('base_TASK', 'TASK')
WHERE number IS NULL
   OR BTRIM(number) = '';

DROP TRIGGER IF EXISTS trg_base_task_before_write ON base_task;
CREATE TRIGGER trg_base_task_before_write
BEFORE INSERT OR UPDATE ON base_task
FOR EACH ROW
EXECUTE FUNCTION base_task_before_write();

DROP TRIGGER IF EXISTS trg_velm_autnumber_before_write ON base_task;
CREATE TRIGGER trg_velm_autnumber_before_write
BEFORE INSERT OR UPDATE ON base_task
FOR EACH ROW
EXECUTE FUNCTION base_autnumber_before_write('[{"column":"number","prefix":"TASK","app_prefix":"base_TASK"}]');

DROP FUNCTION IF EXISTS base_task_number_prefix(TEXT);
DROP FUNCTION IF EXISTS base_sync_task_number_counter(TEXT, TEXT);
DROP FUNCTION IF EXISTS base_next_task_number(TEXT);
DROP TABLE IF EXISTS base_task_number_counter;
DROP SEQUENCE IF EXISTS base_task_number_seq;
