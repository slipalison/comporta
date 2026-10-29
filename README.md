# DB MCP Gateway

Servidor MCP único para PostgreSQL, SQL Server, MySQL/MariaDB, SQLite e MongoDB, com:

- **UI web** para cadastrar conexões e ligar/desligar `INSERT`, `UPDATE`, `DELETE` e `DDL/Admin` por conexão (SELECT sempre liberado);
- **auditoria** de toda chamada (quem, qual conexão, statement, parâmetros, status, linhas, duração), com filtros, detalhe e export CSV;
- **várias conexões simultâneas**, cada uma com seu pool;
- **SQLite interno** para conexões e logs, com retenção configurável;
- imagem Docker de **~38 MB** (Go estático + distroless).

## Por que Go

| | Go | Node/TS | Python |
|---|---|---|---|
| Imagem final | **~38 MB** (binário estático + distroless) | ~180–250 MB | ~250–400 MB (SQL Server via ODBC soma ~100 MB) |
| Drivers | pgx, go-mssqldb (Microsoft), go-sql-driver/mysql, mongo-driver v2, SQLite puro Go (sem CGO) | bons | bons, mas SQL Server exige ODBC nativo |
| SDK MCP | oficial (`modelcontextprotocol/go-sdk` v1.8) | oficial | oficial |
| Deploy | 1 binário, UI embutida | node_modules | venv + libs nativas |

Go foi o melhor equilíbrio entre facilidade e tamanho: todos os drivers são Go puro, a UI vai embutida no binário (`go:embed`) e o container não tem shell.

## Subir

```bash
cp .env.example .env        # defina DBMCP_ADMIN_PASSWORD e os tokens (openssl rand -hex 24)
docker compose up -d --build
```

- UI: <http://127.0.0.1:7843> (usuário/senha do `.env`)
- MCP: `http://127.0.0.1:7843/mcp` (Streamable HTTP, `Authorization: Bearer <token>`)

Para bancos que rodam no host, use `host.docker.internal` no lugar de `localhost` na connection string.

### Conectar clientes

```bash
claude mcp add --transport http --scope user dbmcp http://127.0.0.1:7843/mcp \
  --header "Authorization: Bearer <token-do-claude-code>"
```

A aba **Conectar clientes** da UI mostra os snippets para VS Code, Copilot CLI, Gemini etc. Cada token em `DBMCP_MCP_TOKENS` tem um rótulo (`claude-code:...,copilot:...`) que aparece na coluna *Cliente* da auditoria.

## Configuração (`.env`)

| Variável | Padrão | |
|---|---|---|
| `DBMCP_ADMIN_USER` / `DBMCP_ADMIN_PASSWORD` | `admin` / — | Basic auth da UI. Senha obrigatória. |
| `DBMCP_MCP_TOKENS` | — | `rotulo:token,...` (token ≥ 16 caracteres). Ou `DBMCP_MCP_TOKEN` para um só. |
| `DBMCP_SECRET_KEY` | gerada em `/data/secret.key` | Criptografa as connection strings (AES-256-GCM). Trocar depois torna as conexões ilegíveis. |
| `DBMCP_LOG_RETENTION_DAYS` | `30` | Registros mais antigos são apagados de hora em hora. |
| `DBMCP_MAX_ROWS_CAP` | `1000` | Teto global de linhas por consulta (cada conexão tem o seu limite abaixo disso). |
| `DBMCP_PORT` | `7843` | Porta no host (sempre em `127.0.0.1`). |

## O que a LLM enxerga

As ferramentas são montadas a partir das conexões **ativas**, e o parâmetro `connection` de cada uma é um `enum` só com as conexões válidas para ela, descritas com tipo, dialeto, estilo de parâmetro, descrição e permissões:

| Ferramenta | Quando aparece |
|---|---|
| `list_connections` | sempre: nome, tipo, dialeto, placeholder, permissões e quais ferramentas usar |
| `describe_schema` | há conexões: tabelas/colunas (SQL) ou coleções/campos inferidos/índices (Mongo) |
| `sql_query` | há conexões SQL: um SELECT/WITH/SHOW/EXPLAIN, em transação sempre desfeita |
| `sql_execute` | há conexão SQL com alguma escrita liberada; aceita `dry_run` |
| `mongo_find`, `mongo_aggregate`, `mongo_count` | há conexões MongoDB |
| `mongo_insert` / `mongo_update` / `mongo_delete` | há conexão Mongo com a permissão correspondente |

Clientes que guardam a lista de ferramentas em cache podem precisar reconectar depois de uma mudança na UI. As permissões, porém, são checadas a cada chamada.

## Como as permissões são garantidas

1. **Classificador de SQL por dialeto** (`internal/guard`): tokeniza respeitando comentários, strings, `$$` do Postgres, `[ ]` do T-SQL e crases do MySQL; detecta escrita escondida em CTE, `SELECT INTO`, `MERGE`, `EXEC`, `COPY`, `SET`, `OPENQUERY` etc.; aceita um statement por chamada. O que não reconhece como leitura conta como DDL/Admin.
2. **Transação de leitura**: leituras rodam em `READ ONLY` (Postgres/MySQL) ou em transação sempre desfeita (SQL Server/SQLite). Uma função que escreve dentro de um `SELECT` é barrada pelo próprio banco.
3. **Usuário do banco**: o próprio servidor de banco continua sendo a última barreira. Use na connection string um usuário só com os grants necessários.

No Mongo, as operações são estruturadas: `$merge` exige insert+update, `$out` exige insert+delete+ddl, e update/delete com filtro vazio exigem `all_documents=true`.

**DDL/Admin dá poder total** (DROP, EXEC, GRANT, SET ROLE…). Deixe desligado, a não ser que precise mesmo.

**Segurança da UI:** ela decide o que a LLM pode fazer, então fica atrás de senha, só em `127.0.0.1`, com proteção CSRF. Um agente com acesso ao shell e ao `.env` poderia usar a senha, então mantenha o `.env` fora do workspace dos agentes (por exemplo, use as variáveis de ambiente da stack no Portainer).

## Desenvolvimento

```bash
go test ./...            # classificador SQL e máscara de DSN
go build -o dbmcp . && DBMCP_ADMIN_PASSWORD=x DBMCP_MCP_TOKEN=0123456789abcdef ./dbmcp
```

Estrutura: `internal/guard` (classificador), `internal/db` (pools, execução SQL/Mongo, schema), `internal/mcpsrv` (ferramentas MCP), `internal/store` (SQLite, criptografia, auditoria), `internal/web` (API + UI embutida).
