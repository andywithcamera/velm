# Getting Started

Velm runs as a single Go web service backed by PostgreSQL.

## Local Docker

1. Start PostgreSQL and Velm together:

```bash
DATABASE_PASSWORD=change-me \
BOOTSTRAP_USER_EMAIL=admin@example.com \
BOOTSTRAP_USER_PASSWORD=change-this-now \
docker compose up --build
```

2. Open `http://localhost:3000`.

3. Sign in with the bootstrap user you supplied.

## Build From Source

1. Set a PostgreSQL connection string in `DATABASE_URL`.
2. Set `BOOTSTRAP_USER_EMAIL` and `BOOTSTRAP_USER_PASSWORD` for the first run.
3. Start the server:

```bash
go build -o velm ./cmd/server
./velm
```

## Public Deployments

For first startup on a public or production-style deployment, set all of:

- `APP_ENV=production`
- `DATABASE_URL`
- `BOOTSTRAP_USER_EMAIL`
- `BOOTSTRAP_USER_PASSWORD` or `BOOTSTRAP_USER_PASSWORD_FILE`
- `SESSION_AUTH_KEY`
- `SESSION_ENCRYPTION_KEY`

`SESSION_AUTH_KEY` and `SESSION_ENCRYPTION_KEY` must be base64-encoded 32-byte values.

The service exposes health endpoints at `/health` and `/healthz`.

## Railway

The repository includes [railway.json](../railway.json) with a Dockerfile builder, healthcheck path, and restart policy.

When creating a Railway template, add a PostgreSQL service and wire its `DATABASE_URL` into the app service. Require `BOOTSTRAP_USER_EMAIL`, and generate the password and session keys as template secrets.
