# syntax=docker/dockerfile:1
FROM golang:1.27-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod go mod download
COPY . .
ARG VERSION=dev
RUN --mount=type=cache,target=/go/pkg/mod --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 go build -trimpath -ldflags="-s -w -X main.version=${VERSION}" -o /out/dbmcp . \
    && mkdir -p /out/data

# distroless/static: CA certificates, tzdata and a nonroot user, no shell.
FROM gcr.io/distroless/static-debian13:nonroot
COPY --from=build /out/dbmcp /dbmcp
COPY --from=build --chown=65532:65532 /out/data /data
ENV DBMCP_ADDR=:8080 DBMCP_DATA_DIR=/data
VOLUME /data
EXPOSE 8080
USER 65532:65532
HEALTHCHECK --interval=30s --timeout=5s --start-period=10s CMD ["/dbmcp", "healthcheck"]
ENTRYPOINT ["/dbmcp"]
