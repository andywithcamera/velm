-- Fixes startup failure on existing installs:
--   "fatal: failed to initialize database: error running migrations:
--    app task_definition published definition: table "_task_definition"
--    must use the "base"_ prefix."
-- 076 seeded the task_definition app with namespace=base but named its
-- tables _task_definition / _task_definition_child, which violates the
-- base-namespace prefix rule enforced at init. Rename the tables to
-- base_task_definition / base_task_definition_child so they conform.
-- (FKs referencing them repoint automatically on RENAME.)

ALTER TABLE IF EXISTS _task_definition RENAME TO base_task_definition;
ALTER TABLE IF EXISTS _task_definition_child RENAME TO base_task_definition_child;

-- Replace any seeded task_definition app rows with a conforming definition.
UPDATE _app
SET definition_yaml = replace(definition_yaml, '_task_definition_child', 'base_task_definition_child'),
    published_definition_yaml = replace(published_definition_yaml, '_task_definition_child', 'base_task_definition_child')
WHERE name = 'task_definition';
UPDATE _app
SET definition_yaml = replace(definition_yaml, '_task_definition', 'base_task_definition'),
    published_definition_yaml = replace(published_definition_yaml, '_task_definition', 'base_task_definition')
WHERE name = 'task_definition';
