# Velm — Getting started

Velm is a low-code application platform: define your app's data model, screens, automations and logic in YAML, and Velm materialises a working structured application.

> **Alpha:** the YAML schema will change before 1.0. See the [README](../README.md) for the honest status.

## Quickstart

### Run with Docker (recommended)

```bash
docker pull andywithcamera/velm
docker run -p 3000:3000 \
  -e DATABASE_URL=postgres://user:pass@host:5432/velm \
  -e BOOTSTRAP_USER_EMAIL=admin@example.com \
  -e BOOTSTRAP_USER_PASSWORD=change-this-now \
  andywithcamera/velm
```

### Build from source

Requires **Go 1.22+**.

```bash
git clone https://github.com/andywithcamera/velm.git
cd velm
go build -o velm ./cmd/server
./velm
```

## First production-style deploy

When deploying behind a reverse proxy or platform edge, set at minimum:

- `APP_ENV=production`
- `DATABASE_URL`
- `BOOTSTRAP_USER_EMAIL`
- `BOOTSTRAP_USER_PASSWORD` or `BOOTSTRAP_USER_PASSWORD_FILE`
- `SESSION_AUTH_KEY` and `SESSION_ENCRYPTION_KEY` as base64-encoded 32-byte values
- `SECURITY_HSTS=true`

> `APP_ENV=production` matters: it switches the session cookie to `Secure` (HTTPS-only). Behind TLS-terminating edges (Railway, Cloudflare, nginx), set it explicitly so sessions are never sent in the clear. `SECURITY_HSTS=true` adds the `Strict-Transport-Security` header (it is also emitted whenever `SESSION_COOKIE_SECURE=true`), so send both once you are serving HTTPS.

The container exposes health endpoints at `/health` and `/healthz`, and a build identity endpoint at `/version`.

### Verifying a deploy

`GET /version` returns the commit the running binary was built from, with no authentication:

```bash
curl -s https://your-host/version
# {"commit":"1295514e7e6ef660cb22c065634845ca5900da30","built":"2026-11-04T22:18:52Z","go":"go1.26.0"}
```

Stamp the values at build time. `make build` does this automatically, and the `Dockerfile` accepts `GIT_COMMIT` and `BUILD_TIME`:

```bash
docker build --build-arg GIT_COMMIT=$(git rev-parse HEAD) --build-arg BUILD_TIME=$(date -u +%Y-%m-%dT%H:%M:%SZ) .
```

On Railway, no build args are needed: the Dockerfile declares `ARG RAILWAY_GIT_COMMIT_SHA`, so Railway injects the deployed commit for GitHub-triggered builds. Railway only passes variables whose name is declared as an `ARG` — that declaration is the whole contract. Deploys that do not come from GitHub (`railway up`, image deploys) have no commit to report and show `"unknown"`.

An unstamped image falls back to the platform commit (`RAILWAY_GIT_COMMIT_SHA` on Railway) and reports `"unknown"` if neither is available, so a missing stamp is never mistaken for a real one.

## Where to go next

- [Open an issue](../../issues) for bugs
- [Start a discussion](../../discussions) for ideas
- Review `CONTRIBUTING.md` before opening a pull request
