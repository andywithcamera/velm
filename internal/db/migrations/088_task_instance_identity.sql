-- Task instance identity (#272).
--
-- base_task.title was set to the definition's display_name at spawn, so
-- every run of a definition carried an identical title: 45 of 116 tasks
-- collided, including two 22-task "Feature Request" pipelines whose stage
-- names were identical to each other's. The board then flattened children
-- onto the root title, so a single pipeline rendered 28 cards reading
-- "Feature Request".
--
-- Spawn now composes the title (root: "Name (TASK-n)", child:
-- "Root · Stage (TASK-n)"). This backfill brings existing instances in line.
-- It only touches rows whose title still equals their definition's display
-- name, so a hand-edited title is never overwritten, and it is idempotent:
-- a composed title no longer matches the guard on a second run.

WITH RECURSIVE chain AS (
	SELECT
		t._id,
		t.parent_task_id,
		t.definition_id,
		t.title,
		t.number,
		t._id AS descendant_id,
		0 AS depth
	FROM base_task t
	WHERE t._deleted_at IS NULL
	UNION ALL
	SELECT
		parent._id,
		parent.parent_task_id,
		parent.definition_id,
		parent.title,
		parent.number,
		chain.descendant_id,
		chain.depth + 1
	FROM base_task parent
	JOIN chain ON parent._id = chain.parent_task_id
	WHERE parent._deleted_at IS NULL AND chain.depth < 32
),
roots AS (
	SELECT DISTINCT ON (descendant_id)
		descendant_id,
		COALESCE(NULLIF(BTRIM(d.display_name), ''), chain.title) AS root_name
	FROM chain
	LEFT JOIN base_task_definition d ON d._id = chain.definition_id
	WHERE chain.parent_task_id IS NULL
	ORDER BY descendant_id, chain.depth DESC
)
UPDATE base_task task
SET title = CASE
		WHEN task.parent_task_id IS NULL THEN roots.root_name
		ELSE roots.root_name || ' · ' || definition.display_name
	END || ' (' || task.number || ')'
FROM roots, base_task_definition definition
WHERE definition._id = task.definition_id
	AND task._id = roots.descendant_id
	AND task.definition_id IS NOT NULL
	AND task.number IS NOT NULL
	AND BTRIM(task.number) <> ''
	AND BTRIM(task.title) = BTRIM(definition.display_name)
	AND task._deleted_at IS NULL;
