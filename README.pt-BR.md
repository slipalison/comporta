# Comporta

[English](README.md) · **Português**

**Um servidor MCP para todos os seus bancos de dados, em que você decide o que a IA pode fazer em cada um.**

Comporta é a porta que controla a passagem da água num canal. Aqui ela fica entre os seus assistentes de IA (Claude Code, GitHub Copilot, Gemini…) e os seus bancos: PostgreSQL, SQL Server, MySQL/MariaDB, Oracle, SQLite, ClickHouse, MongoDB e Redis. A leitura passa sempre; a escrita só passa onde você abrir.

## Começo rápido com Docker

Você precisa do Docker. São três passos, uns dois minutos.

**1. Crie o arquivo de configuração.** Ele guarda a senha da interface e um token para o seu cliente de IA:

```bash
cat > comporta.env <<EOF
COMPORTA_ADMIN_PASSWORD=$(openssl rand -hex 12)
COMPORTA_MCP_TOKENS=claude-code:$(openssl rand -hex 24)
EOF
cat comporta.env   # guarde os dois valores: a senha abre a interface, o token conecta a IA
```

**2. Suba o Comporta:**

```bash
docker run -d --name comporta --restart unless-stopped \
  --env-file comporta.env \
  -p 127.0.0.1:7843:8080 \
  -v comporta-data:/data \
  slipalison/comporta:latest
```

