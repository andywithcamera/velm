# Velm LLM Authoring Guide

Use this file when generating Velm app definition YAML.

The goal is to emit YAML that:
- parses cleanly,
- matches the current runtime contract,
- uses the conventions this repo already enforces,
- avoids legacy or half-implemented features unless you are intentionally working on them.

This guide is based on the current parser and runtime behavior in:
- `internal/db/AppDefinition.go`
- `internal/db/MetadataGuards.go`
- `internal/db/Validation.go`
- `internal/db/ExpressionEval.go`
- `internal/db/ScriptRuntime.go`
- `internal/db/PageRuntime.go`

## Hard Rules

- Emit lower-case snake_case identifiers everywhere unless a field is clearly human-facing text.
- Do not emit top-level `scripts:`. That legacy shape is explicitly rejected.
- Prefer table-local `forms:` under `tables[].forms`. Top-level `forms:` are legacy and only kept for migration compatibility.
- Always use `call: service.method` for executable server logic.
- For reference columns, prefer:
  - `data_type: reference`
  - `reference_table: some_table`
- If `namespace: dw`, every normal table name must start with `dw_`.
- Quote expression strings, cron strings, and string default values.
- Write complete executable JavaScript with a `run(ctx)` function.
- Prefer real sections only. Do not add empty arrays unless needed for clarity.

## Fast Checklist

When composing YAML, make sure all of these are true:

- `name` is a safe identifier.
- `namespace` is a safe identifier and at most 8 characters.
- Every declared role is defined in top-level `roles`.
- Every table name uses the correct prefix.
- Every form field and list column exists on the table or is inherited.
- Every `call` uses `service.method`.
- Every dependency call targets a `public` method in the dependency app.
- Every `choice` column has `choices`.
- Every `reference` column has `reference_table`.
- Every `autnumber` column has a 3-4 letter uppercase `prefix`.
- Every enabled client script uses `language: javascript`.

## Canonical App YAML

Use this as the default starting point:

