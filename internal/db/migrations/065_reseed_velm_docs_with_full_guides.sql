DELETE FROM _docs_article_version;
DELETE FROM _docs_article;

SELECT setval('docs_article_doc_number_seq', 1, FALSE);

UPDATE _docs_library
SET name = 'Velm Docs',
	description = 'Guides and reference material for building, operating, and extending Velm.',
	visibility = 'public',
	owner_user_id = '',
	status = 'active',
	app_name = '',
	is_default = FALSE,
	create_roles = COALESCE(create_roles, '[]'::jsonb),
	read_roles = COALESCE(read_roles, '[]'::jsonb),
	edit_roles = COALESCE(edit_roles, '[]'::jsonb),
	delete_roles = COALESCE(delete_roles, '[]'::jsonb),
	_updated_at = NOW()
WHERE slug = 'velm-docs';

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
	'Guides and reference material for building, operating, and extending Velm.',
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
	WHERE slug = 'velm-docs'
);

WITH target_library AS (
	SELECT _id
	FROM _docs_library
	WHERE slug = 'velm-docs'
	ORDER BY _id
	LIMIT 1
),
seed_articles(sort_order, slug, title, tags, markdown_body) AS (
	VALUES
		(1, 'welcome-to-velm', 'Welcome to Velm', 'overview,platform,getting-started', $$# Welcome to Velm
Velm is a platform for designing software as an operating system, not a pile of disconnected admin pages. Apps, data tables, forms, runtime pages, permissions, scripts, tasks, docs, entities, and maps all live in one environment so the system can be understood and changed as a whole.
## What Velm is for
Velm is strongest when you need a system that can be shaped continuously by the team operating it. Instead of pushing data schema in one tool, UI in another, and authorization in a third, Velm keeps those concerns close together so the runtime stays explainable.
## The platform mental model
- Apps define behavior and structure.
- Tables hold durable truth.
- Forms and lists shape operational work.
- Pages present guided runtime experiences.
- Security rules decide who can act.
- Scripts and services automate what should happen consistently.
- Docs explain why the system is shaped the way it is.
- Tasks, entities, and maps help people operate with context instead of guesswork.
## Your first hour in a new workspace
Start with [App Editor](/docs?library=velm-docs&article=app-editor) to see how an app is assembled. Open [Pages, Forms, and Lists](/docs?library=velm-docs&article=pages-forms-and-lists) so the runtime surface makes sense. Then review [Tasks](/docs?library=velm-docs&article=tasks), [Entities](/docs?library=velm-docs&article=entities), and [Maps](/docs?library=velm-docs&article=maps) because they show how work, configuration items, and relationships are actually represented.
## How to learn the platform efficiently
Do not start by clicking every menu item. Pick one business flow, trace the records behind it, find the form and list that expose those records, then look for the app definition that owns the behavior. That path teaches the shape of the system faster than browsing features in isolation.
## Practical habits that pay off
- Change one layer at a time, but always verify the user path end to end.
- Keep names concrete. Ambiguous labels spread confusion into lists, references, search, and docs.
- Prefer small publishable increments over large invisible rewrites.
- Write docs as soon as a pattern becomes important enough that someone else will repeat it.
## When Velm feels confusing
Confusion usually means the layers have drifted apart. The table says one thing, the form says another, and the page implies a third. When that happens, step back and realign the data model, the workflow surface, and the authorization rules around a single operational story.
## Where to go next
Read [App Editor](/docs?library=velm-docs&article=app-editor) for the authoring model, [Automation and Security](/docs?library=velm-docs&article=automation-and-security) for guarded behavior, and [Publishing and Operations](/docs?library=velm-docs&article=publishing-and-operations) for release discipline.$$),
		(2, 'app-editor', 'App Editor', 'app-editor,apps,authoring', $$# App Editor
The App Editor is the main authoring surface for Velm. It is where an app definition becomes concrete: tables, forms, lists, pages, services, client scripts, security rules, and other runtime objects are edited together instead of being scattered across separate tools.
## What the App Editor owns
An app definition is the declarative source of truth for the application layer. When you want to know why the runtime behaves the way it does, the App Editor is the first place to inspect. It tells you what objects exist, how they are grouped, and which configuration belongs to which capability.
## The tree is a runtime map
Treat the left-hand tree as a model of ownership, not just storage. If a list belongs to a table, keep it under that table. If a page depends on a service method, make sure both are named so the relationship is obvious. The editor becomes dramatically easier to use when the tree mirrors how the runtime is meant to behave.
## Draft and published state
Velm separates draft changes from published behavior so the team can prepare work without immediately changing the live runtime. This is a feature, not overhead. It gives you room to inspect related objects before release and keeps operational changes from being half-finished in production.
## A good editing workflow
- Open the app and identify the object that actually owns the change.
- Read the current configuration before editing. Existing structure usually explains hidden assumptions.
- Make the smallest coherent change that can be verified.
- Check affected tables, forms, lists, pages, and permissions before publishing.
- Publish only when the end-to-end user path is understandable.
## Where engineers get into trouble
Most App Editor mistakes come from local edits with global consequences. A column is renamed without checking forms. A page is updated without reviewing the service it calls. A permission rule is added without understanding rule order. The fix is not more ceremony; it is better ownership. Change the object that truly owns the behavior and verify the surfaces that depend on it.
## Naming and structure guidance
Use labels that describe purpose, not implementation trivia. Prefer names like “Incident Intake Form” over “Default Form 2”. Prefer “Resolve Task” over “Action Runner”. The better the names are in the editor, the easier it becomes to search, review, document, and troubleshoot the runtime.
## When to use another tool instead
If you need to make a low-level physical change to database structure, the [Publishing and Operations](/docs?library=velm-docs&article=publishing-and-operations) workflow and schema-oriented tools may be more appropriate. The App Editor should remain the home for app behavior, not an excuse to bypass operational discipline.
## Related reading
Pair this guide with [Pages, Forms, and Lists](/docs?library=velm-docs&article=pages-forms-and-lists), [Automation and Security](/docs?library=velm-docs&article=automation-and-security), and [Tasks](/docs?library=velm-docs&article=tasks) when you want to connect authoring decisions to runtime behavior.$$),
		(3, 'tasks', 'Tasks', 'tasks,workflow,operations', $$# Tasks
Tasks are the standard unit of tracked work in Velm. They are not just cards on a board. A task is a governed record with identity, state, ownership, timestamps, and workflow expectations that need to stay consistent whether the work is updated from a form, list, board, automation, or script.
## What a task record represents
A task should represent an actionable unit of work with a meaningful outcome. That sounds obvious, but teams often overload tasks with vague reminders, status notes, or bundles of unrelated work. A task stays useful when it has one responsible path from creation to closure.
## Task identity and numbering
Each task gets a stable number so it can be referenced in conversation, notifications, audit trails, and search. Use that number as the durable human handle and keep the title focused on the actual work to be done.
## State and ownership
State should reflect operational truth, not aspiration. If nobody can act because a dependency is missing, the task is blocked. If work has started, the timestamps and assignment model should tell that story clearly. Assignment should also respect group membership and role boundaries, otherwise the board becomes a misleading picture of who is responsible.
## The main task surfaces
- The task board is best for scanning flow and bottlenecks.
- The list view is best for filtering, queue management, and bulk review.
- The form view is best for understanding one task deeply and updating it safely.
Each surface is useful, but they should all describe the same underlying record truth.
## Good task writing
Titles should make the work legible without opening the record. “Provision VPN access for contractor” is better than “Access request”. Descriptions should capture enough context to hand the work to another operator without requiring chat archaeology.
## Designing a reliable workflow
Keep the state model small enough that people can reason about it. Too many states create the illusion of precision while hiding uncertainty. Define what each state means operationally, then enforce important transitions in automation or policy rather than relying on memory.
## Common failure modes
- Tasks are too broad, so no one knows when they are actually done.
- Assignment does not reflect the real working group.
- Boards are treated as decorative UI and drift away from record truth.
- Status changes happen without timestamps or resolution criteria.
## How tasks connect to the rest of Velm
Tasks often refer to users, groups, entities, configuration items, or customer records. Those references matter. They are what let a task pull context into forms, related lists, search results, and notifications. If your work cannot connect to the surrounding system, the task model is probably too shallow.
## Related reading
Use [Entities](/docs?library=velm-docs&article=entities) when tasks need relationship context, [Maps](/docs?library=velm-docs&article=maps) when the network of dependencies matters, and [User Administration](/docs?library=velm-docs&article=user-administration) when ownership or access rules are involved.$$),
		(4, 'docs', 'Docs', 'docs,authoring,knowledge-management', $$# Docs
Velm Docs are part of the product, not an afterthought. A doc should help a reader understand a capability, make a safe change, or resolve a problem without wandering through unrelated screens first.
## What the docs system is for
The docs system gives the team a searchable, permission-aware place to store operational guidance, product explanation, and implementation rules. Because docs live inside the same platform as the runtime, they can be searched alongside tasks and tools, and they can link directly to the routes and concepts people need while they are working.
## Reader experience and authoring experience
Velm separates the reader path from the authoring path. Readers should land in a focused article view. Editors should work in `/docs/manage`, where libraries, markdown, version history, and permissions are maintained. Mixing those experiences usually makes both worse.
## Stable identity
Every document gets a durable doc number and a canonical `/d/<id>` route. Use the number when you need a short reference in chat, tickets, or review comments. Use the stable route when you need a bookmark that survives future title or slug changes.
## Search and backlinks
Docs should be discoverable from the global search when the current user can read them. That keeps documentation in the same recovery path as records and tools. Backlinks matter because they expose where an idea is reused. If three guides point to the same decision, that decision is probably operationally important.
## How to write good Velm docs
- Keep one main topic per page.
- Start with what the reader is trying to do or understand.
- Use headings that can be scanned quickly.
- Prefer short sections over one long wall of text.
- Use descriptive link text so cross-references still make sense out of context.
- Keep procedural advice concrete and tied to real platform behavior.
## A practical article structure
Good docs in Velm usually follow the same shape: explain what the feature is for, describe the main model, walk through the common workflow, call out failure modes, and finish with related docs. That structure makes the corpus easier to search and easier to maintain.
## What not to publish
Do not publish notes that only make sense to the author. Avoid placeholders, unexplained acronyms, and private implementation shorthand. If a document cannot help someone else act with less risk, it is not ready.
## When to add or update a doc
Update docs whenever a change affects user workflow, data shape, security expectations, or operational recovery. The best time to document a pattern is when it is still fresh enough that the tradeoffs are obvious.
## Related reading
Read [Welcome to Velm](/docs?library=velm-docs&article=welcome-to-velm) for the platform frame, [App Editor](/docs?library=velm-docs&article=app-editor) for authoring context, and [Publishing and Operations](/docs?library=velm-docs&article=publishing-and-operations) for rollout discipline.$$),
		(5, 'entities', 'Entities', 'entities,relationships,data-model', $$# Entities
Entities give Velm a stable way to describe things that matter across the platform. An entity might represent an application, service, device, location, team-owned system, or another durable object that other records need to point at.
## Why entities exist
If tasks, incidents, approvals, and docs all refer to the same thing but each flow stores that thing differently, the system fragments quickly. Entities solve that by creating a shared identity and relationship layer that other parts of the platform can reuse.
## What belongs in an entity
An entity should represent a real object with lasting meaning. If the item is only a temporary workflow step, a task or transactional record is probably a better fit. If the thing needs to be referenced from multiple tables, show up in relationship views, or carry operational context over time, it is often an entity.
## Modeling guidance
- Use clear names that make sense in search and reference pickers.
- Keep entity types intentional. Types should help people reason, not just categorize for the sake of it.
- Add relationships when they explain how systems depend on one another.
- Avoid duplicate entities that differ only in spelling or local team jargon.
## Entities and references
Entity references are valuable because they let the same object appear consistently in forms, lists, automation, and reporting. If a reference picker gives poor results, the underlying entity naming and labeling usually need attention first.
## Relationship quality matters
An entity graph is only useful when the edges mean something. “Depends on”, “runs on”, “owned by”, and “located in” all tell different operational stories. Relationship labels should be chosen so someone reading a map or a related list can understand the system without translation.
## Where teams go wrong
The most common mistake is trying to turn entities into a junk drawer for everything important. An entity model should stay opinionated. If you cannot explain why something needs a durable cross-platform identity, it probably does not belong here.
## How entities support operations
Entities become more valuable over time. They help [Tasks](/docs?library=velm-docs&article=tasks) pull in context, they give [Maps](/docs?library=velm-docs&article=maps) meaningful structure, and they let automation reason about the same object from multiple angles. That makes incident response, change planning, and ownership review substantially easier.
## Maintenance discipline
Assign ownership for entity quality. Without ownership, labels decay, relationships drift, and the graph slowly becomes decorative instead of operational. Review duplicates, stale records, and relationship semantics as part of normal platform hygiene.
## Related reading
Continue with [Maps](/docs?library=velm-docs&article=maps) to see how entities are visualized and [Pages, Forms, and Lists](/docs?library=velm-docs&article=pages-forms-and-lists) for how entity references appear in runtime surfaces.$$),
		(6, 'maps', 'Maps', 'maps,relationships,visualization', $$# Maps
The map view turns entity relationships into a navigable visual model. It is most useful when an operator needs to understand blast radius, dependency shape, or ownership context faster than a flat list can provide.
## What the map is good at
Maps help answer structural questions. What depends on this service? What sits upstream of this platform component? Which related entities are likely to be affected by a change or outage? When those questions matter, a relationship map can reduce a lot of list-hopping.
## What the map is not for
A map is not a substitute for clean data. If entities are poorly named, relationships are vague, or the graph is over-modeled, the map will faithfully render confusion. Fix the model first, then rely on the visualization.
## Reading the map
Start from the selected root entity and expand outward by relationship depth. Near nodes usually describe direct context. Further nodes suggest dependency chains, ownership surfaces, or broader blast radius. Use the map to form a hypothesis, then confirm important details in the relevant record form.
## Working with depth and scope
More depth is not always better. Deep graphs are harder to interpret and easier to over-read. Begin with the minimum relationship depth that explains the operational question. Expand only when the current view is not enough.
## Common use cases
- Incident triage when you need to understand affected systems quickly.
- Change review when you want to identify obvious downstream impact.
- Service ownership work when a team needs to clean up its dependency picture.
- Architecture discussions where a visual relationship model is easier to inspect than raw rows.
## Troubleshooting a poor map
- If the map is empty, verify the root entity exists and has relationships.
- If the map is noisy, check for relationship types that are too broad or duplicated.
- If labels are hard to read, improve entity names rather than relying on viewers to memorize IDs.
- If operators do not trust the picture, review stale or one-way relationships.
## How maps connect to the rest of Velm
Maps depend on [Entities](/docs?library=velm-docs&article=entities). They become more useful when tasks, changes, incidents, and docs refer back to the same entities because the operator can move from the visual model into the record workflow without losing context.
## Related reading
Read [Entities](/docs?library=velm-docs&article=entities) before designing relationship-heavy models and [Tasks](/docs?library=velm-docs&article=tasks) when you want to connect the graph back to actual operational work.$$),
		(7, 'user-administration', 'User Administration', 'users,roles,groups,security', $$# User Administration
User administration in Velm is not just account maintenance. It is the combined discipline of identity, role assignment, group structure, permission scope, bootstrap safety, and auditability.
## The core objects
- Users represent people who can authenticate.
- Groups organize people for operational ownership.
- Roles bundle responsibilities.
- Permissions define allowed actions.
The point is not to create many objects. The point is to make access explainable.
## Bootstrap and first access
Velm supports bootstrap admin setup so a new environment can become operable quickly. Use that capability only to establish initial control. After that, move to durable administration through normal user, group, and role management so access can be reviewed and understood later.
## A healthy access model
Group-based assignment usually ages better than direct one-off grants. If several people need the same access, encode that through a group and a role pattern instead of editing each user independently. That keeps onboarding, offboarding, and access reviews manageable.
## Least privilege in practice
Least privilege is not about making everything hard. It is about giving people enough access to do their work without turning the entire platform into a shared admin console. Separate builders, operators, reviewers, and administrators where those distinctions matter.
## What to review regularly
- Dormant users that should be disabled or archived.
- Groups that no longer reflect real teams.
- Roles that have become too broad to describe clearly.
- Permission assignments that nobody can justify.
## Common mistakes
The worst administration pattern is accumulation. Temporary grants become permanent, emergency roles are never removed, and new staff inherit whatever the last person happened to have. The fix is routine review and explicit ownership, not a bigger spreadsheet.
## How user administration affects the rest of the platform
Access design shapes [Tasks](/docs?library=velm-docs&article=tasks), [Automation and Security](/docs?library=velm-docs&article=automation-and-security), and even [Docs](/docs?library=velm-docs&article=docs) because the same identity model determines who can see, change, approve, or publish work.
## Operational guidance
When in doubt, prefer access patterns that can be explained in a sentence. If nobody can articulate why a role exists, it probably needs to be split, renamed, or removed.
## Related reading
Read [Automation and Security](/docs?library=velm-docs&article=automation-and-security) for rule design and [Publishing and Operations](/docs?library=velm-docs&article=publishing-and-operations) for audit and release responsibilities.$$),
		(8, 'pages-forms-and-lists', 'Pages, Forms, and Lists', 'pages,forms,lists,runtime', $$# Pages, Forms, and Lists
Pages, forms, and lists are the main surfaces that turn data and configuration into a usable runtime. They should feel like one coherent system, not three separate implementations.
## Pages
Pages are where guided experiences live. Use them when the user needs a flow, a dashboard, a workspace, or a purpose-built runtime journey. A page should answer the question “what is this screen for?” immediately.
## Forms
Forms are for depth. They are where a user reads one record carefully, edits it safely, reviews related records, and understands field-level context. The order of fields, the grouping of information, and the visibility rules all matter because the form teaches the record model.
## Lists
Lists are for breadth. They help users scan queues, filter work, compare records, and act across many items quickly. A good list exposes the columns that support a decision, not every field that happens to exist on the table.
## Keeping the three aligned
The strongest runtime experiences come from alignment:
- The page explains the process.
- The list helps users find the right records inside that process.
- The form helps them understand and update one record correctly.
When one of those layers tells a different story, the user feels the inconsistency immediately.
## Related lists and context
Related lists are where runtime context becomes powerful. They let a user stay inside one record while seeing the records that depend on it, refer to it, or are governed by it. That context is often what turns a form from a data editor into an operational workspace.
## Design mistakes to avoid
- A page that duplicates what a form already does with less clarity.
- A list with too many columns to support scanning.
- A form that reflects database order instead of operator workflow.
- Related lists that exist but do not help a real decision.
## Practical review questions
When you open a runtime surface, ask four things. What is the user trying to do here? What record truth supports that task? What context is missing? What permissions or automation affect the outcome? If those answers are not obvious, the surface probably needs redesign.
## Related reading
Use [App Editor](/docs?library=velm-docs&article=app-editor) to find the objects that own these surfaces and [Entities](/docs?library=velm-docs&article=entities) when reference context needs to be richer.$$),
		(9, 'automation-and-security', 'Automation and Security', 'automation,security,scripts,services', $$# Automation and Security
Automation and security should be designed together because the same question sits underneath both: what should happen, and who is allowed to make it happen?
## The execution layers
Velm gives you several places to put logic:
- Client scripts shape browser behavior close to the form or page.
- Service methods hold reusable server-side domain actions.
- Endpoints answer explicit request paths.
- Triggers enforce behavior around writes.
- Security rules decide whether reads and writes are allowed at all.
The quality of the system depends on choosing the right layer for each responsibility.
## Put logic where truth belongs
Validation that must always hold should not live only in client behavior. Authorization should not depend on what the browser decided to hide. Reusable business operations should not be copied into several page handlers. Put logic at the layer that can guarantee the outcome you care about.
## Security rules as product behavior
Security rules are not paperwork. They are part of the product. They define who can see and modify data, and therefore they shape the runtime just as much as a form or page does. Rule order, condition scope, and field scope should be understandable enough that someone else can predict the result.
## Common architecture pattern
Use the client for guidance, the server for truth, and security rules for authority. That split keeps the runtime responsive without making it fragile.
## Failure modes to watch
- Business logic duplicated across scripts and triggers.
- Validation only in the client.
- Roles so broad that security rules become meaningless.
- Services that do too much because object ownership was never defined.
## Operational review
Whenever you add automation, ask what records it reads, what records it writes, what permissions it relies on, and how an operator will diagnose failure. If those answers are vague, the automation is not ready.
## How this connects to administration
[User Administration](/docs?library=velm-docs&article=user-administration) defines the identities and roles that your rules rely on. [Publishing and Operations](/docs?library=velm-docs&article=publishing-and-operations) defines how those changes reach the live runtime safely.
## Related reading
Read [App Editor](/docs?library=velm-docs&article=app-editor) for object ownership, [Tasks](/docs?library=velm-docs&article=tasks) for workflow implications, and [User Administration](/docs?library=velm-docs&article=user-administration) for access design.$$),
		(10, 'publishing-and-operations', 'Publishing and Operations', 'publishing,operations,migrations,release', $$# Publishing and Operations
Publishing and operations are where design intent meets production reality. Velm gives you a lot of power to shape the system quickly, but that power only stays useful when releases, schema changes, monitoring, and rollback thinking are handled with discipline.
## Draft versus live behavior
Velm separates draft and published state so teams can prepare coherent changes before they affect users. Treat that separation as a safety rail. Review related objects together, verify important workflows, and publish only when the runtime story is clear.
## Migrations and seed data
Schema migrations should be repeatable, ordered, and understandable. Seed data should be intentional and idempotent. If a migration cannot be explained or rerun safely, it is already carrying operational risk.
## A release mindset
- Publish in increments small enough to reason about.
- Verify the user path, not just the config diff.
- Check forms, lists, pages, automation, and security together when a change crosses layers.
- Document the reason for a significant operational choice while it is still fresh.
## Monitoring and audit
Request metrics and audit records are part of routine platform operation, not just incident response tools. They help explain what happened, when it happened, and whether the runtime is drifting away from expectations.
## When to stop and rethink
If a change requires many hidden assumptions, touches unrelated areas, or cannot be validated by the team operating it, stop and reduce the scope. Large fuzzy releases create more cleanup work than they save.
## Operational ownership
Someone should own the release path for meaningful changes. Ownership does not mean one person presses every button. It means someone can explain the intent, the risk, the verification plan, and the fallback plan.
## Where docs fit
[Docs](/docs?library=velm-docs&article=docs) are part of operations because they reduce recovery time and prevent repeated mistakes. Good documentation is one of the cheapest ways to make a powerful platform safer to change.
## Related reading
Pair this guide with [Welcome to Velm](/docs?library=velm-docs&article=welcome-to-velm) for the high-level model, [Automation and Security](/docs?library=velm-docs&article=automation-and-security) for guarded behavior, and [App Editor](/docs?library=velm-docs&article=app-editor) for the authoring workflow.$$)
),
inserted_articles AS (
	INSERT INTO _docs_article (
		docs_library_id,
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
	ORDER BY seed_articles.sort_order
	RETURNING _id, markdown_body, status
)
INSERT INTO _docs_article_version (
	docs_article_id,
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
FROM inserted_articles;
