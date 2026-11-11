-- Backfill instance fields the spawn path now seeds (2026-11-11).
--
-- Evidence from the live instance: of 45 definition-spawned tasks, none had
-- a description and two carried "inputs": null because the catalog start
-- passed a nil map. New spawns are fixed in TaskEngine; this brings the
-- existing rows to the same shape so the forms read consistently.
--
-- Guarded twice: it only touches rows whose description is still empty (a
-- rewritten description is never overwritten) and rows whose inputs are
-- JSON null (an empty {} is already correct). Re-running affects 0 rows.

UPDATE base_task t
SET description = BTRIM(d.description)
FROM base_task_definition d
WHERE d._id = t.definition_id
  AND t.definition_id IS NOT NULL
  AND t._deleted_at IS NULL
  AND (t.description IS NULL OR BTRIM(t.description) = '')
  AND d.description IS NOT NULL
  AND BTRIM(d.description) <> '';

UPDATE base_task
SET variables = jsonb_set(COALESCE(variables, '{}'::jsonb), '{inputs}', '{}'::jsonb, true)
WHERE variables IS NOT NULL
  AND variables ? 'inputs'
  AND variables->'inputs' = 'null'::jsonb;