```yaml
name: devworks
namespace: dw
label: DevWorks
description: Delivery planning and execution app.
dependencies:
  - base

roles:
  - name: manager
    label: Manager
    description: Can manage delivery records.
  - name: contributor
    label: Contributor
    description: Can update assigned work.

tables:
  - name: dw_story
    extends: base_task
    extensible: false
    label_singular: Story
    label_plural: Stories
    description: Delivery story.
    display_field: number
    columns:
      - name: number
        label: Story Number
        data_type: autnumber
        is_nullable: false
        prefix: STR
      - name: title
        label: Title
        data_type: varchar(255)
        is_nullable: false
      - name: state
        label: State
        data_type: choice
        is_nullable: false
        default_value: "new"
        choices:
          - value: new
            label: New
          - value: in_progress
            label: In Progress
          - value: done
            label: Done
      - name: epic_id
        label: Epic
        data_type: reference
        reference_table: dw_epic
        is_nullable: true
      - name: parent_id
        label: Parent Story
        data_type: reference
        reference_table: dw_story
        is_nullable: true
      - name: story_points
        label: Story Points
        data_type: integer
        is_nullable: true
        validation_expr: "empty(value) || value >= 0"
        validation_message: Must be zero or greater.
      - name: ready_for_qa
        label: Ready For QA
        data_type: bool
        is_nullable: false
        default_value: "false"
      - name: ready_for_qa_at
        label: Ready For QA At
        data_type: timestamptz
        is_nullable: true

    forms:
      - name: default
        label: Default
        description: Main story editor.
        fields:
          - number
          - title
          - state
          - epic_id
          - story_points
          - ready_for_qa
        actions:
          - name: reopen
            label: Reopen
            call: story.reopen
            roles:
              - manager
        security:
          roles:
            - contributor

    lists:
      - name: default
        label: Default
        columns:
          - number
          - title
          - state
          - epic_id
          - story_points

    data_policies:
      - name: require_title
        label: Require Title
        description: Example policy metadata.
        condition: "!empty(title)"
        action: validate
        enabled: true

    triggers:
      - name: set_ready_timestamp
        label: Set Ready Timestamp
        description: Example record trigger.
        event: record.update
        condition: "ready_for_qa == true"
        call: story.set_ready_timestamp
        mode: sync
        order: 100
        enabled: true

    related_lists:
      - name: children
        label: Child Stories
        table: dw_story
        reference_field: parent_id
        columns:
          - number
          - title
          - state

    security:
      roles:
        - contributor
      notes: Table-level record security.
      rules:
        - name: contributor_read
          description: Contributors can read stories.
          order: 10
          effect: allow
          operation: R
          role: contributor
        - name: manager_update
          description: Managers can update any story.
          order: 20
          effect: allow
          operation: U
          role: manager

  - name: dw_epic
    label_singular: Epic
    label_plural: Epics
    description: Portfolio epic.
    columns:
      - name: name
        data_type: varchar(255)
        is_nullable: false

pages:
  - name: Launchpad
    slug: launchpad
    label: Launchpad
    description: App landing page.
    search_keywords: home, dashboard, launchpad
    editor_mode: html
    status: published
    content: |
      <section>
        <h1>DevWorks</h1>
        <p>Delivery workspace.</p>
      </section>
    actions:
      - name: refresh_board
        label: Refresh Board
        call: story.refresh_board
        roles:
          - contributor
    security:
      roles:
        - contributor

client_scripts:
  - name: default_story_state
    label: Default Story State
    description: Set a default state when a form opens.
    table: dw_story
    event: form.load
    language: javascript
    script: |
      function run(ctx) {
        if (!record.state) {
          record.state = "new";
        }
      }
    enabled: true

documentation:
  - name: story_workflow
    label: Story Workflow
    description: Workflow overview.
    category: Working With Stories
    visibility: internal
    content: |
      # Story Workflow

      Stories move from New to In Progress to Done.
    related:
      - dw_story
      - story.refresh_board

services:
  - name: story
    label: Story Service
    description: Story domain logic.
    methods:
      - name: refresh_board
        label: Refresh Board
        description: Example reusable method.
        visibility: public
        language: javascript
        roles:
          - contributor
        script: |
          async function run(ctx) {
            ctx.log("refresh_board", { app: app.id, user: user.id });
            return { ok: true };
          }

      - name: reopen
        label: Reopen
        description: Reopen a story.
        visibility: private
        language: javascript
        roles:
          - manager
        script: |
          async function run(ctx) {
            if (record) {
              record.state = "new";
            }
            return { ok: true };
          }

      - name: set_ready_timestamp
        label: Set Ready Timestamp
        description: Example trigger target.
        visibility: private
        language: javascript
        script: |
          function run(ctx) {
            if (record && record.ready_for_qa && !record.ready_for_qa_at) {
              record.ready_for_qa_at = ctx.now();
            }
            return record;
          }

endpoints:
  - name: board_data
    label: Board Data
    description: Return board payload.
    method: POST
    path: /api/devworks/board-data
    call: story.refresh_board
    roles:
      - contributor
    enabled: true

triggers:
  - name: normalize_story_state
    label: Normalize Story State
    description: App-level trigger example.
    table: dw_story
    event: record.update
    condition: "!empty(state)"
    call: story.refresh_board
    mode: sync
    order: 200
    enabled: false

schedules:
  - name: hourly_rollup
    label: Hourly Rollup
    description: Example schedule metadata.
    cron: "0 * * * *"
    call: story.refresh_board
    enabled: false

seeds:
  - table: dw_epic
    rows:
      - name: Platform Foundations
      - name: Delivery UX
```

## Top-Level Keys

The parser understands these top-level keys:

- `name`
- `namespace`
- `label`
- `description`
- `dependencies`
- `roles`
- `tables`
- `forms` (legacy; avoid in new YAML)
- `pages`
- `client_scripts`
- `seeds`
- `documentation`
- `services`
- `endpoints`
- `triggers`
- `schedules`

Do not use top-level `scripts`.

## Identifier Rules

Safe identifiers use this shape:

- start with a letter or underscore
- continue with letters, digits, or underscores

In practice:

- use `lowercase_snake_case`
- do not use spaces in identifiers
- do not use hyphens in identifiers unless the field is explicitly slug-like or human-facing text

Normalization behavior:

