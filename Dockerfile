# batondeck-kanban image: one static binary on distroless, non-root, state on /data.
# Copied from hdtp-gateway's Dockerfile, its limits sidecar removed.
FROM golang:1.26.9-alpine@sha256:cdfd4fe2da6b225d8b40c6b7a105736e548e83ff56d5d8f9394446eeb5eb84e0 AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod go mod download
COPY . .
ARG VERSION=0.1.0-dev
RUN --mount=type=cache,target=/go/pkg/mod --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 go build -trimpath \
      -ldflags "-s -w -X main.version=${VERSION}" \
      -o /batondeck-kanban ./cmd/batondeck-kanban
RUN mkdir /data-skel

FROM gcr.io/distroless/static-debian12:nonroot
# The image's labels (`make image`, RELEASING.md). `source` is what links the package on ghcr.io to
# this repository; VERSION is the build argument main.version is stamped with.
ARG VERSION=0.1.0-dev
LABEL org.opencontainers.image.source="https://github.com/batondeck/batondeck-kanban" \
      org.opencontainers.image.version="${VERSION}" \
      org.opencontainers.image.licenses="Apache-2.0" \
      org.opencontainers.image.description="batondeck-kanban, the self-hosted BatonDeck task orchestrator"
# /usr/local/bin is on distroless's PATH, so `docker compose exec <service> batondeck-kanban <cmd>`
# resolves; /batondeck-kanban is the entrypoint.
COPY --from=build /batondeck-kanban /batondeck-kanban
COPY --from=build /batondeck-kanban /usr/local/bin/batondeck-kanban
# named volumes inherit the image's ownership of the mount point on first use;
# distroless has no shell, so seed /data with the right owner at build time
COPY --from=build --chown=nonroot:nonroot /data-skel /data
# The SQLite file lives on the volume, and the server listens on every interface so a published
# port reaches it (the binary's own default is loopback); the healthcheck dials loopback.
ENV BATONDECK_DATABASE_URL=/data/batondeck-kanban.db
ENV BATONDECK_LISTEN=tcp://:8080
VOLUME /data
EXPOSE 8080
USER nonroot
HEALTHCHECK --interval=30s --timeout=5s --start-period=10s \
  CMD ["/batondeck-kanban", "healthcheck"]
ENTRYPOINT ["/batondeck-kanban"]
CMD ["serve"]
