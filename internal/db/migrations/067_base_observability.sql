CREATE TABLE IF NOT EXISTS base_obs_definition (
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
	observable_class TEXT NOT NULL DEFAULT 'custom',
	golden_signal TEXT,
	metric_name TEXT NOT NULL,
	value_schema JSONB,
	default_thresholds JSONB,
	default_check_interval INTEGER NOT NULL DEFAULT 300,
	CONSTRAINT uq_base_obs_definition_name UNIQUE (name),
	CONSTRAINT uq_base_obs_definition_metric UNIQUE (observable_class, metric_name),
	CONSTRAINT chk_base_obs_definition_name CHECK (BTRIM(name) <> ''),
	CONSTRAINT chk_base_obs_definition_class CHECK (observable_class IN ('cpu', 'memory', 'disk', 'http', 'custom')),
	CONSTRAINT chk_base_obs_definition_signal CHECK (golden_signal IS NULL OR golden_signal IN ('latency', 'traffic', 'errors', 'saturation')),
	CONSTRAINT chk_base_obs_definition_metric_name CHECK (BTRIM(metric_name) <> ''),
	CONSTRAINT chk_base_obs_definition_interval CHECK (default_check_interval > 0)
);

CREATE TABLE IF NOT EXISTS base_obs_default_action (
	_id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
	_created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
	_updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
	_update_count BIGINT NOT NULL DEFAULT 0,
	_deleted_at TIMESTAMPTZ,
	_created_by UUID,
	_updated_by UUID,
	_deleted_by UUID,
	observable_class TEXT,
	name TEXT NOT NULL,
	trigger_state TEXT NOT NULL,
	trigger_severity INTEGER,
	flap_guard_count INTEGER,
	flap_guard_window INTEGER,
	action_type TEXT NOT NULL,
	task_type TEXT,
	task_priority TEXT,
	task_title TEXT,
	task_description TEXT,
	enabled BOOLEAN NOT NULL DEFAULT TRUE,
	CONSTRAINT chk_base_obs_default_action_class CHECK (observable_class IS NULL OR observable_class IN ('cpu', 'memory', 'disk', 'http', 'custom')),
	CONSTRAINT chk_base_obs_default_action_name CHECK (BTRIM(name) <> ''),
	CONSTRAINT chk_base_obs_default_action_state CHECK (trigger_state IN ('ok', 'warning', 'critical', 'unknown')),
	CONSTRAINT chk_base_obs_default_action_severity CHECK (trigger_severity IS NULL OR (trigger_severity >= 0 AND trigger_severity <= 5)),
	CONSTRAINT chk_base_obs_default_action_flap_count CHECK (flap_guard_count IS NULL OR flap_guard_count >= 0),
	CONSTRAINT chk_base_obs_default_action_flap_window CHECK (flap_guard_window IS NULL OR flap_guard_window >= 0),
	CONSTRAINT chk_base_obs_default_action_type CHECK (action_type IN ('create_task', 'resolve_task', 'cancel_task', 'notify')),
	CONSTRAINT chk_base_obs_default_action_priority CHECK (task_priority IS NULL OR task_priority IN ('low', 'medium', 'high', 'critical'))
);

