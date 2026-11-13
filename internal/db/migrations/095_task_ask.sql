-- The ask: what a task is actually asking of its assignee (#225).
-- A card used to show a subject and a state, never the verb, so readers had
-- to guess between "design this", "approve this" and "do this".
--
-- base_task.ask is the ask on a concrete task. NOT NULL DEFAULT 'validate'
-- makes existing rows valid with no backfill and gives the intake path a
-- safe default for tasks created without one.
-- base_task.definition.ask records what the definition asks of its doer, so
-- instances spawned from it inherit the verb instead of re-deriving it.

ALTER TABLE base_task
	ADD COLUMN IF NOT EXISTS ask TEXT NOT NULL DEFAULT 'validate';

-- Narrower than description: description stays the brief, this is the one
-- line of "why now" that the brief does not carry.
ALTER TABLE base_task
	ADD COLUMN IF NOT EXISTS context_note TEXT;

ALTER TABLE base_task
	DROP CONSTRAINT IF EXISTS chk_base_task_ask;
ALTER TABLE base_task
	ADD CONSTRAINT chk_base_task_ask CHECK (ask IN (
		'validate', 'requirement', 'approval', 'design', 'code',
		'review', 'test', 'deploy', 'decide', 'other'));

ALTER TABLE base_task_definition
	ADD COLUMN IF NOT EXISTS ask TEXT NOT NULL DEFAULT '';

ALTER TABLE base_task_definition
	DROP CONSTRAINT IF EXISTS chk_base_task_definition_ask;
ALTER TABLE base_task_definition
	ADD CONSTRAINT chk_base_task_definition_ask CHECK (ask = '' OR ask IN (
		'validate', 'requirement', 'approval', 'design', 'code',
		'review', 'test', 'deploy', 'decide', 'other'));

-- Definitions carry no producer/gate flag, only a slug; derive the verb from
-- slug prefixes in the style of 086.
UPDATE base_task_definition SET ask = CASE
	WHEN slug ~ '^intake' THEN 'validate'
	WHEN slug ~ '^(write_requirement|requirement)' THEN 'requirement'
	WHEN slug ~ '^(write_design|design)' THEN 'design'
	WHEN slug ~ '^(gate_code|review)' THEN 'review'
	WHEN slug ~ '^gate' THEN 'approval'
	WHEN slug ~ '^approval' THEN 'approval'
	WHEN slug ~ '^(run_tests|test|qa)' THEN 'test'
	WHEN slug ~ '^(deployment|deploy|release|rollback|hotfix)' THEN 'deploy'
	WHEN slug ~ '^(coding|code|build|refactor)' THEN 'code'
	ELSE ''
END
WHERE ask = '';

-- Instances already spawned from a definition take its ask.
UPDATE base_task t SET ask = d.ask
FROM base_task_definition d
WHERE d._id = t.definition_id AND d.ask <> '';
