# batondeck-kanban image: one static binary on distroless, non-root, state on /data.
# Copied from hdtp-gateway's Dockerfile, its limits sidecar removed.
# The build stage runs on the builder's own platform (BUILDPLATFORM) and cross-compiles for the
# image's (TARGETOS/TARGETARCH, which buildx sets per --platform): Go needs no emulation to build
# for another architecture, so `make image` builds linux/arm64 on an amd64 host without QEMU. The
# final stage only copies, so it runs nothing on the target platform either.
# Both base images are named by digest (multi-platform index), so a tag moved upstream cannot change
# what the image is built from; moving to a newer base is an edit here, with the tag kept for readers.
FROM --platform=$BUILDPLATFORM golang:1.26.10-alpine@sha256:87c65a688975c0b1d4585d204874c45ca78354670e3a5f11368c5a492303d696 AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod go mod download
COPY . .
ARG VERSION=0.1.0-dev
ARG TARGETOS TARGETARCH
RUN --mount=type=cache,target=/go/pkg/mod --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH go build -trimpath \
      -ldflags "-s -w -X main.version=${VERSION}" \
      -o /batondeck-kanban ./cmd/batondeck-kanban
RUN mkdir /data-skel

FROM gcr.io/distroless/static-debian12:nonroot@sha256:afa5c872c891853ca7fcf1f12c3edb23f7eeef36189728842dd51042ff57f7ab
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
# The licence the image is under and the notices of what it redistributes, as files beside the
# binary that also prints them (`batondeck-kanban licenses`).
COPY LICENSE NOTICE THIRD_PARTY_NOTICES /licenses/
# named volumes inherit the image's ownership of the mount point on first use;
# distroless has no shell, so seed /data with the right owner at build time
COPY --from=build --chown=nonroot:nonroot /data-skel /data
# The SQLite file and the blobs live on the volume (named, not left to the per-user default dir), and the server listens on every interface so a published
# port reaches it (the binary's own default is loopback); the healthcheck dials loopback.
ENV BATONDECK_DATABASE_URL=/data/batondeck-kanban.db
ENV BATONDECK_BLOB_DIR=/data/blobs
ENV BATONDECK_LISTEN=tcp://:8080
VOLUME /data
EXPOSE 8080
USER nonroot
HEALTHCHECK --interval=30s --timeout=5s --start-period=10s \
  CMD ["/batondeck-kanban", "healthcheck"]
ENTRYPOINT ["/batondeck-kanban"]
CMD ["serve"]
