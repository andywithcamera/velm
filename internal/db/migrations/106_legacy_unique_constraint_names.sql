-- 106: drop uniqueness by definition, not by the name we think it has (#373).
--
-- 076 created base_task_definition / base_task_definition_child as an app's
-- tables while the app was named task_definition, so Postgres named the
-- constraints after the *old* tables: uq_task_definition_child,
-- _task_definition_slug_key. 078 and 080 then renamed the tables to the base_
-- spelling; a renamed table keeps its constraint names.
--
-- 094 and 098 each drop the constraint they mean to remove under the base_
-- spelling. On any instance that took the 076 -> 080 path -- every real
-- instance, including production -- those names never existed, so the drops
-- were no-ops and the old rules stayed in force:
--
--   * a container could not name one definition from two blocks, which is the
--     whole point of a block tree (two approval gates, two "record the
--     rejection" steps). Saving failed with "duplicate key value violates
--     unique constraint uq_task_definition_child", naming a constraint that
--     no longer appears anywhere in the repo.
--   * a soft-deleted definition kept its slug reserved, so 094's "uniqueness
--     among live rows only" never took effect and deleting 'create-doc' still
--     blocked ever creating 'create-doc' again.
--
-- A from-zero database also never showed either bug: there 078 creates the
-- table with the base_ names, 094/098 drop them successfully, and the schema
-- is correct. The defect lived only in the path from an older instance, which
-- is why it survived a green test suite.
--
-- So this migration drops by *shape*, not by name: any unique constraint (or
-- bare unique index) over exactly the columns that must no longer be unique.
-- It is idempotent and harmless on a database that is already correct.

DO $$
DECLARE
	rec RECORD;
BEGIN
	-- 1. base_task_definition_child: a container may hold the same child
	--    definition in two blocks.
	IF to_regclass('public.base_task_definition_child') IS NOT NULL THEN
		FOR rec IN
			SELECT c.conname
			FROM pg_constraint c
			JOIN pg_class t ON t.oid = c.conrelid
			WHERE t.relname = 'base_task_definition_child'
				AND c.contype = 'u'
				AND (
					SELECT array_agg(a.attname::text ORDER BY a.attname)
					FROM pg_attribute a
					WHERE a.attrelid = c.conrelid AND a.attnum = ANY (c.conkey)
				) = ARRAY['child_definition_id', 'container_definition_id']
		LOOP
			EXECUTE format('ALTER TABLE base_task_definition_child DROP CONSTRAINT %I', rec.conname);
			RAISE NOTICE '106: dropped unique constraint %.%', 'base_task_definition_child', rec.conname;
		END LOOP;

		-- A unique index that backs no constraint has to be dropped directly,
		-- or the same rule survives without a constraint to drop.
		FOR rec IN
			SELECT i.indexrelid::regclass::text AS index_name
			FROM pg_index i
			JOIN pg_class t ON t.oid = i.indrelid
			WHERE t.relname = 'base_task_definition_child'
				AND i.indisunique
				AND NOT EXISTS (SELECT 1 FROM pg_constraint c WHERE c.conindid = i.indexrelid)
				AND (
					SELECT array_agg(a.attname::text ORDER BY a.attname)
					FROM pg_attribute a
					WHERE a.attrelid = i.indrelid AND a.attnum = ANY (i.indkey)
				) = ARRAY['child_definition_id', 'container_definition_id']
		LOOP
			EXECUTE format('DROP INDEX IF EXISTS %s', rec.index_name);
			RAISE NOTICE '106: dropped unique index %', rec.index_name;
		END LOOP;
	end if;

	-- 2. base_task_definition: slug is unique among live rows only, which is
	--    what 094's partial index uq_base_task_definition_slug_live enforces.
	IF to_regclass('public.base_task_definition') IS NOT NULL THEN
		FOR rec IN
			SELECT c.conname
			FROM pg_constraint c
			JOIN pg_class t ON t.oid = c.conrelid
			WHERE t.relname = 'base_task_definition'
				AND c.contype = 'u'
				AND (
					SELECT array_agg(a.attname::text ORDER BY a.attname)
					FROM pg_attribute a
					WHERE a.attrelid = c.conrelid AND a.attnum = ANY (c.conkey)
				) = ARRAY['slug']
		LOOP
			EXECUTE format('ALTER TABLE base_task_definition DROP CONSTRAINT %I', rec.conname);
			RAISE NOTICE '106: dropped unique constraint %.%', 'base_task_definition', rec.conname;
		END LOOP;

		-- A bare unique index over slug carries the same rule. 094's
		-- uq_base_task_definition_slug_live is partial (WHERE _deleted_at IS
		-- NULL) and must survive, so only non-partial indexes are matched.
		FOR rec IN
			SELECT i.indexrelid::regclass::text AS index_name
			FROM pg_index i
			JOIN pg_class t ON t.oid = i.indrelid
			WHERE t.relname = 'base_task_definition'
				AND i.indisunique
				AND i.indpred IS NULL
				AND NOT EXISTS (SELECT 1 FROM pg_constraint c WHERE c.conindid = i.indexrelid)
				AND (
					SELECT array_agg(a.attname::text ORDER BY a.attname)
					FROM pg_attribute a
					WHERE a.attrelid = i.indrelid AND a.attnum = ANY (i.indkey)
				) = ARRAY['slug']
		LOOP
			EXECUTE format('DROP INDEX IF EXISTS %s', rec.index_name);
			RAISE NOTICE '106: dropped unique index %', rec.index_name;
		END LOOP;
	end if;
END $$;
