-- 090: make the Feature Request chain run in order, land on a named person,
-- and survive a rejection.
--
-- Measured before this: the two live Feature Request runs had 22 tasks each,
-- every one still pending, because (a) no stage declared a dependency so all
-- seven stages would have opened at once, (b) nested containers never opened
-- their children at all, (c) 42 of 45 spawned tasks carried no assignee and
-- 085 refuses in_progress without one, and (d) no gate could reject back to
-- its author, so a rejection ended the pipeline.
--
-- (b) and (c) and (d)'s engine half are in code. This migration is the data:
-- routing, sequencing, and the revision loop.

-- ---------------------------------------------------------------------------
-- 1. A definition can name the person, not just the queue.
-- ---------------------------------------------------------------------------
ALTER TABLE base_task_definition
	ADD COLUMN IF NOT EXISTS default_assignee_id UUID REFERENCES _user(_id) ON DELETE SET NULL;

-- The QA queue had no machine member, so nothing in it could be assigned to
-- the machine that actually runs the suite.
INSERT INTO _group_membership (group_id, user_id)
SELECT g._id, u._id::text
FROM _group g, _user u
WHERE g.name = 'QA'
  AND u.email = 'billybot@andydoyle.ie'
  AND NOT EXISTS (
	SELECT 1 FROM _group_membership m WHERE m.group_id = g._id AND m.user_id = u._id::text
  );

-- ---------------------------------------------------------------------------
-- 2. Routing: producers to the machine, gates to the human.
--
-- A gatekeeper who also drafts the artefact is not a gate. Group descriptions
-- already say so ("Business Approval ... never a producer, never an agent");
-- the requirement writer was pointed at Product, and the design review sat in
-- Design where the person who wrote the design also sits.
-- ---------------------------------------------------------------------------
UPDATE base_task_definition d SET default_group_id = g._id
FROM _group g
WHERE g.name = 'Development' AND d.slug = 'write_requirement';

UPDATE base_task_definition d SET default_group_id = g._id
FROM _group g
WHERE g.name = 'Code Review' AND d.slug IN ('gate_design', 'gate_code');

UPDATE base_task_definition d SET default_assignee_id = u._id
FROM _user u
WHERE u.email = 'billybot@andydoyle.ie'
  AND d.slug IN ('intake_validation', 'write_requirement', 'write_design', 'coding',
                 'run_tests', 'deployment_scheduling', 'deployment');

UPDATE base_task_definition d SET default_assignee_id = u._id
FROM _user u
WHERE u.email = 'hi@andydoyle.ie'
  AND d.slug IN ('gate_business', 'gate_design', 'gate_code', 'gate_test',
                 'approval_in_principle');

-- ---------------------------------------------------------------------------
-- 3. Sequencing: stages run in order.
--
-- depends_on names SIBLING DEFINITIONS in the same container, and 084's
-- promotion waits for the dependency instance to close. Empty everywhere
-- before this, so the seven stages of delivery_full had no order at all.
-- ---------------------------------------------------------------------------
DO $$
DECLARE
	pairs CONSTANT TEXT[][] := ARRAY[
		['st_principle',  'st_intake'],
		['st_requirement','st_principle'],
		['st_design',     'st_requirement'],
		['st_build',      'st_design'],
		['st_test',       'st_build'],
		['st_release',    'st_test']
	];
	i INT;
	child_def UUID;
	dep_def UUID;
BEGIN
	FOR i IN 1 .. array_length(pairs, 1) LOOP
		SELECT _id INTO child_def FROM base_task_definition WHERE slug = pairs[i][1] AND _deleted_at IS NULL;
		SELECT _id INTO dep_def   FROM base_task_definition WHERE slug = pairs[i][2] AND _deleted_at IS NULL;
		IF child_def IS NULL OR dep_def IS NULL THEN
			CONTINUE;
		END IF;
		UPDATE base_task_definition_child
		SET depends_on = ARRAY[dep_def]
		WHERE container_definition_id = (SELECT _id FROM base_task_definition WHERE slug = 'delivery_full')
		  AND child_definition_id = child_def;
	END LOOP;
END $$;

-- The deploy waits for the approved window: the release stage schedules,
-- a human approves, and only then does the deploy task open.
UPDATE base_task_definition_child c
SET depends_on = ARRAY[(SELECT _id FROM base_task_definition WHERE slug = 'gate_business')]
WHERE c.child_definition_id = (SELECT _id FROM base_task_definition WHERE slug = 'deployment')
  AND c.container_definition_id = (SELECT _id FROM base_task_definition WHERE slug = 'st_release')
  AND EXISTS (SELECT 1 FROM base_task_definition WHERE slug = 'gate_business' AND _deleted_at IS NULL);

-- ---------------------------------------------------------------------------
-- 4. Gates are spawned by their producer, not declared beside it.
--
-- A static gate could only ever be judged once: the instance closes and
-- there is no second one to receive the re-draft. Spawned from the
-- producer's closure, each revision gets its own gate, and the stage closes
-- when the last one does.
-- ---------------------------------------------------------------------------
DELETE FROM base_task_definition_child c
USING base_task_definition parent, base_task_definition child
WHERE c.container_definition_id = parent._id
  AND c.child_definition_id = child._id
  AND parent.slug IN ('st_requirement', 'st_design', 'st_build', 'st_test', 'st_release')
  AND child.slug IN ('gate_business', 'gate_design', 'gate_code', 'gate_test');