- app `name` and `namespace` are lowercased
- many loose identifiers are normalized by converting spaces and `-` to `_`
- endpoint methods are uppercased
- endpoint paths get a leading `/`
- page `slug` is lowercased

## App and Namespace Rules

- `namespace` is optional, but for ordinary apps it should be set.
- `namespace` must be 1-8 characters if present.
- Table names must use the namespace prefix:
  - `namespace: dw` -> `dw_story`
  - `namespace: sales` -> `sales_opportunity`
- Keep `name` descriptive and `namespace` short.
- In most apps, `name` can be the full app name and `namespace` the short prefix:
  - `name: devworks`
  - `namespace: dw`

## Table Rules

- Normal app tables must start with `<namespace>_`.
- Cross-app inheritance is allowed through `extends`, but the parent table must be marked `extensible: true` if it lives in another app.
- A table cannot redefine an inherited column.
- `display_field` must reference an existing local or inherited field.

Velm implicitly adds these system columns to ordinary YAML tables unless you explicitly declare them:

- `_id` (`uuid`)
- `_created_at` (`timestamptz`)
- `_updated_at` (`timestamptz`)
- `_update_count` (`bigint`)
- `_deleted_at` (`timestamptz`)
- `_created_by` (`reference` -> `_user`)
- `_updated_by` (`reference` -> `_user`)
- `_deleted_by` (`reference` -> `_user`)

Implications:

- You usually should not declare these columns yourself in normal app YAML.
- Forms, lists, and security rules may still reference them if useful.
- `display_field` auto-infers from these preferred names if present:
  - `name`
  - `title`
  - `number`
  - `email`
  - `label`
  - `key`
  - `slug`

## Column Rules

Supported data types from the validator:

- Text-like:
  - `text`
  - `varchar(255)` or other `varchar(n)`
  - `long_text`
  - `markdown`
  - `richtext`
  - `code`
  - `code:javascript`
- Numeric:
  - `int`
  - `integer`
  - `bigint`
  - `bigserial`
  - `serial`
  - `float`
  - `double`
  - `decimal`
  - `numeric`
- Boolean and temporal:
  - `bool`
  - `boolean`
  - `date`
  - `timestamp`
  - `timestamptz`
  - `datetime`
- Structured and IDs:
  - `uuid`
  - `json`
  - `jsonb`
- Relationship and selection:
  - `reference`
  - `choice`
  - `enum:a|b|c`
- Specialty:
  - `email`
  - `url`
  - `phone`
  - `autnumber`

Recommended patterns:

- Prefer `varchar(255)` for short text labels and names.
- Prefer `text` for unbounded plain text.
- Prefer `markdown` or `richtext` for authored content.
- Prefer `choice` if you want explicit labels.
- Use `enum:a|b|c` only for simple unlabeled fixed values.

Current authoring guidance:

- Use `data_type: reference` plus `reference_table: some_table`.
- Do not rely on `data_type: reference:some_table` even though the lower-level type normalizer understands it.

### Column Field Semantics

- `name`: required
- `label`: optional; auto-humanized if omitted
- `data_type`: optional; defaults to `text`
- `is_nullable`: boolean
- `default_value`: string, validated against the column type
- `prefix`: only valid for `autnumber`
- `validation_regex`: regex checked against submitted value
- `validation_expr`: boolean expression checked against submitted value and form data
- `condition_expr`: controls whether the field participates in form validation/display logic
- `validation_message`: custom message for failed `validation_expr`
- `reference_table`: required for `reference`
- `choices`: required for `choice`

### Data-Type-Specific Rules

- `reference`
  - requires `reference_table`
  - cannot use `choices`
- `choice`
  - requires non-empty `choices`
  - cannot use `reference_table`
- `autnumber`
  - requires `prefix`
  - `prefix` must be 3 or 4 uppercase letters
  - cannot use `default_value`
  - cannot use `reference_table`
  - cannot use `choices`
- `enum:a|b|c`
  - do not also supply `choices`

### Default Value Guidance

`default_value` is stored as a string field in the YAML struct, so prefer quoted values:

```yaml
default_value: "new"
default_value: "false"
default_value: "2026-01-01"
```

