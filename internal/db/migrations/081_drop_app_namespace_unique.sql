-- Converge every database on "several apps may share a namespace".
--
-- Namespaces are table-name prefixes ('base' -> base_task), and more than one
-- app legitimately contributes tables to the same prefix: 047 seeds 'base',
-- 076 seeds 'task_definition' (both namespace 'base'). 024 declared
-- `namespace TEXT NOT NULL UNIQUE`, so a fresh database could not apply 076 —
-- it failed with a duplicate key on _app_namespace_key, which means no fresh
-- install could ever finish migrating.
--
-- Databases bootstrapped before 024 shipped never got that constraint, because
-- 024's CREATE TABLE is IF NOT EXISTS. This migration drops it where it exists
-- so old and new installs end up with the same schema.
ALTER TABLE _app DROP CONSTRAINT IF EXISTS _app_namespace_key;

-- The name and namespace uniqueness that actually matters is per app name;
-- namespace-level uniqueness was never enforced in application code.
