# Comporta

**One MCP server for all your databases, where you decide what the AI may do in each one.**

Comporta sits between your AI assistants (Claude Code, GitHub Copilot, Gemini…) and your databases: PostgreSQL, SQL Server, MySQL/MariaDB, SQLite and MongoDB. Reads always flow through; writes (`INSERT`, `UPDATE`, `DELETE`, `DDL`) only where you turn them on. Every call is recorded in an audit log, and an admin UI in English and Portuguese lets you manage it all.

Source, full docs and Portuguese version: **https://github.com/slipalison/comporta** · [Português](https://github.com/slipalison/comporta/blob/main/README.pt-BR.md)

![Connections screen](https://raw.githubusercontent.com/slipalison/comporta/main/docs/img/connections.png)

## Quick start

**1. Create the settings file** (admin password and a token for your AI client):

```bash
cat > comporta.env <<EOF
COMPORTA_ADMIN_PASSWORD=$(openssl rand -hex 12)
COMPORTA_MCP_TOKENS=claude-code:$(openssl rand -hex 24)
EOF
cat comporta.env   # keep both values
```

**2. Run it:**

```bash
docker run -d --name comporta --restart unless-stopped \
  --env-file comporta.env \
  -p 127.0.0.1:7843:8080 \
  -v comporta-data:/data \
  slipalison/comporta:latest
```

**3. Open http://127.0.0.1:7843**, sign in as `admin` with the password from step 1, and add a connection. Then connect your AI client:

```bash
claude mcp add --transport http --scope user comporta http://127.0.0.1:7843/mcp \
  --header "Authorization: Bearer <the token after claude-code: in comporta.env>"
```

Database on the same machine? Use `host.docker.internal` in the connection string and add `--add-host host.docker.internal:host-gateway` to `docker run`.

## Tags

| Tag | Meaning |
|---|---|
| `latest` | the newest release |
| `0.1.0`, `0.2.0`… | a specific release: pin one in production |

The same images are on `ghcr.io/slipalison/comporta`, with the same digests. Platform: `linux/amd64`.

## Settings

| Variable | Default | What it does |
|---|---|---|
| `COMPORTA_ADMIN_PASSWORD` | — (required) | Password of the admin UI. |
| `COMPORTA_ADMIN_USER` | `admin` | User name of the admin UI. |
| `COMPORTA_MCP_TOKENS` | — (required) | `label:token,...`; each token at least 16 characters. The label identifies the client in the audit log. |
| `COMPORTA_SECRET_KEY` | random key in `/data` | Passphrase that encrypts the stored connection strings. |
| `COMPORTA_LOG_RETENTION_DAYS` | `30` | Days to keep audit records. |
| `COMPORTA_MAX_ROWS_CAP` | `1000` | Most rows any query may return. |

Data lives in the `/data` volume. The container runs as a non-root user, on a distroless image with no shell, and listens on port `8080`.

License: MIT.
