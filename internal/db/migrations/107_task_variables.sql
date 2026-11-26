-- Task variables move out of one jsonb column and into rows.
--
-- `base_task.variables` held a task's whole scope as a single document, so
-- every write was read-whole / mutate-in-Go / write-whole: two writers
-- touching different keys silently overwrote each other, and nothing could
-- query a variable without casting out of a document. A row per name is one
-- statement per value, and an index scan per question.
--
-- Scopes:
--   input   variables.inputs.<name>
--   local   variables.locals.<name>
--   output  variables.outputs.<name>
--   block   variables.block.<name>
--   top     every other top-level key, one row each: parent, blocks, item,
--           index, attempt_no. `parent` stays a document because it is a
--           snapshot of the ancestor's whole scope, not a variable this task
--           owns; `blocks` is the same for sibling results.
--
-- value holds the canonical JSON. value_text/value_number/value_bool are
-- projections of it, written by the application, so a filter compares a
-- typed column instead of casting every document in the table.

CREATE TABLE IF NOT EXISTS _task_variables (
	_id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
	_created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
	_updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
	task_id UUID NOT NULL REFERENCES base_task(_id) ON DELETE CASCADE,
	scope TEXT NOT NULL,
	name TEXT NOT NULL,
	value JSONB,
	value_text TEXT,
	value_number NUMERIC,
	value_bool BOOLEAN,
	CONSTRAINT uq_task_variables UNIQUE (task_id, scope, name)
);

CREATE INDEX IF NOT EXISTS idx_task_variables_task ON _task_variables (task_id, scope);
CREATE INDEX IF NOT EXISTS idx_task_variables_text ON _task_variables (scope, name, value_text) WHERE value_text IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_task_variables_number ON _task_variables (scope, name, value_number) WHERE value_number IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_task_variables_bool ON _task_variables (scope, name, value_bool) WHERE value_bool IS NOT NULL;

-- Backfill. An object-valued scope becomes one row per key; anything else at
-- the top level becomes a single `top` row under its own name.
INSERT INTO _task_variables (task_id, scope, name, value)
SELECT t._id, s.scope, kv.key, kv.value
FROM base_task t
JOIN LATERAL (
	SELECT 'input' AS scope, t.variables -> 'inputs' AS doc WHERE jsonb_typeof(t.variables -> 'inputs') = 'object'
	UNION ALL
	SELECT 'local', t.variables -> 'locals' WHERE jsonb_typeof(t.variables -> 'locals') = 'object'
	UNION ALL
	SELECT 'output', t.variables -> 'outputs' WHERE jsonb_typeof(t.variables -> 'outputs') = 'object'
	UNION ALL
	SELECT 'block', t.variables -> 'block' WHERE jsonb_typeof(t.variables -> 'block') = 'object'
) s ON TRUE
JOIN LATERAL jsonb_each(s.doc) kv ON TRUE
ON CONFLICT (task_id, scope, name) DO NOTHING;

INSERT INTO _task_variables (task_id, scope, name, value)
SELECT t._id, 'top', kv.key, kv.value
FROM base_task t
JOIN LATERAL jsonb_each(t.variables) kv ON TRUE
WHERE kv.key NOT IN ('inputs', 'locals', 'outputs', 'block')
ON CONFLICT (task_id, scope, name) DO NOTHING;

UPDATE _task_variables
SET value_text = CASE WHEN jsonb_typeof(value) = 'string' THEN value #>> '{}' END,
    value_number = CASE WHEN jsonb_typeof(value) = 'number' THEN (value #>> '{}')::numeric END,
    value_bool = CASE WHEN jsonb_typeof(value) = 'boolean' THEN (value #>> '{}')::boolean END;

ALTER TABLE base_task DROP COLUMN IF EXISTS variables;

-- A read-only view that reassembles a task's scope as the document the
-- column used to hold. Nothing needs it to run the app — the engine reads the
-- table — but an operator looking at a task in psql, and a test asserting on
-- a whole scope, both want the shape the engine evaluates. A view cannot be
-- written, so it cannot become a second source of truth.
CREATE OR REPLACE VIEW base_task_variables AS
WITH nested AS (
	SELECT task_id,
		jsonb_object_agg(name, value) FILTER (WHERE scope = 'input') AS inputs,
		jsonb_object_agg(name, value) FILTER (WHERE scope = 'local') AS locals,
		jsonb_object_agg(name, value) FILTER (WHERE scope = 'output') AS outputs,
		jsonb_object_agg(name, value) FILTER (WHERE scope = 'block') AS block
	FROM _task_variables
	GROUP BY task_id
), top AS (
	SELECT task_id, jsonb_object_agg(name, value) AS doc
	FROM _task_variables
	WHERE scope = 'top'
	GROUP BY task_id
)
SELECT t._id AS task_id,
	COALESCE(top.doc, '{}'::jsonb)
	|| jsonb_strip_nulls(jsonb_build_object(
		'inputs', nested.inputs,
		'locals', nested.locals,
		'outputs', nested.outputs,
		'block', nested.block)) AS variables
FROM base_task t
LEFT JOIN nested ON nested.task_id = t._id
LEFT JOIN top ON top.task_id = t._id;
