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

-- The rewrite is anchored on "the character before the underscore is not a
-- letter", so an occurrence that already reads base_task_definition is left
-- alone. A plain replace(definition_yaml, '_task_definition',
-- 'base_task_definition') also matches inside base_task_definition and yields
-- basebase_task_definition, which fails the prefix check with a message that
-- names a table nobody wrote.
UPDATE _app
SET definition_yaml = regexp_replace(
        regexp_replace(definition_yaml,
            '(^|[^a-zA-Z0-9])_task_definition_child', '\1base_task_definition_child', 'g'),
        '(^|[^a-zA-Z0-9])_task_definition', '\1base_task_definition', 'g'),
    published_definition_yaml = regexp_replace(
        regexp_replace(published_definition_yaml,
            '(^|[^a-zA-Z0-9])_task_definition_child', '\1base_task_definition_child', 'g'),
        '(^|[^a-zA-Z0-9])_task_definition', '\1base_task_definition', 'g')
WHERE name = 'task_definition'
  AND (definition_yaml LIKE '%_task_definition%' OR published_definition_yaml LIKE '%_task_definition%');

-- Repair any database that already ran the unanchored version of this
-- migration: basebase_task_definition cannot be produced deliberately, so
-- collapsing it is always correct.
UPDATE _app
SET definition_yaml = replace(definition_yaml, 'basebase_', 'base_'),
    published_definition_yaml = replace(published_definition_yaml, 'basebase_', 'base_')
WHERE definition_yaml LIKE '%basebase_%' OR published_definition_yaml LIKE '%basebase_%';
