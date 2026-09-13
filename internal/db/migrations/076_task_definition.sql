-- Task composition (issue #112, design: Task Composition Model.md).
--
-- Ruling: NO step engine. Tasks compose out of tasks; a container IS a task.
-- Definitions are business data (editable in the catalog app); YAML only
-- seeds/versions them. Composition over extension: incident / change request /
-- order-a-tshirt are definitions, not table extensions.
--
-- Variables: single JSONB column on base_task. Scoping enforced in the engine
-- (child reads parent scope + own locals; publishes declared outputs on close).
-- History comes free from _audit_data_change (009, field-level JSONB diff).
--
-- Scripts (task script and run_if condition) are admin-only ACL. Script tasks
-- must be assigned to an Automation user (user_type from 075); the Automation
-- principal is the audit principal. Script work happens on in_progress, not spawn.
--
-- parent_task_id already exists on base_task (047) — reused, not recreated.

CREATE TABLE IF NOT EXISTS base_task_definition (
	_id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
	slug TEXT NOT NULL UNIQUE,          -- 'write-doc-draft', 'reboot-server'
	display_name TEXT NOT NULL,
	description TEXT NOT NULL DEFAULT '',
	is_container BOOLEAN NOT NULL DEFAULT FALSE,  -- composes children, or is a leaf
	-- Input schema: 042-style YAML field list the requester fills.
	input_schema_yaml TEXT NOT NULL DEFAULT '',
	-- Variable declarations: YAML with sections inputs/locals/outputs.
	-- inputs are bound at spawn from the parent scope; outputs publish up on close.
	variables_yaml TEXT NOT NULL DEFAULT '',
	-- Optional admin-only Goja script run when the task enters in_progress.
	-- Runs as the assigned Automation user; output published to parent scope.
	script_text TEXT NOT NULL DEFAULT '',
	-- Optional admin-only, pure Goja condition over the parent scope.
	-- false -> task transitions pending -> skipped, publishes nothing.
	run_if_script TEXT NOT NULL DEFAULT '',
	-- Default routing group; copied onto the instance's assignment_group_id.
	-- Group, never member — routing policy stays a separate future layer.
	default_group_id UUID REFERENCES _group(_id) ON DELETE SET NULL,
	-- Spawn rules per closure reason. Shape (JSONB):
	-- [{ "on": "approved", "definition_slug": "publish-doc",
	--    "input_mapping": {"doc_id": "$outputs.doc_id"},
	--    "assign": "same" | "pool" }]
	spawn_rules JSONB NOT NULL DEFAULT '[]'::jsonb,
	is_active BOOLEAN NOT NULL DEFAULT TRUE,
	definition_metadata JSONB NOT NULL DEFAULT '{}'::jsonb,
	_created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
	_updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_base_task_definition_active
	ON base_task_definition(is_active);

-- Instance side: variable scope lives on the task row.
ALTER TABLE base_task
	ADD COLUMN IF NOT EXISTS variables JSONB NOT NULL DEFAULT '{}'::jsonb;

-- NOTE: an earlier revision of this migration added a CHECK on a
-- nonexistent base_task.state column. The real state model is state_id +
-- base_task_state rows + base_task_transition (047); 'skipped' is added
-- there by 077_fix_task_state_model.sql.

-- Catalog app for definitions. Definitions are business data: admins edit
-- these rows through the normal record forms; YAML is only the seed channel.
-- Script fields are admin-only ACL (enforced in app role config).
INSERT INTO _app (name, namespace, label, description, definition_yaml, published_definition_yaml, definition_version, published_version)
SELECT
	'task_definition', 'base', 'Task Definitions',
	'Composable task definitions: inputs, variables, scripts, run_if conditions, spawn rules.',
	trim($yaml$
name: task_definition
namespace: base
label: Task Definitions
description: Composable task definitions. Container tasks hold children; leaf tasks do work.
dependencies:
  - base
tables:
  - name: base_task_definition
    label_singular: Task Definition
    label_plural: Task Definitions
    display_field: display_name
    columns:
      - name: slug
        label: Slug
        data_type: text
        is_nullable: false
      - name: display_name
        label: Display Name
        data_type: text
        is_nullable: false
      - name: description
        label: Description
        data_type: long_text
        is_nullable: true
      - name: is_container
        label: Container
        data_type: boolean
        is_nullable: false
        default_value: false
      - name: input_schema_yaml
        label: Input Schema
        data_type: long_text
        is_nullable: true
      - name: variables_yaml
        label: Variables (inputs / locals / outputs)
        data_type: long_text
        is_nullable: true
      - name: script_text
        label: Script (admin only)
        data_type: long_text
        is_nullable: true
        acl:
          write_roles: [admin]
      - name: run_if_script
        label: Run If Condition (admin only)
        data_type: long_text
        is_nullable: true
        acl:
          write_roles: [admin]
      - name: default_group_id
        label: Default Group
        data_type: reference
        is_nullable: true
        reference_table: "_group"
      - name: spawn_rules
        label: Spawn Rules
        data_type: json
        is_nullable: true
      - name: is_active
        label: Active
        data_type: boolean
        is_nullable: false
        default_value: true
    list_columns:
      - slug
      - display_name
      - is_container
      - is_active
      - _updated_at
    forms:
      - name: default
        fields:
          - slug
          - display_name
          - description
          - is_container
          - input_schema_yaml
          - variables_yaml
          - script_text
          - run_if_script
          - default_group_id
          - spawn_rules
          - is_active
$yaml$),
	trim($yaml$
name: task_definition
namespace: base
label: Task Definitions
description: Composable task definitions. Container tasks hold children; leaf tasks do work.
dependencies:
  - base
tables:
  - name: base_task_definition
    label_singular: Task Definition
    label_plural: Task Definitions
    display_field: display_name
    columns:
      - name: slug
        label: Slug
        data_type: text
        is_nullable: false
      - name: display_name
        label: Display Name
        data_type: text
        is_nullable: false
      - name: description
        label: Description
        data_type: long_text
        is_nullable: true
      - name: is_container
        label: Container
        data_type: boolean
        is_nullable: false
        default_value: false
      - name: input_schema_yaml
        label: Input Schema
        data_type: long_text
        is_nullable: true
      - name: variables_yaml
        label: Variables (inputs / locals / outputs)
        data_type: long_text
        is_nullable: true
      - name: script_text
        label: Script (admin only)
        data_type: long_text
        is_nullable: true
        acl:
          write_roles: [admin]
      - name: run_if_script
        label: Run If Condition (admin only)
        data_type: long_text
        is_nullable: true
        acl:
          write_roles: [admin]
      - name: default_group_id
        label: Default Group
        data_type: reference
        is_nullable: true
        reference_table: "_group"
      - name: spawn_rules
        label: Spawn Rules
        data_type: json
        is_nullable: true
      - name: is_active
        label: Active
        data_type: boolean
        is_nullable: false
        default_value: true
    list_columns:
      - slug
      - display_name
      - is_container
      - is_active
      - _updated_at
    forms:
      - name: default
        fields:
          - slug
          - display_name
          - description
          - is_container
          - input_schema_yaml
          - variables_yaml
          - script_text
          - run_if_script
          - default_group_id
          - spawn_rules
          - is_active
$yaml$),
	1, 1
ON CONFLICT (name) DO NOTHING;
