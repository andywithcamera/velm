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

> `APP_ENV=production` matters: it switches the session cookie to `Secure` (HTTPS-only). Behind TLS-terminating edges (Railway, Cloudflare, nginx), set it explicitly so sessions are never sent in the clear.

The container exposes health endpoints at `/health` and `/healthz`.

## Where to go next

- [Open an issue](../../issues) for bugs
- [Start a discussion](../../discussions) for ideas
- Review `CONTRIBUTING.md` before opening a pull request
