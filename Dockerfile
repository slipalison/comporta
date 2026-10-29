# syntax=docker/dockerfile:1
# Binário Go estático (sem CGO: todos os drivers são Go puro) numa imagem
# distroless, sem shell nem gerenciador de pacotes.
FROM golang:1.27.3-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod go mod download
COPY . .
ARG VERSION=dev
RUN --mount=type=cache,target=/go/pkg/mod --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 go build -trimpath -ldflags="-s -w -X main.version=${VERSION}" -o /out/comporta . \
    && mkdir -p /out/data

# distroless/static: certificados, tzdata e o usuário nonroot (65532).
FROM gcr.io/distroless/static-debian13:nonroot
LABEL org.opencontainers.image.title="comporta" \
      org.opencontainers.image.description="Gateway MCP com permissões por conexão para PostgreSQL, SQL Server, MySQL, SQLite e MongoDB" \
      org.opencontainers.image.source="https://github.com/slipalison/comporta" \
      org.opencontainers.image.licenses="MIT"
COPY --from=build /out/comporta /comporta
COPY --from=build --chown=65532:65532 /out/data /data
ENV COMPORTA_ADDR=:8080 COMPORTA_DATA_DIR=/data
VOLUME /data
EXPOSE 8080
USER 65532:65532
HEALTHCHECK --interval=30s --timeout=5s --start-period=10s CMD ["/comporta", "healthcheck"]
ENTRYPOINT ["/comporta"]