CREATE TABLE IF NOT EXISTS base_obs_observable (
	_id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
	_created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
	_updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
	_update_count BIGINT NOT NULL DEFAULT 0,
	_deleted_at TIMESTAMPTZ,
	_created_by UUID,
	_updated_by UUID,
	_deleted_by UUID,
	definition_id UUID NOT NULL REFERENCES base_obs_definition(_id) ON DELETE CASCADE,
	entity_id UUID NOT NULL REFERENCES base_entity(_id) ON DELETE CASCADE,
	node TEXT NOT NULL,
	resource TEXT,
	display_name TEXT NOT NULL,
	severity_config JSONB,
	check_interval INTEGER,
	current_severity INTEGER NOT NULL DEFAULT 0,
	current_state TEXT NOT NULL DEFAULT 'unknown',
	last_value JSONB,
	last_observed_at TIMESTAMPTZ,
	state_changed_at TIMESTAMPTZ,
	flap_count INTEGER NOT NULL DEFAULT 0,
	slo_context JSONB,
	message_key TEXT NOT NULL,
	enabled BOOLEAN NOT NULL DEFAULT TRUE,
	CONSTRAINT chk_base_obs_observable_node CHECK (BTRIM(node) <> ''),
	CONSTRAINT chk_base_obs_observable_display_name CHECK (BTRIM(display_name) <> ''),
	CONSTRAINT chk_base_obs_observable_check_interval CHECK (check_interval IS NULL OR check_interval > 0),
	CONSTRAINT chk_base_obs_observable_severity CHECK (current_severity >= 0 AND current_severity <= 5),
	CONSTRAINT chk_base_obs_observable_state CHECK (current_state IN ('ok', 'warning', 'critical', 'unknown')),
	CONSTRAINT chk_base_obs_observable_flap_count CHECK (flap_count >= 0),
	CONSTRAINT chk_base_obs_observable_message_key CHECK (BTRIM(message_key) <> '')
);

CREATE TABLE IF NOT EXISTS base_obs_action (
	_id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
	_created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
	_updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
	_update_count BIGINT NOT NULL DEFAULT 0,
	_deleted_at TIMESTAMPTZ,
	_created_by UUID,
	_updated_by UUID,
	_deleted_by UUID,
	observable_id UUID NOT NULL REFERENCES base_obs_observable(_id) ON DELETE CASCADE,
	name TEXT NOT NULL,
	trigger_state TEXT NOT NULL,
	trigger_severity INTEGER,
	flap_guard_count INTEGER,
	flap_guard_window INTEGER,
	action_type TEXT NOT NULL,
	task_type TEXT,
	task_priority TEXT,
	task_title TEXT,
	task_description TEXT,
	enabled BOOLEAN NOT NULL DEFAULT TRUE,
	CONSTRAINT chk_base_obs_action_name CHECK (BTRIM(name) <> ''),
	CONSTRAINT chk_base_obs_action_state CHECK (trigger_state IN ('ok', 'warning', 'critical', 'unknown')),
	CONSTRAINT chk_base_obs_action_severity CHECK (trigger_severity IS NULL OR (trigger_severity >= 0 AND trigger_severity <= 5)),
	CONSTRAINT chk_base_obs_action_flap_count CHECK (flap_guard_count IS NULL OR flap_guard_count >= 0),
	CONSTRAINT chk_base_obs_action_flap_window CHECK (flap_guard_window IS NULL OR flap_guard_window >= 0),
	CONSTRAINT chk_base_obs_action_type CHECK (action_type IN ('create_task', 'resolve_task', 'cancel_task', 'notify')),
	CONSTRAINT chk_base_obs_action_priority CHECK (task_priority IS NULL OR task_priority IN ('low', 'medium', 'high', 'critical'))
);

CREATE TABLE IF NOT EXISTS base_obs_event (
	_id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
	_created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
	_updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
	_update_count BIGINT NOT NULL DEFAULT 0,
	_deleted_at TIMESTAMPTZ,
	_created_by UUID,
	_updated_by UUID,
	_deleted_by UUID,
	observable_id UUID NOT NULL REFERENCES base_obs_observable(_id) ON DELETE CASCADE,
	entity_id UUID NOT NULL REFERENCES base_entity(_id) ON DELETE CASCADE,
	source TEXT NOT NULL DEFAULT 'observability',
	node TEXT NOT NULL,
	resource TEXT,
	metric_name TEXT NOT NULL,
	severity INTEGER NOT NULL,
	summary TEXT,
	value JSONB,
	golden_signal TEXT,
	payload JSONB,
	occurred_at TIMESTAMPTZ NOT NULL,
	ingested_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
	CONSTRAINT chk_base_obs_event_source CHECK (BTRIM(source) <> ''),
	CONSTRAINT chk_base_obs_event_node CHECK (BTRIM(node) <> ''),
	CONSTRAINT chk_base_obs_event_metric_name CHECK (BTRIM(metric_name) <> ''),
	CONSTRAINT chk_base_obs_event_severity CHECK (severity >= 0 AND severity <= 5),
	CONSTRAINT chk_base_obs_event_signal CHECK (golden_signal IS NULL OR golden_signal IN ('latency', 'traffic', 'errors', 'saturation'))
);

