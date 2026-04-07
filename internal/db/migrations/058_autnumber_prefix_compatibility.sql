ALTER TABLE base_counters
	DROP CONSTRAINT IF EXISTS chk_base_counters_prefix;

ALTER TABLE base_counters
	DROP CONSTRAINT IF EXISTS chk_base_counters_app_prefix;

ALTER TABLE base_counters
	ADD CONSTRAINT chk_base_counters_prefix CHECK (prefix ~ '^[A-Z]{3,4}$');

ALTER TABLE base_counters
	ADD CONSTRAINT chk_base_counters_app_prefix CHECK (app_prefix ~ '^[a-z0-9_]+_[A-Z]{3,4}$');

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
	IF normalized_prefix !~ '^[A-Z]{3,4}$' THEN
		RAISE EXCEPTION 'prefix must be three or four uppercase letters';
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
	IF normalized_prefix !~ '^[A-Z]{3,4}$' THEN
		RAISE EXCEPTION 'prefix must be three or four uppercase letters';
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