A linha `-v comporta-data:/data` é opcional, e recomendada: ela guarda as conexões e a auditoria quando você atualiza a imagem. Veja [Guardar os seus dados](#guardar-os-seus-dados).

**3. Abra <http://127.0.0.1:7843>**, entre como `admin` com a senha do passo 1 e cadastre a primeira conexão. Depois, conecte o seu cliente de IA:

```bash
claude mcp add --transport http --scope user comporta http://127.0.0.1:7843/mcp \
  --header "Authorization: Bearer <o token depois de claude-code: no comporta.env>"
```

Pronto. Peça ao assistente algo como *"liste as tabelas do meu banco"* e veja a aba **Auditoria**.

> **Banco na mesma máquina?** Dentro do Docker, `localhost` é o próprio container. Use `host.docker.internal` na connection string e acrescente `--add-host host.docker.internal:host-gateway` ao `docker run`.

A mesma imagem também está no GitHub: `ghcr.io/slipalison/comporta`. Cada versão sai nos dois registros com o mesmo digest. Em produção, fixe uma versão (`slipalison/comporta:0.1.0`) em vez de `latest`.

### Guardar os seus dados

O Comporta guarda as conexões, as connection strings cifradas e a auditoria num arquivo SQLite em `/data`. O volume é **opcional**:

| Como você roda | Reinício (`docker restart`) | Imagem nova (remove e roda de novo) |
|---|---|---|
| sem volume | dados mantidos | **dados perdidos** |
| volume nomeado: `-v comporta-data:/data` | dados mantidos | dados mantidos |
| pasta do host: `-v /srv/comporta:/data` | dados mantidos | dados mantidos |

Sem volume, o Comporta funciona normalmente e avisa: um aviso no log e outro no topo da interface.

**Atualizar a imagem** com volume é só remover o container antigo e subir a imagem nova com o mesmo `-v`. Na partida, o Comporta confere a versão do arquivo e, se a imagem nova precisar de um formato mais novo, primeiro grava uma cópia ao lado (`comporta.db.v<versão antiga>.bak`) e depois migra. O log mostra:

```
"msg":"database schema migrated","from":0,"to":2,"backup":"/data/comporta.db.v0.bak"
```

**Voltar** para uma imagem mais antiga depois de uma migração é recusado: a imagem antiga não conhece o formato novo. Suba de novo a imagem mais nova, ou restaure o arquivo `.bak` no lugar do `comporta.db`.

**Uma pasta do host** precisa aceitar escrita do usuário do container (`65532`). Se não aceitar, o Comporta para e mostra a solução no log:

```bash
sudo mkdir -p /srv/comporta && sudo chown -R 65532:65532 /srv/comporta
```

Volume nomeado não precisa de nada: o Docker já dá o dono certo.

### Com Docker Compose

O repositório traz um `docker-compose.yml` que roda o container com sistema de arquivos somente leitura, sem capabilities do Linux e com a porta só em `127.0.0.1`:

```bash
git clone https://github.com/slipalison/comporta.git && cd comporta
cp .env.example .env        # defina COMPORTA_ADMIN_PASSWORD e COMPORTA_MCP_TOKENS
docker compose up -d
```

## O que você ganha

- **Permissões por conexão.** `INSERT`, `UPDATE`, `DELETE` e `DDL/Admin` ligam e desligam em cada banco, e valem na próxima chamada da IA. `SELECT` está sempre liberado.
- **Auditoria de toda chamada.** Qual cliente, qual conexão, o comando, os parâmetros, o resultado, as linhas e a duração. Filtros, detalhe e exportação em CSV.
- **Vários bancos ao mesmo tempo**, cada um com o seu pool de conexões.
- **Imagem pequena e fechada.** Uns 13 MB comprimida: binário Go estático em distroless, sem shell.
- **Interface em inglês e português**, acessível (WCAG 2.2 AA) e boa de usar no celular.

![Conexões: cada banco com o nível de risco e as permissões da IA](docs/img/conexoes.pt.png)

| Auditoria | Nova conexão, tema escuro | Celular |
|---|---|---|
| ![Auditoria com filtros e o detalhe de uma chamada](docs/img/auditoria.pt.png) | ![Formulário de nova conexão no tema escuro](docs/img/nova-conexao-escuro.pt.png) | ![Lista de conexões no celular](docs/img/celular.pt.png) |

## Bancos suportados

| Banco | Connection string | Também funciona com | Observações |
|---|---|---|---|
| PostgreSQL | `postgres://user:pass@host:5432/db?sslmode=require` | CockroachDB, YugabyteDB, TimescaleDB, Supabase, Neon, Aurora PostgreSQL, AlloyDB | leituras em transação `READ ONLY` |
| SQL Server | `sqlserver://user:pass@host:1433?database=db` | Azure SQL | leituras em transação sempre desfeita |
| MySQL / MariaDB | `mysql://user:pass@host:3306/db` | TiDB, PlanetScale, Aurora MySQL | leituras em transação `READ ONLY` |
| Oracle Database | `oracle://user:pass@host:1521/service_name` | Oracle Autonomous Database | leituras em transação `SET TRANSACTION READ ONLY`; testado com o Oracle 23ai Free |
| SQLite | `/data/files/app.db` | | o arquivo precisa estar dentro do container (monte-o) |
| ClickHouse | `clickhouse://user:pass@host:9000/db` | ClickHouse Cloud | sem transações: leituras rodam com `readonly=1`, e não há `dry_run` |
| MongoDB | `mongodb://user:pass@host:27017/db?authSource=admin` | MongoDB Atlas | operações estruturadas, Extended JSON |
| Redis / Valkey | `redis://user:pass@host:6379/0` (`rediss://` com TLS) | Valkey, KeyDB, Dragonfly, ElastiCache, Upstash | um comando por chamada; veja abaixo |

Todos os drivers são Go puro, o que mantém a imagem pequena e sem bibliotecas nativas. É também por isso que alguns bancos ainda não estão aqui: Snowflake e BigQuery trariam SDKs maiores que o resto da imagem, e IBM Db2 e DuckDB exigem bibliotecas C. [Abra uma issue](https://github.com/slipalison/comporta/issues) se precisar de um deles.

## Conectar clientes de IA

O Comporta fala MCP por Streamable HTTP em `http://127.0.0.1:7843/mcp`. Toda requisição leva `Authorization: Bearer <token>`.

Cada cliente deve ter o seu token. Dê um rótulo a cada um em `COMPORTA_MCP_TOKENS`, separados por vírgula:

```
COMPORTA_MCP_TOKENS=claude-code:<token-1>,copilot:<token-2>
```

O rótulo aparece na auditoria, então você sabe qual assistente rodou cada comando. A aba **Conectar clientes** da interface tem os trechos prontos para copiar: Claude Code, VS Code / GitHub Copilot (`mcp.json`), Copilot CLI, Gemini CLI e outros (`mcpServers`).

## Configuração

Tudo é configurado por variáveis de ambiente.

| Variável | Padrão | O que faz |
|---|---|---|
| `COMPORTA_ADMIN_PASSWORD` | — (obrigatória) | Senha da interface (HTTP basic auth). Dez senhas erradas em 5 minutos bloqueiam aquele endereço por 5 minutos. |
| `COMPORTA_ADMIN_USER` | `admin` | Usuário da interface. |
| `COMPORTA_MCP_TOKENS` | — (obrigatória) | `rotulo:token,...`; cada token com 16 caracteres ou mais. `COMPORTA_MCP_TOKEN` serve para um token só. |
| `COMPORTA_SECRET_KEY` | chave aleatória em `/data/secret.key` | Frase que criptografa as connection strings guardadas (AES-256-GCM, chave derivada por Argon2id). Trocar a frase depois torna as connection strings guardadas ilegíveis, e o servidor não sobe. |
| `COMPORTA_LOG_RETENTION_DAYS` | `30` | Registros de auditoria mais antigos que isso são apagados de hora em hora. |
| `COMPORTA_MAX_ROWS_CAP` | `1000` | Máximo de linhas que uma consulta pode devolver; cada conexão define o seu limite abaixo disso. |
| `COMPORTA_PORT` | `7843` | Porta no host, sempre em `127.0.0.1` (só no Docker Compose). |

Mantenha o `comporta.env` (ou o `.env`) fora das pastas que os seus agentes de IA conseguem ler: quem tem a senha da interface muda as permissões.

### Vindo do db-mcp

O Comporta se chamava db-mcp. Um volume existente continua funcionando: as variáveis `DBMCP_*` continuam lidas (o log pede para renomear), e o `dbmcp.db` continua sendo usado. Na primeira partida, as connection strings guardadas são cifradas de novo no formato novo.

Só se o db-mcp rodava **com** `DBMCP_SECRET_KEY`, suba o Comporta uma vez com a chave antiga em `COMPORTA_LEGACY_KEY`:

```bash
printf %s "$DBMCP_SECRET_KEY" | sha256sum | cut -c1-64   # este é o COMPORTA_LEGACY_KEY
```

Depois dessa primeira partida, a variável pode sair. Sem ela, o servidor não sobe e o log diz o motivo.

## O que a IA enxerga

As ferramentas são montadas a partir das conexões **ativas**. O parâmetro `connection` de cada ferramenta lista só as conexões que ela pode usar, com tipo, dialeto SQL, estilo de parâmetro, descrição e permissões.

| Ferramenta | Aparece quando |
|---|---|
| `list_connections` | sempre: nome, tipo, dialeto, permissões e quais ferramentas usar |
| `describe_schema` | há alguma conexão: tabelas e colunas (SQL), ou coleções, campos amostrados e índices (MongoDB) |
| `sql_query` | há conexão SQL: um `SELECT`/`WITH`/`SHOW`/`EXPLAIN`, em transação sempre desfeita |
| `sql_execute` | alguma conexão SQL tem escrita liberada; aceita `dry_run` |
| `mongo_find`, `mongo_aggregate`, `mongo_count` | há conexão MongoDB |
| `mongo_insert`, `mongo_update`, `mongo_delete` | alguma conexão MongoDB tem aquela operação liberada |
| `redis_query` | há conexão Redis: um comando de leitura (`GET`, `HGETALL`, `LRANGE`, `SCAN`…) |
| `redis_execute` | alguma conexão Redis tem escrita liberada |

**Capriche na descrição de cada conexão.** É por ela que a IA escolhe o banco certo.

Clientes que guardam a lista de ferramentas podem precisar reconectar depois de uma mudança numa conexão. As permissões, porém, são conferidas a cada chamada.

## Como as permissões são garantidas

Nos bancos SQL, três camadas, do gateway até o banco:

1. **Um classificador de SQL por dialeto** (`internal/guard`). Ele lê comentários, strings, as aspas `$$` do Postgres, os `[ ]` do T-SQL e as crases do MySQL como cada banco lê. As aspas `q'[...]'` do Oracle e os comentários `#` e escapes com barra invertida do ClickHouse são lidos do mesmo jeito. Acha escrita escondida em CTE, `SELECT INTO`, `MERGE`, `EXEC`, `COPY`, `SET`, `OPENQUERY` e afins. Um comando por chamada; o que ele não consegue provar que é leitura conta como DDL/Admin. No SQLite, `ATTACH` e `DETACH` são sempre recusados, porque abririam outros arquivos, inclusive o banco do próprio Comporta.
2. **Transações de leitura.** Leituras rodam em transação `READ ONLY` (PostgreSQL, MySQL, Oracle), numa transação sempre desfeita (SQL Server, SQLite) ou com a configuração `readonly=1` do servidor (ClickHouse). Uma função que escreve dentro de um `SELECT` é barrada pelo próprio banco.
3. **O usuário do banco.** O servidor de banco continua sendo a última barreira: use um usuário só com os grants necessários.

No **Redis** os comandos são classificados pelo nome: `SADD`, `LPUSH` ou `ZADD` exigem insert; `INCR`, `EXPIRE` ou `RENAME` exigem update; `SET`, `HSET` ou `MSET` exigem insert e update (criam ou sobrescrevem); `DEL`, `HDEL` ou `LPOP` exigem delete; `FLUSHDB`, `CONFIG`, `EVAL` e qualquer comando desconhecido exigem DDL/Admin. Alguns nunca estão disponíveis, porque travam o servidor ou mudam uma conexão compartilhada: `KEYS` (use `SCAN`), `SELECT` (ponha o número do banco na connection string), `MULTI`/`EXEC`, `SUBSCRIBE`, `MONITOR` e comandos que bloqueiam, como `BLPOP`.

No MongoDB as operações são estruturadas: `$merge` exige insert e update, `$out` exige insert, delete e DDL, e um update ou delete com filtro vazio exige `all_documents=true`.

**DDL/Admin é poder total** (`DROP`, `EXEC`, `GRANT`, `SET ROLE`…). A interface pede confirmação antes de ligar `DELETE` ou `DDL`. Deixe desligado se não precisar.

**A interface** decide o que a IA pode fazer, então fica atrás de senha, só escuta em `127.0.0.1`, bloqueia requisições de outros sites (CSRF) e roda com uma Content Security Policy sem `unsafe-inline`.

## A interface

- **Idiomas:** inglês e português. O Comporta segue o idioma do navegador, e o seletor no topo troca na hora; a escolha fica guardada.
- **Segura por desenho:** confirmação antes de liberar escrita destrutiva ou excluir uma conexão, **Desfazer** depois de toda troca de permissão e aviso antes de descartar um formulário editado.
- **Rápida de usar:** `n` abre uma nova conexão e `/` busca na auditoria. Os filtros ficam no endereço (`#/audit?status=denied`), então o botão voltar e os favoritos funcionam.
- **Acessível:** conferida com axe-core (WCAG 2.2 AA, nenhuma violação em 320, 390 e 1440 px, temas claro e escuro), com navegação por teclado e rótulos para leitor de tela. O contraste das cores é verificado por teste (`internal/web/contrast_test.go`).

Ela foi desenhada com o [kit Leis de UX](https://github.com/slipalison/leis-de-ux). As decisões principais:

| Lei | Onde |
|---|---|
| Região comum | cada conexão é um cartão, com identificação, permissões e ações em faixas separadas |
| Similaridade | toda permissão é o mesmo botão de alternância; "liberado" é ícone ✓ mais cor, nunca só cor; `DELETE` e `DDL` ficam vermelhos |
| Von Restorff | uma ação primária por tela ("Nova conexão"); o nível de risco da conexão em destaque |
| Fitts | alvos de 40 px (44 px em tela de toque); "Excluir" longe de "Testar" e "Editar" |
| Postel | "Pagamentos Prod" é aceito e vira `pagamentos_prod`; todo erro aparece junto do campo que o causou |
| Tesler | o sistema assume o que sabe: exemplo de connection string do tipo escolhido, datas enviadas como instante absoluto, atividade de 24 h em cada conexão |

### Acrescentar um idioma

Todos os textos ficam em [`internal/web/static/i18n.js`](internal/web/static/i18n.js). Copie o dicionário `en`, traduza e acrescente o idioma em `DICTIONARIES` e `LANGUAGES`. Um teste (`internal/web/i18n_test.go`) falha se faltar alguma chave em algum idioma.

## Desenvolvimento

```bash
go test -race ./...
golangci-lint run ./...          # .golangci.yml: gosec, errorlint, noctx, revive…
go build -o comporta . && COMPORTA_ADMIN_PASSWORD=x COMPORTA_MCP_TOKEN=0123456789abcdef ./comporta
```

| Pacote | O que faz |
|---|---|
| `internal/guard` | classificador de SQL |
| `internal/db` | pools de conexão, execução SQL e MongoDB, schema |
| `internal/mcpsrv` | ferramentas MCP |
| `internal/store` | store em SQLite, criptografia, auditoria |
| `internal/web` | API da interface e a interface embutida (sem build de frontend) |

**Testes de integração** rodam contra Oracle, ClickHouse e Redis de verdade, no Docker; o [docs/TESTING.md](docs/TESTING.md) mostra como. Sem eles, os testes usam SQLite e um Redis em memória.

**Por que Go:** todos os drivers são Go puro (sem CGO), então o resultado é um binário estático com a interface dentro, e uma imagem de uns 13 MB. O mesmo em Node ou Python ficaria entre 180 e 400 MB, e o SQL Server pediria bibliotecas ODBC nativas.

**A CI** roda a esteira de [`slipalison/github-workflows`](https://github.com/slipalison/github-workflows): lint, testes com piso de cobertura, imagem varrida pelo Trivy antes de publicar, Gitleaks, TruffleHog, Semgrep, SBOM, CodeQL, Quality Gate do SonarQube Cloud e versão calculada pelos [Conventional Commits](https://www.conventionalcommits.org/pt-br/).

## Licença

[MIT](LICENSE)