-- Producer finished -> the gate opens for the human. producer_slug is
-- recorded on the gate via $task, which is what lets one shared gate
-- definition reject back to whichever producer spawned it.
UPDATE base_task_definition SET spawn_rules = $json$[
  {"on": "completed", "definition_slug": "gate_business", "assign": "definition",
   "input_mapping": {
     "subject_task_id": "$task.id",
     "producer_slug": "$task.definition_slug",
     "artifact_id": "$outputs.requirement_artifact_id",
     "revision": "$outputs.requirement_version",
     "criteria": "Requirement is unambiguous, testable, and scoped. Approval releases design."}}
]$json$::jsonb
WHERE slug = 'write_requirement';

UPDATE base_task_definition SET spawn_rules = $json$[
  {"on": "completed", "definition_slug": "gate_design", "assign": "definition",
   "input_mapping": {
     "subject_task_id": "$task.id",
     "producer_slug": "$task.definition_slug",
     "artifact_id": "$outputs.design_artifact_id",
     "revision": "$outputs.design_version",
     "criteria": "Design satisfies the approved requirement and names its risks."}}
]$json$::jsonb
WHERE slug = 'write_design';

UPDATE base_task_definition SET spawn_rules = $json$[
  {"on": "completed", "definition_slug": "gate_code", "assign": "definition",
   "input_mapping": {
     "subject_task_id": "$task.id",
     "producer_slug": "$task.definition_slug",
     "artifact_id": "$outputs.patch_artifact_id",
     "commit_sha": "$outputs.commit_sha",
     "branch": "$outputs.branch",
     "criteria": "Correctness, tests, security. Approval authorises the merge."}}
]$json$::jsonb
WHERE slug = 'coding';

UPDATE base_task_definition SET spawn_rules = $json$[
  {"on": "completed", "definition_slug": "gate_test", "assign": "definition",
   "input_mapping": {
     "subject_task_id": "$task.id",
     "producer_slug": "$task.definition_slug",
     "artifact_id": "$outputs.test_artifact_id",
     "passed": "$outputs.passed",
     "failures": "$outputs.failures",
     "criteria": "Tests pass at the commit under test; failures explained."}}
]$json$::jsonb
WHERE slug = 'run_tests';

UPDATE base_task_definition SET spawn_rules = $json$[
  {"on": "completed", "definition_slug": "gate_business", "assign": "definition",
   "input_mapping": {
     "subject_task_id": "$task.id",
     "producer_slug": "$task.definition_slug",
     "artifact_id": "$outputs.window_start",
     "window_start": "$outputs.window_start",
     "backout_plan": "$outputs.backout_plan",
     "criteria": "Window, backout plan and change record accepted."}}
]$json$::jsonb
WHERE slug = 'deployment_scheduling';

-- Any gate that rejects returns the work to whoever produced it, with the
-- verdict's comment as feedback. One rule, four gates, no per-stage config.
UPDATE base_task_definition SET spawn_rules = $json$[
  {"on": "rejected", "definition_slug": "$inputs.producer_slug", "assign": "definition",
   "input_mapping": {
     "feedback": "$outputs.comment",
     "revision": "$inputs.revision"}}
]$json$::jsonb
WHERE slug IN ('gate_business', 'gate_design', 'gate_code', 'gate_test');

-- ---------------------------------------------------------------------------
-- 5. Scope fixes in the input mappings.
--
-- Four edges prefixed a reference with the container's own slug while being
-- RESOLVED inside that container, where the namespace is the child's:
-- inside st_requirement the scope is st_requirement.variables, so
-- $outputs.st_requirement.write_requirement.x looked for a key that never
-- exists. They resolved to nothing, silently, because an unresolvable ref is
-- skipped rather than fatal.
-- ---------------------------------------------------------------------------
DO $$
DECLARE
	fixes CONSTANT TEXT[][] := ARRAY[
		['delivery_full','st_principle',  'route',                   '$outputs.st_intake.route'],
		['delivery_full','st_requirement','route',                   '$outputs.st_intake.route'],
		['delivery_full','st_design',     'route',                   '$outputs.st_intake.route'],
		['delivery_full','st_design',     'requirement_artifact_id', '$outputs.st_requirement.requirement_artifact_id'],
		['delivery_full','st_build',      'design_artifact_id',      '$outputs.st_design.design_artifact_id'],
		['delivery_full','st_test',       'commit_sha',              '$outputs.st_build.commit_sha'],
		['delivery_full','st_release',    'commit_sha',              '$outputs.st_build.commit_sha']
	];
	i INT;
BEGIN
	FOR i IN 1 .. array_length(fixes, 1) LOOP
		UPDATE base_task_definition_child c
		SET input_mapping = c.input_mapping || jsonb_build_object(fixes[i][3], fixes[i][4])
		WHERE c.container_definition_id = (SELECT _id FROM base_task_definition WHERE slug = fixes[i][1])
		  AND c.child_definition_id = (SELECT _id FROM base_task_definition WHERE slug = fixes[i][2]);
	END LOOP;
END $$;