ALTER TABLE base_obs_observable
	ADD COLUMN IF NOT EXISTS last_event_id UUID;

DO $$
BEGIN
	IF NOT EXISTS (
		SELECT 1
		FROM pg_constraint
		WHERE conname = 'base_obs_observable_last_event_id_fkey'
	) THEN
		ALTER TABLE base_obs_observable
			ADD CONSTRAINT base_obs_observable_last_event_id_fkey
			FOREIGN KEY (last_event_id) REFERENCES base_obs_event(_id) ON DELETE SET NULL;
	END IF;
END $$;

CREATE TABLE IF NOT EXISTS base_obs_task (
	_id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
	_created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
	_updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
	_update_count BIGINT NOT NULL DEFAULT 0,
	_deleted_at TIMESTAMPTZ,
	_created_by UUID,
	_updated_by UUID,
	_deleted_by UUID,
	observable_id UUID NOT NULL REFERENCES base_obs_observable(_id) ON DELETE CASCADE,
	action_id UUID NOT NULL REFERENCES base_obs_action(_id) ON DELETE CASCADE,
	task_id UUID NOT NULL REFERENCES base_task(_id) ON DELETE CASCADE,
	spawned_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
	closed_at TIMESTAMPTZ
);

SELECT _ensure_record_version_trigger('base_obs_definition');
SELECT _ensure_record_version_trigger('base_obs_default_action');
SELECT _ensure_record_version_trigger('base_obs_observable');
SELECT _ensure_record_version_trigger('base_obs_action');
SELECT _ensure_record_version_trigger('base_obs_event');
SELECT _ensure_record_version_trigger('base_obs_task');

CREATE OR REPLACE FUNCTION base_obs_observable_before_write()
RETURNS trigger
LANGUAGE plpgsql
AS $$
DECLARE
	definition_name TEXT;
	normalized_resource TEXT;
BEGIN
	SELECT d.name
	INTO definition_name
	FROM base_obs_definition d
	WHERE d._id = NEW.definition_id
	  AND d._deleted_at IS NULL;
	IF NOT FOUND THEN
		RAISE EXCEPTION 'definition_id references an inactive observation definition';
	END IF;

	NEW.node := LOWER(BTRIM(COALESCE(NEW.node, '')));
	IF NEW.node = '' THEN
		RAISE EXCEPTION 'node is required';
	END IF;

	normalized_resource := NULLIF(LOWER(BTRIM(COALESCE(NEW.resource, ''))), '');
	NEW.resource := normalized_resource;

	IF COALESCE(BTRIM(NEW.display_name), '') = '' THEN
		NEW.display_name := definition_name || ' on ' || NEW.node;
		IF NEW.resource IS NOT NULL THEN
			NEW.display_name := NEW.display_name || ' ' || NEW.resource;
		END IF;
	END IF;

	IF NEW.current_severity IS NULL THEN
		NEW.current_severity := 0;
	END IF;
	IF NEW.current_severity < 0 OR NEW.current_severity > 5 THEN
		RAISE EXCEPTION 'current_severity must be between 0 and 5';
	END IF;

	NEW.current_state := LOWER(BTRIM(COALESCE(NEW.current_state, 'unknown')));
	IF NEW.current_state NOT IN ('ok', 'warning', 'critical', 'unknown') THEN
		RAISE EXCEPTION 'invalid current_state %', NEW.current_state;
	END IF;

	IF NEW.flap_count IS NULL OR NEW.flap_count < 0 THEN
		NEW.flap_count := 0;
	END IF;

	IF COALESCE(BTRIM(NEW.message_key), '') = '' THEN
		NEW.message_key := 'obs_' || md5(
			LOWER(
				COALESCE(definition_name, '') || '::' ||
				COALESCE(NEW.entity_id::text, '') || '::' ||
				COALESCE(NEW.node, '') || '::' ||
				COALESCE(NEW.resource, '')
			)
		);
	END IF;

	RETURN NEW;
