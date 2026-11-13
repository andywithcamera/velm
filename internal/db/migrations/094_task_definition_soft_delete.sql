-- base_task_definition opted into soft delete but never got the _deleted_by
-- column every other soft-deletable base table has. handleSoftDeleteRecord
-- builds one UPDATE for all of them:
--
--   SET _deleted_at = NOW(), _deleted_by = NULLIF($1,'')::uuid, _updated_at = NOW()
--
-- so on this table the statement referenced a column that does not exist and
-- every delete returned 500 "Failed to delete record" (#301). Entities, tasks
-- and relationships deleted fine; definitions alone could not be removed at
-- all, through any surface.
ALTER TABLE base_task_definition
	ADD COLUMN IF NOT EXISTS _deleted_by UUID;

-- The slug constraint was plain UNIQUE (076), so a soft-deleted definition
-- kept its slug reserved forever: deleting 'create-doc' still blocked ever
-- creating 'create-doc' again. Uniqueness should hold among live rows only.
ALTER TABLE base_task_definition
	DROP CONSTRAINT IF EXISTS base_task_definition_slug_key;

DROP INDEX IF EXISTS uq_base_task_definition_slug_live;
CREATE UNIQUE INDEX IF NOT EXISTS uq_base_task_definition_slug_live
	ON base_task_definition(slug) WHERE _deleted_at IS NULL;
