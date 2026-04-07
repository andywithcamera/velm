UPDATE _docs_library
SET name = 'Velm',
	description = 'Official Velm platform documentation and operational reference.',
	visibility = 'public',
	create_roles = COALESCE(create_roles, '[]'::jsonb),
	read_roles = COALESCE(read_roles, '[]'::jsonb),
	edit_roles = COALESCE(edit_roles, '[]'::jsonb),
	delete_roles = COALESCE(delete_roles, '[]'::jsonb),
	_updated_at = NOW()
WHERE app_name = 'velm'
  AND is_default = TRUE;

INSERT INTO _docs_library (
	slug,
	name,
	description,
	visibility,
	owner_user_id,
	status,
	app_name,
	is_default,
	create_roles,
	read_roles,
	edit_roles,
	delete_roles
)
SELECT
	'velm-docs',
	'Velm Docs',
	'Official Velm platform documentation and operational reference.',
	'public',
	'',
	'active',
	'',
	FALSE,
	'[]'::jsonb,
	'[]'::jsonb,
	'[]'::jsonb,
	'[]'::jsonb
WHERE NOT EXISTS (
	SELECT 1
	FROM _docs_library
	WHERE app_name = 'velm'
	  AND is_default = TRUE
)
AND NOT EXISTS (
	SELECT 1
	FROM _docs_library
	WHERE slug = 'velm-docs'
);

UPDATE _docs_library
SET name = 'Velm Docs',
	description = 'Official Velm platform documentation and operational reference.',
	visibility = 'public',
	create_roles = COALESCE(create_roles, '[]'::jsonb),
	read_roles = COALESCE(read_roles, '[]'::jsonb),
	edit_roles = COALESCE(edit_roles, '[]'::jsonb),
	delete_roles = COALESCE(delete_roles, '[]'::jsonb),
	_updated_at = NOW()
WHERE slug = 'velm-docs'
  AND app_name = '';

INSERT INTO _docs_library (
	slug,
	name,
	description,
	visibility,
	owner_user_id,
	status,
	app_name,
	is_default,
	create_roles,
	read_roles,
	edit_roles,
	delete_roles
)
SELECT
	'velm',
	'Velm',
	'Official Velm platform documentation and operational reference.',
	'public',
	'',
	'active',
	'velm',
	TRUE,
	'[]'::jsonb,
	'[]'::jsonb,
	'[]'::jsonb,
	'[]'::jsonb
WHERE EXISTS (
	SELECT 1
	FROM _app
	WHERE name = 'velm'
	  AND status = 'active'
)
AND NOT EXISTS (
	SELECT 1
	FROM _docs_library
	WHERE app_name = 'velm'
	  AND is_default = TRUE
);