END;
$$;

DROP TRIGGER IF EXISTS trg_base_obs_observable_before_write ON base_obs_observable;
CREATE TRIGGER trg_base_obs_observable_before_write
BEFORE INSERT OR UPDATE ON base_obs_observable
FOR EACH ROW
EXECUTE FUNCTION base_obs_observable_before_write();

CREATE OR REPLACE FUNCTION base_obs_event_before_insert()
RETURNS trigger
LANGUAGE plpgsql
AS $$
DECLARE
	observable_entity_id UUID;
	observable_node TEXT;
	observable_resource TEXT;
	definition_metric_name TEXT;
	definition_signal TEXT;
BEGIN
	SELECT o.entity_id, o.node, o.resource, d.metric_name, d.golden_signal
	INTO observable_entity_id, observable_node, observable_resource, definition_metric_name, definition_signal
	FROM base_obs_observable o
	JOIN base_obs_definition d ON d._id = o.definition_id
	WHERE o._id = NEW.observable_id
	  AND o._deleted_at IS NULL
	  AND d._deleted_at IS NULL;
	IF NOT FOUND THEN
		RAISE EXCEPTION 'observable_id references an inactive observable';
	END IF;

	IF NEW.entity_id IS NULL THEN
		NEW.entity_id := observable_entity_id;
	ELSIF NEW.entity_id IS DISTINCT FROM observable_entity_id THEN
		RAISE EXCEPTION 'entity_id must match observable.entity_id';
	END IF;

	NEW.source := COALESCE(NULLIF(BTRIM(COALESCE(NEW.source, '')), ''), 'observability');
	NEW.node := LOWER(COALESCE(NULLIF(BTRIM(COALESCE(NEW.node, '')), ''), observable_node));
	IF NEW.node = '' THEN
		RAISE EXCEPTION 'event node is required';
	END IF;

	NEW.resource := NULLIF(LOWER(BTRIM(COALESCE(NEW.resource, observable_resource, ''))), '');
	NEW.metric_name := COALESCE(NULLIF(BTRIM(COALESCE(NEW.metric_name, '')), ''), definition_metric_name);
	IF COALESCE(BTRIM(NEW.metric_name), '') = '' THEN
		RAISE EXCEPTION 'metric_name is required';
	END IF;

	IF NEW.golden_signal IS NULL AND definition_signal IS NOT NULL THEN
		NEW.golden_signal := definition_signal;
	END IF;

	IF NEW.severity < 0 OR NEW.severity > 5 THEN
		RAISE EXCEPTION 'severity must be between 0 and 5';
	END IF;

	RETURN NEW;
END;
$$;

DROP TRIGGER IF EXISTS trg_base_obs_event_before_insert ON base_obs_event;
CREATE TRIGGER trg_base_obs_event_before_insert
BEFORE INSERT ON base_obs_event
FOR EACH ROW
EXECUTE FUNCTION base_obs_event_before_insert();

CREATE OR REPLACE FUNCTION base_obs_event_immutable_guard()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
	RAISE EXCEPTION 'base_obs_event rows are immutable';
END;
$$;

DROP TRIGGER IF EXISTS trg_base_obs_event_immutable_guard ON base_obs_event;
CREATE TRIGGER trg_base_obs_event_immutable_guard
BEFORE UPDATE OR DELETE ON base_obs_event
FOR EACH ROW
EXECUTE FUNCTION base_obs_event_immutable_guard();

