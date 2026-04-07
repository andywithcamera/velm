# Velm

**A low-code application platform for building structured tools — from household management to enterprise operations.**

[![Licence: AGPL v3](https://img.shields.io/badge/Licence-AGPL%20v3-blue.svg)](LICENSE)
[![Status: Alpha](https://img.shields.io/badge/Status-Alpha-orange.svg)]()
[![Docker](https://img.shields.io/badge/Docker-andywithcamera%2Fvelm-blue.svg)](https://hub.docker.com/r/andywithcamera/velm)

---

> ⚠️ **Alpha — Honest Warning**
>
> Velm is being built in the open from an early stage. The runtime is functional but not yet stable. The YAML schema **will** change before 1.0.
>
> Early adopters who like getting in before the paint dries — welcome. If you need something production-ready today — check back in a few weeks.

---

## What is Velm?

Most tools for managing structured work are either too expensive, too rigid, or too generic to be genuinely useful. The ones that do everything cost a fortune and take months to configure. The ones you can afford don't do quite enough.

Velm is a different approach.

You define your application in YAML — its data model, its screens, its automations, its logic. Velm materialises it. You get a working, structured application without the boilerplate, the framework sprawl, or the enterprise licence bill.

The goal is to give developers — and eventually everyone — a way to build the kind of internal tools that currently require either significant budget or a dedicated engineering team.

**Stack:** Go · HTMX · PostgreSQL · Goja (JS scripting)  
**Model:** Open core · AGPL-3.0-only + commercial licensing · Managed hosting coming

---

## What's in the box

| | Feature | Status |
|---|---|---|
| ✅ | YAML-defined app model | Stable-ish |
| ✅ | Platform runtime (Go + HTMX) | Stable-ish |
| ✅ | Core apps: Tasks, Docs, Entities | Working |
| ✅ | Group-based RBAC | Working |
| ✅ | Goja scripting engine | Working |
| ✅ | App registry + marketplace infrastructure | Working |
| 🔧 | LLM agents as first-class platform users | In progress |
| 🔧 | Dev-Works — agile delivery management | In progress |
| 🔧 | Ops-Works — ITSM / CMDB | In progress |
| ⏳ | Managed hosting | Coming |
| ⏳ | Full documentation | Coming — yes, I know |

---

## What "Alpha" actually means

Not vaporware. Not a mockup. Not a landing page with a waitlist.

Velm runs. You can install it, define an app in YAML, and have something working today. What you should expect at this stage:

- **Known runtime instability** — being actively resolved
- **YAML schema will change** — breaking changes will be documented when they happen
- **Security is still being hardened** — use normal internet-facing precautions and review your deployment config before exposing it publicly
- **Sparse documentation** — the code and examples are your best guide for now

If you hit something broken, [open an issue](../../issues).  
If you build something interesting, [start a discussion](../../discussions).  
Both are genuinely useful.

---

## Getting started

```bash
# Docker (recommended)
docker pull andywithcamera/velm
docker run -p 3000:3000 \
  -e DATABASE_URL=postgres://user:pass@host:5432/velm \
  -e BOOTSTRAP_USER_EMAIL=admin@example.com \
  -e BOOTSTRAP_USER_PASSWORD=change-this-now \
  andywithcamera/velm
```

```bash
# Build from source (requires Go 1.22+)
git clone https://github.com/andywithcamera/velm.git
cd velm
go build -o velm ./cmd/server
./velm
```

Full setup guide in [`docs/getting-started.md`](docs/getting-started.md).

For a first production-style deploy behind a reverse proxy or platform edge, set:

- `APP_ENV=production`
- `DATABASE_URL`
- `BOOTSTRAP_USER_EMAIL`
- `BOOTSTRAP_USER_PASSWORD` or `BOOTSTRAP_USER_PASSWORD_FILE`
- `SESSION_AUTH_KEY` and `SESSION_ENCRYPTION_KEY` as base64-encoded 32-byte values

The container exposes a health endpoint at `/health` and `/healthz`.

---

## The vision

Velm is designed around a few ideas that inform every decision:

- **Apps are defined, not coded** — YAML describes what your app is; Velm handles the rest
- **Everything is a task** — work flows through one unified model whether it's assigned to a human, a script, or an LLM agent
- **AI agents are first-class actors** — with roles, permissions, and accountability, not bolted-on afterthoughts
- **Self-hostable by default** — your data stays where you put it
- **Open source forever** — the core will never be closed

The first-party apps — Dev-Works for agile delivery, Ops-Works for ITSM — demonstrate what the platform can do and are useful in their own right.

---

## Licence

Velm is publicly released under the [GNU Affero General Public Licence v3.0](LICENSE), with the repository notice clarified in [COPYRIGHT.md](COPYRIGHT.md) as `AGPL-3.0-only`.

Plain terms: use it, modify it, and self-host it freely. If you run a modified version as a network service, you must publish your modifications under the same licence. This protects the community that builds on it.

Velm is also intended to be dual-licensed. Commercial terms may be available separately from the copyright holder; see [COMMERCIAL-LICENSING.md](COMMERCIAL-LICENSING.md).

## Railway

This repository now includes [railway.json](railway.json) with a Dockerfile builder, healthcheck path, and restart policy suitable for a Railway service. For a template based on this repo:

- add a PostgreSQL service and wire its `DATABASE_URL` into the app service;
- require `BOOTSTRAP_USER_EMAIL` from the deploying user;
- generate `BOOTSTRAP_USER_PASSWORD`, `SESSION_AUTH_KEY`, and `SESSION_ENCRYPTION_KEY` as template variables or secrets;
- set `APP_ENV=production`; and
- use `/health` as the healthcheck path if you override the checked-in config.

---

## Contributing

Velm is solo-maintained. Response times reflect that honestly.

Contributions intended for inclusion in the project are accepted only under the terms in [CLA.md](CLA.md). Start with [CONTRIBUTING.md](CONTRIBUTING.md) before opening a pull request.

- **Bugs:** open an issue with reproduction steps
- **Ideas:** open a discussion before building, to avoid wasted effort
- **Questions:** GitHub Discussions

---

## Roadmap

Short version: stability → documentation → managed hosting → the world.

---