WITH target_library AS (
	SELECT _id
	FROM (
		SELECT 1 AS priority, _id
		FROM _docs_library
		WHERE app_name = 'velm'
		  AND is_default = TRUE
		UNION ALL
		SELECT 2 AS priority, _id
		FROM _docs_library
		WHERE slug = 'velm-docs'
		  AND app_name = ''
	) candidates
	ORDER BY priority, _id
	LIMIT 1
),
seed_articles(slug, title, tags, markdown_body) AS (
	VALUES
		('velm-overview', 'Velm Overview', 'overview,platform,product', $$# Velm Overview
Velm is a low-code platform for defining apps, data models, pages, forms, security, and automation from one coherent workspace.
## What matters first
- Apps describe runtime behavior.
- Tables define persistent data.
- Pages, forms, lists, and scripts shape the working experience.
## How to approach the platform
Start by understanding the app editor, then move into tables, forms, and runtime security.$$),
		('getting-started', 'Getting Started In Velm', 'getting-started,setup,basics', $$# Getting Started In Velm
Your first useful loop in Velm is simple: open the app editor, inspect the base app, make a small schema or page change, publish, and verify it in runtime.
## Recommended first steps
- Review the Velm app and Base app definitions.
- Inspect a table, a form, and a page before editing.
- Publish deliberately so draft and runtime behavior stay understandable.$$),
		('platform-navigation', 'Platform Navigation', 'navigation,ui,search', $$# Platform Navigation
The main shell is designed around direct access to tables, forms, pages, tasks, admin tools, and docs.
## Core navigation patterns
- Use the global search to jump across records, tables, and tools.
- Use `/t/...` for table views, `/f/...` for forms, and `/p/...` for runtime pages.
- Use Docs when you need explanation before configuration.$$),
		('app-editor-overview', 'App Editor Overview', 'app-editor,apps,configuration', $$# App Editor Overview
The app editor is where Velm app definitions are structured and published. It exposes tables, forms, lists, scripts, pages, services, security, and other runtime objects as editable assets.
## Good working habits
- Keep table-specific configuration under the table it belongs to.
- Publish in small increments.
- Treat the tree as a runtime map, not just a file browser.$$),
		('app-definition-structure', 'App Definition Structure', 'app-editor,yaml,definition', $$# App Definition Structure
Each app definition holds the declarative shape of an app: metadata, tables, pages, scripts, services, endpoints, and security.
## Important principle
The definition is the source of truth. Runtime artifacts should reflect the definition, not drift away from it.
## Practical rule
If you are unsure where a behavior lives, look for the object that owns it in the app tree.$$),
		('tables-and-columns', 'Tables And Columns', 'tables,columns,data-model', $$# Tables And Columns
Tables represent entities and system records. Columns define the stored fields, their types, nullability, and references.
## Design guidance
- Model business concepts explicitly.
- Prefer clear display fields.
- Use references when relationships matter in forms, lists, and scripts.$$),
		('forms-and-fields', 'Forms And Fields', 'forms,fields,ui', $$# Forms And Fields
Forms are where record editing becomes real. A form decides field order, field visibility, field editability, and how a user experiences a record.
## Practical approach
- Keep the default form focused.
- Group related fields together.
- Put record-specific client behavior beside the form that owns it.$$),
		('lists-and-related-lists', 'Lists And Related Lists', 'lists,related-lists,tables', $$# Lists And Related Lists
Lists shape how records are scanned, filtered, and acted on. Related lists expose child or connected records inside another record context.
## When they matter
- Lists are for breadth.
- Forms are for depth.
- Related lists connect the two by surfacing context without leaving the record.$$),
		('client-scripts', 'Client Scripts', 'scripts,client,forms', $$# Client Scripts
Client scripts run in the browser and shape interactivity close to the form or page.
## Use them for
- Dynamic field behavior.
- Light validation and guided UX.
- Runtime polish that does not belong in data storage.
## Avoid
Do not rely on client scripts for final authorization or server truth.$$),
		('data-policies', 'Data Policies', 'data-policies,validation,governance', $$# Data Policies
Data policies describe field and record expectations that should hold consistently.
## Good uses
- Require important combinations of values.
- Guard state transitions.
- Keep records structurally sane before they spread across runtime features.$$),
		('triggers', 'Triggers', 'triggers,automation,server', $$# Triggers
Triggers let a table react before or after writes. They are the right place for server-side automation that must execute consistently.
## Strong use cases
- Derived field updates.
- Cross-record synchronization.
- Guardrails that must run even when writes happen outside a single form.$$),
		('security-rules', 'Security Rules', 'security,authorization,roles', $$# Security Rules
Velm security rules decide who can create, read, update, or delete records and fields under given conditions.
## Model shape
- Rules are ordered.
- Operations are explicit.
- Conditions and field scope can narrow a rule.
## Working advice
Keep the rule list small enough to reason about in order.$$),
		('publishing-and-versioning', 'Publishing And Versioning', 'publishing,versioning,release', $$# Publishing And Versioning
Velm keeps draft and published app definitions separate so changes can be prepared before release.
## Release discipline
- Build in draft.
- Verify with intention.
- Publish in coherent increments.
## Why it matters
Publishing is the moment your design becomes runtime behavior.$$),
		('pages-and-routes', 'Pages And Routes', 'pages,routes,runtime', $$# Pages And Routes
Pages render runtime experiences and can be attached to root routes or named page routes.
## Keep pages clear
- Use pages for flows and dashboards.
- Keep route ownership obvious.
- Treat page security as runtime access control, not decoration.$$),
		('schema-builder', 'Schema Builder', 'schema,builder,data', $$# Schema Builder
The schema builder handles physical data structure changes. It is lower level than the app editor and should be used deliberately.
## When to use it
- Creating or editing platform tables directly.
- Applying structural data changes.
- Working below the app-definition layer.$$),
		('scripting-overview', 'Scripting Overview', 'scripting,automation,services', $$# Scripting Overview
Velm scripting spans client scripts, server methods, endpoints, and registry-managed scripts.
## Choose the right execution layer
- Client scripts for browser behavior.
- Service methods for controlled server actions.
- Endpoints for request handling.
- Registry scripts for administration and operational work.$$),
		('services-and-endpoints', 'Service Methods And Endpoints', 'services,endpoints,api', $$# Service Methods And Endpoints
Service methods expose app-owned server logic, while endpoints define request-driven runtime behavior.
## Practical distinction
Use service methods for reusable domain operations. Use endpoints when the platform must answer a route or API call directly.$$),
		('script-registry', 'Script Registry And Test Runs', 'scripts,registry,testing', $$# Script Registry And Test Runs
The script registry is the place to catalog, dry-run, and operationalize scripts without losing track of what they do.
## Recommended workflow
- Name scripts clearly.
- Test with realistic payloads.
- Keep destructive operations explicit and reviewable.$$),
		('user-accounts', 'User Accounts', 'users,accounts,admin', $$# User Accounts
User records represent the people who can authenticate and operate inside Velm.
## Admin concerns
- Keep identities clean and unique.
- Separate authentication from authorization concerns.
- Bootstrap carefully, then manage long-term access through roles and groups.$$),
		('roles-and-permissions', 'Roles And Permissions', 'roles,permissions,security', $$# Roles And Permissions
Roles bundle access. Permissions define allowed actions. Together they determine what a user can do across the platform.
## Working rule
Keep role names meaningful and scoped. A role should imply a real operational responsibility, not just a technical shortcut.$$),
		('groups-and-inheritance', 'Groups And Role Inheritance', 'groups,roles,inheritance', $$# Groups And Role Inheritance
Groups let you assign access to many users at once, and role inheritance lets higher-trust roles include lower-trust capabilities.
## Why this matters
Without groups and inheritance, authorization becomes repetitive, brittle, and hard to audit.$$),
		('audit-and-metrics', 'Audit Log And Request Metrics', 'audit,metrics,monitoring', $$# Audit Log And Request Metrics
Velm records request activity, data changes, and timing information so teams can explain behavior and diagnose regressions.
## Use them for
- Operational troubleshooting.
- Compliance-oriented review.
- Performance investigation before guesswork takes over.$$),
		('task-board', 'Task Board', 'tasks,board,workflow', $$# Task Board
The task board is a workflow surface, not just a visualization. Board actions still need to respect the same write security as other runtime paths.
## Good practice
Treat drag-and-drop as a convenience on top of governed state changes.$$),
		('saved-views-and-exports', 'Saved Views And Exports', 'views,exports,lists', $$# Saved Views And Exports
Saved views let users keep useful filters and column layouts close at hand, while exports provide a controlled path to move data outward.
## Design guidance
- Save views for repeat work.
- Export intentionally.
- Keep sensitive columns aligned with read security expectations.$$),
		('notifications-and-realtime', 'Notifications And Realtime', 'notifications,realtime,collaboration', $$# Notifications And Realtime
Velm supports realtime updates and user notifications so records feel live rather than static.
## Important perspective
Realtime is not just visual polish. It changes how people coordinate, notice drift, and trust what they are seeing.$$),
		('migrations-and-seeds', 'Migrations And Seed Data', 'migrations,seed,operations', $$# Migrations And Seed Data
Migrations evolve schema and baseline data in a repeatable way. Seed content should be idempotent and safe to apply more than once.
## Operational rule
If a migration cannot run twice safely, it probably needs more care.$$),
		('bootstrapping-admin-access', 'Bootstrapping Admin Access', 'bootstrap,admin,users', $$# Bootstrapping Admin Access
Velm supports first-user bootstrapping so a new environment can become operable without manual database surgery.
## Keep it disciplined
- Use bootstrap only to establish initial control.
- Move to explicit user, role, and group management after setup.$$),
		('docs-catalog', 'Docs Catalog And Authoring', 'docs,knowledge,markdown', $$# Docs Catalog And Authoring
Docs are written in markdown, versioned over time, and surfaced through a catalog meant for readers first.
## Intent
- `/docs` should help people find answers quickly.
- `/docs/manage` should help editors maintain the corpus cleanly.
- Historical versions should remain available after changes.$$),
		('app-publishing-checklist', 'App Publishing Checklist', 'publishing,release,checklist', $$# App Publishing Checklist
Before publishing an app change, verify schema assumptions, forms, lists, security rules, scripts, and runtime pages together.
## Quick checklist
- Does the draft match the intended runtime behavior?
- Are security and scripts aligned?
- Is the user path still understandable after the change?$$),
		('runtime-search', 'Runtime Search And Discovery', 'search,discovery,ux', $$# Runtime Search And Discovery
Search is one of the fastest ways to recover context in a live platform. It should point users toward the record, tool, or doc that actually resolves their question.
## Product standard
Search should reduce wandering, not simply return more places to wander.$$),
		('table-security-troubleshooting', 'Troubleshooting Table Security', 'security,troubleshooting,tables', $$# Troubleshooting Table Security
When a write or read path behaves unexpectedly, check rule order, operation scope, field scope, and the user role set together.
## Debug sequence
- Confirm the effective roles.
- Confirm the matching rule order.
- Confirm whether the path is form, list, board, or service-driven.$$),
		('user-management-operations', 'User Management Operations', 'users,operations,admin', $$# User Management Operations
User management in Velm is more than editing a record. It includes role grants, group membership, inheritance effects, and safe bootstrap behavior.
## Practical approach
Prefer durable group-based access over one-off role grants wherever possible.$$),
		('platform-operations', 'Platform Operations', 'operations,admin,platform', $$# Platform Operations
Platform operations cover migrations, monitoring, bootstrap flows, auditing, script execution, and release hygiene.
## The underlying rule
Treat the platform itself as a product you operate, not just a place where your app happens to run.$$),
		('reference-lookup', 'Reference Fields And Lookups', 'reference,forms,data', $$# Reference Fields And Lookups
Reference fields connect records across tables and power lookup experiences in forms, lists, and automation.
## Design guidance
- Choose references when a real relationship exists.
- Keep labels clear so users can understand what a lookup result represents.$$),
		('pages-forms-and-data', 'Pages, Forms, And Data Together', 'pages,forms,data,architecture', $$# Pages, Forms, And Data Together
The strongest Velm designs keep pages, forms, and tables aligned. A page should reveal the process, a form should support the task, and the table should preserve the truth.
## When systems feel confusing
It is usually because one of those three layers is pulling in a different direction.$$)
),
inserted_articles AS (
	INSERT INTO _docs_article (
		library_id,
		slug,
		title,
		markdown_body,
		rendered_html,
		status,
		tags,
		version_num,
		published_at,
		owner_user_id,
		read_roles,
		edit_roles,
		delete_roles
	)
	SELECT
		target_library._id,
		seed_articles.slug,
		seed_articles.title,
		seed_articles.markdown_body,
		'',
		'published',
		seed_articles.tags,
		1,
		NOW(),
		'',
		'[]'::jsonb,
		'[]'::jsonb,
		'[]'::jsonb
	FROM seed_articles
	CROSS JOIN target_library
	ON CONFLICT (library_id, slug) DO NOTHING
	RETURNING _id, markdown_body, status
)
INSERT INTO _docs_article_version (
	article_id,
	version_num,
	markdown_body,
	rendered_html,
	status,
	created_by
)
SELECT
	inserted_articles._id,
	1,
	inserted_articles.markdown_body,
	'',
	inserted_articles.status,
	'system'
FROM inserted_articles
ON CONFLICT (article_id, version_num) DO NOTHING;
