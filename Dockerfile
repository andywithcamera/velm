# syntax=docker/dockerfile:1

FROM golang:1.26-bookworm AS build
WORKDIR /src

# Build identity for GET /version.
#
# Commit: RAILWAY_GIT_COMMIT_SHA is declared with the exact name Railway uses so
# the platform injects it at build time; Railway only passes provided variables
# whose name is declared as an ARG. Deploys that are not triggered from GitHub
# (e.g. `railway up`) leave it empty, and builds elsewhere pass GIT_COMMIT.
#
# Release: derived by scripts/describe-release.sh from the repository in the
# build context when there is one, and otherwise from the GitHub API using the
# commit above. The second path is what production uses: Railway's builder
# strips .git before the build context, so keeping .git in .dockerignore is
# necessary for local `docker build` but cannot help there. An explicit
# BUILD_RELEASE always wins. With no repository, no commit and no reachable
# API, the value is "unknown" rather than a plausible-looking lie.
ARG RAILWAY_GIT_COMMIT_SHA=""
ARG GIT_COMMIT=""
ARG BUILD_RELEASE=""
ARG BUILD_TIME=""

# curl is what scripts/describe-release.sh talks to GitHub with when the build
# context carries no repository. It is present in this image already, but stated
# explicitly because losing it would silently downgrade release to "unknown".
RUN command -v curl >/dev/null 2>&1 || \
	(apt-get update && apt-get install -y --no-install-recommends curl ca-certificates && rm -rf /var/lib/apt/lists/*)

COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN mkdir -p /out \
	&& COMMIT="${GIT_COMMIT:-${RAILWAY_GIT_COMMIT_SHA}}" \
	&& COMMIT="${COMMIT:-unknown}" \
	&& STAMPED_AT="${BUILD_TIME:-$(date -u +%Y-%m-%dT%H:%M:%SZ)}" \
	&& RELEASE="$(sh scripts/describe-release.sh "${COMMIT}")" \
	&& echo "Building velm commit=${COMMIT} release=${RELEASE} built=${STAMPED_AT}" \
	&& CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w -X main.buildCommit=${COMMIT} -X main.buildRelease=${RELEASE} -X main.buildTime=${STAMPED_AT}" -o /out/velm ./cmd/server \
	&& CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/bootstrap-user ./cmd/bootstrap-user \
	&& CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/bootstrap-admin ./cmd/bootstrap-admin

FROM gcr.io/distroless/static-debian12:nonroot
WORKDIR /app

COPY --from=build --chown=nonroot:nonroot /out/velm /app/velm
COPY --from=build --chown=nonroot:nonroot /out/bootstrap-user /app/bootstrap-user
COPY --from=build --chown=nonroot:nonroot /out/bootstrap-admin /app/bootstrap-admin
COPY --from=build --chown=nonroot:nonroot /src/web /app/web

ENV PORT=3000
ENV SESSION_KEY_FILE=/tmp/velm-session-keys.json

USER nonroot:nonroot
EXPOSE 3000
ENTRYPOINT ["/app/velm"]