CREATE UNIQUE INDEX IF NOT EXISTS idx_base_obs_observable_message_key
	ON base_obs_observable(message_key);

CREATE UNIQUE INDEX IF NOT EXISTS idx_base_obs_observable_target
	ON base_obs_observable(
		definition_id,
		entity_id,
		LOWER(node),
		LOWER(COALESCE(resource, ''))
	);

CREATE INDEX IF NOT EXISTS idx_base_obs_definition_metric
	ON base_obs_definition(observable_class, metric_name);

CREATE INDEX IF NOT EXISTS idx_base_obs_default_action_class
	ON base_obs_default_action(observable_class, enabled);

CREATE INDEX IF NOT EXISTS idx_base_obs_action_observable
	ON base_obs_action(observable_id, enabled);

CREATE INDEX IF NOT EXISTS idx_base_obs_event_observable_time
	ON base_obs_event(observable_id, occurred_at DESC);

CREATE INDEX IF NOT EXISTS idx_base_obs_event_entity_time
	ON base_obs_event(entity_id, occurred_at DESC);

CREATE INDEX IF NOT EXISTS idx_base_obs_event_metric
	ON base_obs_event(metric_name, severity, occurred_at DESC);

CREATE INDEX IF NOT EXISTS idx_base_obs_observable_entity_state
	ON base_obs_observable(entity_id, current_state, current_severity);

CREATE INDEX IF NOT EXISTS idx_base_obs_observable_last_observed
	ON base_obs_observable(last_observed_at DESC);

CREATE INDEX IF NOT EXISTS idx_base_obs_task_observable
	ON base_obs_task(observable_id, closed_at);

CREATE INDEX IF NOT EXISTS idx_base_obs_task_action
	ON base_obs_task(action_id, closed_at);

CREATE INDEX IF NOT EXISTS idx_base_obs_task_task
	ON base_obs_task(task_id);

CREATE UNIQUE INDEX IF NOT EXISTS base_obs_task_open_unique
	ON base_obs_task(observable_id, action_id)
	WHERE closed_at IS NULL AND _deleted_at IS NULL;

INSERT INTO base_obs_default_action (
	_id,
	observable_class,
	name,
	trigger_state,
	trigger_severity,
	flap_guard_count,
	flap_guard_window,
	action_type,
	task_type,
	task_priority,
	task_title,
	task_description,
	enabled
)
SELECT
	'8d7373b2-c5fb-4d3a-8347-b1a0f3bdf9f1'::uuid,
	NULL,
	'create_low_priority_incident_on_critical',
	'critical',
	4,
	2,
	600,
	'create_task',
	'incident',
	'low',
	'{{display_name}} is critical on {{node}}',
	'{{summary}}

Metric: {{metric_name}}
Source: {{source}}
State: {{current_state}}
Severity: {{current_severity}}
Entity: {{entity_id}}
Value: {{last_value}}',
	TRUE
WHERE NOT EXISTS (
	SELECT 1
	FROM base_obs_default_action
	WHERE _id = '8d7373b2-c5fb-4d3a-8347-b1a0f3bdf9f1'::uuid
);

INSERT INTO base_obs_default_action (
	_id,
	observable_class,
	name,
	trigger_state,
	trigger_severity,
	flap_guard_count,
	flap_guard_window,
	action_type,
	task_type,
	task_priority,
	task_title,
	task_description,
	enabled
)
SELECT
	'8822351e-b63d-4999-a809-89baf0f495db'::uuid,
	NULL,
	'resolve_incident_on_recovery',
	'ok',
	NULL,
	NULL,
	NULL,
	'resolve_task',
	'incident',
	'low',
	'',
	'',
	TRUE
WHERE NOT EXISTS (
	SELECT 1
	FROM base_obs_default_action
	WHERE _id = '8822351e-b63d-4999-a809-89baf0f495db'::uuid
);
