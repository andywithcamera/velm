# syntax=docker/dockerfile:1

FROM golang:1.25-bookworm AS build
WORKDIR /src

COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN mkdir -p /out \
	&& CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/velm ./cmd/server \
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
