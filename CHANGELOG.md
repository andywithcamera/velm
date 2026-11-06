# Changelog

All notable changes to Velm, newest first.

Format follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/); versioning is semver, pre-1.0 (see `AGENTS.md`). Entries are grouped `Added`, `Changed`, `Fixed`, `Removed`, and each names the issue it came from. Unreleased work sits under `[Unreleased]` until the next tag.

## [Unreleased]

### Added

- `GET /version` reports `schema`, the highest applied migration. A deploy that half-migrated, or a database restored from an older backup, is now visible from outside instead of discovered at request time. (#161, #175)
- `GET /version` reports `release`, the tag `git describe` resolves. Production now stamps it: `scripts/describe-release.sh` resolves the tag from the repository when the build context has one, and from the GitHub API when it does not — Railway's builder strips `.git`, so a production deploy names its release (`v0.1.0-13-gabc1234`) instead of reporting `unknown`. (#160, #164, #178)

### Changed

- `MFASecretCipher` generalised to `security.SecretCipher`: the AES-256-GCM string cipher was named for its first consumer, and the AI connection keys (#93), webhooks (#91) and OAuth (#88) would have inherited that name for non-MFA secrets. (#102, #174)
- CI runs `staticcheck` and `gosec`, a route × role matrix, and integration tests against a real Postgres instead of skipping them. (#141, #172)

### Removed

- The GitHub Pages workflow (`.github/workflows/static.yml`). It was the stock starter template, unmodified: it archived the entire repository root and published it at `andywithcamera.github.io/velm` — which has no `index.html`, so it served a 404. It has never published anything anyone reads. It now also fails intermittently in `actions/deploy-pages` (`Found 0 artifact(s)` immediately after a successful upload), and the two commits whose Pages check failed are the two commits Railway did not deploy. Removing the workflow removes a check that can fail for reasons unrelated to Velm. (#182)

### Fixed

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
