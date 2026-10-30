# syntax=docker/dockerfile:1
# A static Go binary (no CGO: every driver is pure Go) on a distroless image,
# with no shell and no package manager.
FROM golang:1.27.3-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod go mod download
COPY . .
# VERSION wins when given (docker compose passes COMPORTA_VERSION). The CI
# builds the image before it knows the version number and passes the commit
# in GIT_SHA instead: the binary then reports sha-<7 chars>, the same name as
# the image tag in the registry.
ARG VERSION=
ARG GIT_SHA=
RUN --mount=type=cache,target=/go/pkg/mod --mount=type=cache,target=/root/.cache/go-build \
    v="${VERSION}"; \
    if [ -z "$v" ] && [ -n "$GIT_SHA" ]; then v="sha-$(echo "$GIT_SHA" | cut -c1-7)"; fi; \
    CGO_ENABLED=0 go build -trimpath -ldflags="-s -w -X main.version=${v:-dev}" -o /out/comporta . \
    && mkdir -p /out/data

# distroless/static: CA certificates, tzdata and the nonroot user (65532).
FROM gcr.io/distroless/static-debian13:nonroot
LABEL org.opencontainers.image.title="comporta" \
      org.opencontainers.image.description="One MCP server for PostgreSQL, SQL Server, MySQL, SQLite and MongoDB, with per-connection permissions and an audit log" \
      org.opencontainers.image.source="https://github.com/slipalison/comporta" \
      org.opencontainers.image.licenses="MIT"
COPY --from=build /out/comporta /comporta
COPY --from=build --chown=65532:65532 /out/data /data
ENV COMPORTA_ADDR=:8080 COMPORTA_DATA_DIR=/data
# No VOLUME instruction on purpose: it would give every container a fresh
# anonymous volume, which looks persistent but is left behind (empty for the
# next container) on every image update. Mount /data to keep the data
# (-v comporta-data:/data); without a mount it lives in the container and
# survives restarts only. The server tells which one it got at startup.
EXPOSE 8080
USER 65532:65532
HEALTHCHECK --interval=30s --timeout=5s --start-period=10s CMD ["/comporta", "healthcheck"]
ENTRYPOINT ["/comporta"]