## Forms, Lists, and Related Lists

### Forms

Use `tables[].forms` for new definitions.

Form fields:

- `name`
- `label`
- `description`
- `fields`
- `actions`
- `security`

Notes:

- If a table has no forms, Velm generates a default form from all non-system columns.
- `fields` must reference real columns on the table, including inherited columns.
- `default` is the conventional form name.

### Lists

List fields:

- `name`
- `label`
- `columns`

Notes:

- If a table has no lists, Velm generates a default list from all non-system columns.
- `columns` must reference real columns on the table, including inherited columns.

### Related Lists

Related list fields:

- `name`
- `label`
- `table`
- `reference_field`
- `columns`

Rules:

- `table` must resolve to a local table or a dependency table.
- `reference_field` is required.

## Pages

Page fields:

- `name`
- `slug`
- `label`
- `description`
- `search_keywords`
- `editor_mode`
- `status`
- `content`
- `actions`
- `security`

Use these values:

- `editor_mode`: `wysiwyg` or `html`
- `status`: `draft` or `published`

Page slug rules:

- use simple lowercase slugs like `launchpad` or `story-board`
- do not namespace the slug yourself
- runtime qualification is handled internally

## Documentation

Documentation fields:

- `name`
- `label`
- `description`
- `category`
- `visibility`
- `content`
- `related`

Allowed `visibility` values:

- `internal`
- `external`
- `role-gated`

## Services and Methods

This is the main server-side execution model for app YAML.

Service fields:

- `name`
- `label`
- `description`
- `methods`

Method fields:

- `name`
- `label`
- `description`
- `visibility`
- `language`
- `roles`
- `script`

Rules:

- `visibility` must be `private` or `public`
- default `visibility` is `private`
- default `language` is `javascript`
- `roles` must reference top-level app roles
- a dependency app can only call `public` methods

Use `public` when:

- the method is called from another app via `dependencies`
- the method is intended to be called through the runtime service-call API

Use `private` when:

- the method is only used by local triggers, local endpoints, or internal actions

## Endpoints

Endpoint fields:

- `name`
- `label`
- `description`
- `method`
- `path`
- `call`
- `roles`
- `enabled`

Rules:

- use standard HTTP verbs: `GET`, `POST`, `PUT`, `PATCH`, `DELETE`
- `call` must use `service.method`
- enabled endpoints must declare `method`, `path`, and `call`
- dependency calls can only target `public` methods

Current runtime note:

- endpoints are served under authenticated `/api/*` routes in the current server
- if `roles` is empty, the endpoint has no app-role gate of its own

## Triggers

Trigger fields:

- `name`
- `label`
- `description`
- `event`
- `table`
- `condition`
- `call`
- `mode`
- `order`
- `enabled`

Rules:

- `call` must use `service.method`
- `mode` must be `sync` or `async`
- `table` is required for top-level triggers
- table-local triggers inherit the table name automatically

Current runtime behavior:

- triggers are sorted by ascending `order`, then by `name`
- both app-level and table-level triggers run
- current record-write execution paths actively invoke `record.insert` and `record.update`
- `mode` is validated and stored, but the current trigger execution path does not branch on it yet

## Schedules

Schedule fields:

- `name`
- `label`
- `description`
- `cron`
- `call`
- `enabled`

Rules:

- `cron` must be non-empty
- `call` must use `service.method`

Current runtime note:

- schedules are normalized and validated in app YAML
- this repo does not currently show a schedule runner executing them
- treat schedules as declared metadata unless you are also implementing the runner

## Client Scripts

Client script fields:

- `name`
- `label`
- `description`
- `table`
- `event`
- `field`
- `language`
- `script`
- `enabled`

Supported events:

- `form.load`
- `field.change`
- `field.input`

Rules:

- `language` must be `javascript`
- enabled client scripts must declare `table`, `event`, and `script`
- `field` is required for `field.change` and `field.input`

## Seeds

Seed fields:

- `table`
- `rows`

Notes:

- `rows` can use normal YAML scalars and objects
- row keys should match real column names
- seed values do not need to be quoted unless YAML would otherwise change the type or content

## Security

Security blocks can appear on:

- tables
- forms
- pages

