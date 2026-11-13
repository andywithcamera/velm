# Changelog

All notable changes to Velm, newest first.

Format follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/); versioning is semver, pre-1.0 (see `AGENTS.md`). Entries are grouped `Added`, `Changed`, `Fixed`, `Removed`, and each names the issue it came from. Unreleased work sits under `[Unreleased]` until the next tag.

## [Unreleased]

### Added

- `GET /version` reports `schema`, the highest applied migration. A deploy that half-migrated, or a database restored from an older backup, is now visible from outside instead of discovered at request time. (#161, #175)
- `GET /version` reports `release`, the tag `git describe` resolves. Production now stamps it: `scripts/describe-release.sh` resolves the tag from the repository when the build context has one, and from the GitHub API when it does not — Railway's builder strips `.git`, so a production deploy names its release (`v0.1.0-13-gabc1234`) instead of reporting `unknown`. (#160, #164, #178)

### Changed

- Derived observability no longer runs on the request path. `emitDerivedObservabilityBestEffort` called the base app's `observability.ingest_event` service inline, so every recorded request waited for a goja run — measured at 1.8s locally, of which 78ms resolved the base app and 2s was the script timing out. Emits are now queued to one background worker that coalesces up to 32 jobs per pass, drops rather than blocks when the queue is full, and carries one metadata cache per batch. A failed event no longer aborts the rest of its batch. Health checks, `/version` and `/robots.txt` are no longer recorded at all. (#189)
- Script model operations keep the request metadata cache. `scriptViewForTable`, `ParseRecordIDValue` and `buildScriptWhereClause` resolved table views through `context.Background()`, so the cache the request had already built was invisible to them: one request issued 61 identical `FROM _app` queries, each pulling 2×239KB of YAML. Measured 61 → 1. (#189)
- `MFASecretCipher` generalised to `security.SecretCipher`: the AES-256-GCM string cipher was named for its first consumer, and the AI connection keys (#93), webhooks (#91) and OAuth (#88) would have inherited that name for non-MFA secrets. (#102, #174)
- CI runs `staticcheck` and `gosec`, a route × role matrix, and integration tests against a real Postgres instead of skipping them. (#141, #172)

### Removed

- Direct user-role grants. `_user_role` let one user hold a role without being in any group; it was the second grant path, and the two disagreed: the admin count, the last-admin guard and eleven query sites treated it differently. Roles now come from group membership only, so there is one path and one place to look when a user has the wrong access. Migration 083 folds every existing direct grant into a group named after it (`Administrators (<role>)`, idempotent by name) and then drops the table, and the system app sync no longer recreates it from the definition 042 seeded. The `/admin/access-control/user-roles` endpoints and the page section that listed them are gone. (#199)
- The GitHub Pages workflow (`.github/workflows/static.yml`). It was the stock starter template, unmodified: it archived the entire repository root and published it at `andywithcamera.github.io/velm` — which has no `index.html`, so it served a 404. It has never published anything anyone reads. It now also fails intermittently in `actions/deploy-pages` (`Found 0 artifact(s)` immediately after a successful upload), and the two commits whose Pages check failed are the two commits Railway did not deploy. Removing the workflow removes a check that can fail for reasons unrelated to Velm. (#182)

### Fixed

- Saving an existing task no longer fails with `unsupported script value type time.Time`. `HandleSave` re-reads every non-underscore column of the row through the before-write trigger path, and `state_changed_at` arrives from Postgres as a `time.Time` that `scriptAnyToString` had no case for. Because 051's trigger stamps that column on insert, the failure applied to every task row: closing, reassigning or retitling a task from the UI returned a 400, while MCP's `complete_task` still worked, so the engine looked healthy. Timestamps are now formatted per column type through the same helper the snapshot comparison uses, which keeps an unchanged timestamp equal to the snapshot so it is skipped rather than written back over the trigger's value. Applies to any table with a non-underscore `timestamptz` column. (#314)

- A user's password can be set from the record editor, and `password_hash` is no longer a textarea. The column was `long_text` in the `_user` form, so whatever an operator typed was written verbatim and login (bcrypt) then rejected it, silently and forever. `password_hash` is now absent from every generated form and list, replaced by a write-only `password` field that is bcrypt-hashed on save, never round-trips to the browser, and is refused as a raw column write by the save, bulk-update and security-preview paths. A `_user` record whose stored value is missing or not a bcrypt hash now says so on the record instead of only failing at the login screen, and changing a password signs that user out of their other sessions. (#194)

- Clicking a tab in the app editor no longer lands on `/`. `maintain-base-url.js`, loaded on every page, pushed `/` onto history on load and again on every `popstate`, so the browser believed every page was the root; the tab strip built its links from that address bar and resolved each one against `/`. The script is gone, and the editor now builds its links from the path the server rendered, which also makes them correct from `/builder/pages`. (#30)
- `TestAgentTokenLifecycle` is re-runnable against a database it has already used. It created a fixed token hash, so the second run died on `_agent_api_token_token_hash_key`; the seeded `_user` row was also never removed. Each run now derives its own token and email and deletes both in `t.Cleanup`, and the run asserts the identity it seeded rather than the one from a previous run. (#177)
- Applying a migration is verified against its checksum. `_schema_migration` now records each file's sha256 and boot fails, naming the file, when an applied migration was edited in place — previously an existing database and a fresh one diverged while both claimed the same version, with no error anywhere. A migration recorded as applied but no longer shipping is fatal too; rows predating checksums are backfilled on first boot. (#161, #175)
- The `id` attribute pattern in `pageHTMLPolicy` actually runs: bluemonday applies `AllowAttrs` rules in declaration order, so the rule was overwritten before it could match. Enforced in a post-pass. (#166, #168)
- An AI connection's key is pinned to the host it was entered for. Constraining *where* a key may be sent (#140) did not constrain *which* host, and the destination came from a provider row shared by every connection on it — one edit moved every connection's key at once. (#167, #169)
- Provider destinations are constrained and sync errors no longer echoed raw. An admin could point `_ai_provider.base_url` anywhere and have the server attach a decrypted key to that request, and could read transport errors back to distinguish timeout from refusal from DNS failure, using the endpoint as a blind port scanner. (#140, #165)
- A fresh database can finish migrating again. Three defects visible only from an empty database, found by running the integration test against a real Postgres for the first time. (#170, #171)

## [0.1.0] - 2026-11-05

First tagged release. Baseline `5497507`, deployed to `velm.dev`.

### Added

- Task engine: tasks, assignment and state, on PostgreSQL migrations 001–080.
- App definitions as YAML, with a draft and a published version.
- Schema builder, forms and pages.
- Goja scripting for server-side logic; service calls, triggers and schedules.
- Agent API tokens and role-based access control with security rules.

### Notes

- Pre-1.0, deliberately: the app-definition format is unversioned and will change (#159), there is no stable HTTP API (#163), and migration files are append-only from this tag (#161).