Security shape:

```yaml
security:
  roles:
    - manager
  notes: Optional explanation.
  rules:
    - name: manager_update
      description: Managers can update any record.
      order: 20
      effect: allow
      operation: U
      table: dw_story
      field: state
      condition: "state != 'done'"
      role: manager
```

Rules:

- `roles` is a coarse role gate: user must have at least one listed role
- `rules` are evaluated in ascending `order`
- the first matching rule for the operation and field decides the result
- `operation` must normalize to one of:
  - `C`
  - `R`
  - `U`
  - `D`
- `effect` should be `allow` or `deny`
- `field` is useful for field-level `R` and `U`
- `field` is not allowed for delete rules
- `condition` uses the boolean expression language described below

Use short canonical operation codes in generated YAML:

- `C`
- `R`
- `U`
- `D`

Do not use runtime-scoped role names like `dw.manager` inside YAML. Use local role names like `manager`.

## Boolean Expression Language

The current expression engine is used for:

- column `validation_expr`
- column `condition_expr`
- security rule `condition`
- trigger `condition`

Supported operators:

- `==`
- `!=`
- `<`
- `<=`
- `>`
- `>=`
- `&&`
- `||`
- `!`

Supported literals:

- strings with `'single'` or `"double"` quotes
- numbers
- `true`
- `false`
- `empty`

Supported functions:

- `len(x)`
- `lower(x)`
- `upper(x)`
- `trim(x)`
- `empty(x)`
- `contains(a, b)`
- `startswith(a, b)`
- `endswith(a, b)`
- `now()`
- `today()`

Behavior notes:

- field names resolve directly by identifier
- inside `validation_expr`, `value` refers to the current field value

Examples:

```yaml
validation_expr: "empty(value) || len(value) <= 255"
condition_expr: "state != 'done'"
condition: "priority == 'high' && empty(assigned_user_id)"
condition: "startswith(number, 'STR-')"
```

## JavaScript Runtime Conventions

Server-side methods and browser client scripts should define `run(ctx)`.

Valid examples:

```javascript
function run(ctx) {
  return { ok: true };
}
```

```javascript
async function run(ctx) {
  ctx.log("hello", { app: app.id, user: user.id });
  return { ok: true };
}
```

Server-side globals available in the script runtime include:

- `ctx`
- `input`
- `payload`
- `record`
- `previousRecord`
- `app`
- `user`
- `trigger`
- `console`

Useful `ctx` helpers currently exposed:

- `ctx.log(message, data)`
- `ctx.now()`
- `ctx.services.call("service.method", input, payload)`
- `ctx.notifications.send({...})`

Current runtime requirement:

- server-side scripts must define `run(ctx)` or execution fails
- only `javascript` is supported

## Features To Treat Carefully

These shapes exist in the YAML model but should not be treated as strongly implemented runtime primitives unless you confirm the current code path:

- `data_policies`
  - normalized and stored
  - not clearly enforced by the current write path
- form and page `actions`
  - stored and role-validated
  - `call` targets are not schema-validated like service-trigger-endpoint calls
- `schedules`
  - validated and stored
  - no schedule runner is evident in the current repo
- trigger `mode`
  - validated and stored
  - current execution path does not change behavior based on `sync` vs `async`

If you need guaranteed execution, prefer:

- `services.methods`
- `endpoints`
- `triggers`
- direct runtime code changes

## Preferred Authoring Style

- Keep YAML minimal but complete.
- Put human-facing prose in `label` and `description`.
- Put executable logic in services.
- Keep table-specific forms, lists, security, and triggers under the table.
- Use dependency apps instead of cross-app duplication.
- Prefer one clean `default` form and one clean `default` list first.
- Prefer `choice` with explicit labels over opaque enums.
- Prefer app-local role names over platform-scoped role names in YAML.

## Avoid These Mistakes

- top-level `scripts:`
- unprefixed table names
- `choice` without `choices`
- `reference` without `reference_table`
- `autnumber` without a valid uppercase prefix
- referencing unknown roles in `roles`, `security`, endpoints, or methods
- calling dependency private methods
- using unsupported client script events
- depending on `data_policies` or `schedules` as if they are already full runtime features
