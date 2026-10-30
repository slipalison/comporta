// Translations for the admin UI. English is the default; Portuguese is the
// second language. Keys are flat; values may hold {placeholders}, filled by t().
//
// Static text in index.html is marked with data-i18n (text), data-i18n-html
// (text with markup from THIS file only, never from the API) and
// data-i18n-attr="attribute:key;attribute:key".

const en = {
  "app.skip": "Skip to content",
  "nav.label": "Main",
  "nav.connections": "Connections",
  "nav.audit": "Audit log",
  "nav.clients": "Connect clients",
  "lang.label": "Language",
  "pulse.prefix": "MCP calls in the last ",
  "pulse.calls": { one: "call", other: "calls" },
  "pulse.denied": { one: "denied", other: "denied" },
  "pulse.errors": { one: "error", other: "errors" },

  "conn.title": "Connections",
  "conn.lede": "Each connection is a database the AI can see. Read queries are always allowed; writing needs the permission turned on here.",
  "conn.new": "New connection",
  "conn.loading": "Loading connections…",
  "conn.loadFailed": "Could not load the connections: {msg}",
  "conn.listLabel": "Saved connections",
  "onboard.title": "Get started",
  "onboard.step1": "<b>Add a connection.</b> Enter the connection string and what the AI may do with it. Read-only by default.",
  "onboard.step2": "<b>Connect your MCP client.</b> Claude Code, VS Code, Copilot or Gemini, each with its own token. <a href=\"#/clients\">See how</a>",
  "onboard.step3": "<b>Follow the audit log.</b> Every call is recorded: who, where, what and the result.",
  "onboard.cta": "Add your first connection",

  "risk.full": "Full access",
  "risk.write": "Read and write",
  "risk.read": "Read-only",
  "conn.disabled": "Disabled",
  "conn.noDescription": "No description. Say what this database holds so the AI picks the right connection.",
  "conn.limits": "up to {rows} rows · timeout {sec} s",
  "conn.visible": "Visible to the AI",
  "conn.hidden": "Hidden from the AI",
  "conn.permsLabel": "Permissions for {name}",
  "conn.aiCan": "The AI can:",
  "conn.selectAlways": "Reading is always allowed",
  "conn.permOn": "Allowed. Click to block.",
  "conn.permOff": "Blocked. Click to allow.",
  "conn.permAria": "{sql}: {label} on {name}",
  "conn.test": "Test",
  "conn.edit": "Edit",
  "conn.delete": "Delete",
  "conn.deleteAria": "Delete {name}",
  "activity.none": "No calls yet",
  "activity.calls": { one: "{n} call in 24 h", other: "{n} calls in 24 h" },
  "activity.denied": { one: "{n} denied", other: "{n} denied" },
  "activity.errors": { one: "{n} error", other: "{n} errors" },
  "activity.last": "last {when}",

  "op.insert": "Insert",
  "op.update": "Update",
  "op.delete": "Delete",
  "op.ddl": "Schema",
  "risk.delete": "The AI will be able to delete rows, documents and keys. Deleting cannot be undone: make sure the database user only has the grants it needs.",
  "risk.ddl": "The AI will be able to create, change and drop tables, run procedures and change permissions (CREATE, DROP, EXEC, GRANT, SET…). That is full power over the database.",
  "confirm.allowTitleOn": "Allow {sql} on {name}?",
  "confirm.allowTitle": "Allow {sql}?",
  "confirm.allowNote": "The change applies to the AI's next call and is recorded in the audit log.",
  "confirm.allowOk": "Allow {sql}",
  "confirm.deleteTitle": "Delete the connection {name}?",
  "confirm.deleteBody1": "The AI loses access to this database right away. The stored connection string is erased and cannot be recovered.",
  "confirm.deleteBody2": "The audit records of this connection stay available.",
  "confirm.deleteOk": "Delete connection",
  "confirm.discardTitle": "Discard your changes?",
  "confirm.discardBody": "What you typed in this form will be lost.",
  "confirm.discardOk": "Discard",
  "confirm.default": "Confirm",
  "common.cancel": "Cancel",
  "common.close": "Close",
  "common.copy": "Copy",
  "common.undo": "Undo",

  "toast.permChanged": "{sql} {state} on {name}.",
  "toast.allowed": "allowed",
  "toast.blocked": "blocked",
  "toast.visible": "Connection {name} is now visible to the AI.",
  "toast.hidden": "Connection {name} is now hidden from the AI.",
  "toast.testing": "Testing…",
  "toast.testOk": "{name}: connected in {ms}.",
  "toast.testFail": "{name}: could not connect. {msg}",
  "toast.deleted": "Connection {name} deleted.",
  "toast.updated": "Connection {name} updated.",
  "toast.created": "Connection {name} created. The AI can use it now.",
  "toast.copied": "Copied to the clipboard.",
  "toast.copyFailed": "The browser did not allow copying. Select the text and press Ctrl+C.",
  "error.offline": "No answer from the server. Check that Comporta is running and try again.",
  "error.status": "Error {status}",

  "form.newTitle": "New connection",
  "form.editTitle": "Edit {name}",
  "form.create": "Create connection",
  "form.save": "Save changes",
  "form.identity": "Identity",
  "form.type": "Database type",
  "form.name": "Name",
  "form.nameHint": "How the AI refers to this connection. Letters, digits and _ — spaces and hyphens become _.",
  "form.namePlaceholder": "e.g. payments_prod",
  "form.description": "Description for the AI",
  "form.optionalRecommended": "(optional, recommended)",
  "form.descriptionHint": "What this database holds. The AI uses this text to choose the right connection.",
  "form.descriptionPlaceholder": "e.g. Production payments: transactions, refunds and reconciliation.",
  "form.access": "Access",
  "form.dsn": "Connection string",
  "form.dsnKeep": "Leave blank to keep the current one. ",
  "form.dsnFormat": "Format: ",
  "form.show": "Show",
  "form.hide": "Hide",
  "form.dsnUserHint": "Use a database user with only the grants it needs: it is the last line of defense.",
  "form.database": "Default database",
  "form.databaseOptional": "(optional if it is in the URI)",
  "form.test": "Test connection",
  "form.testing": "Testing… (up to 15 s)",
  "form.testOk": "Connected in {ms}",
  "form.perms": "What the AI can do",
  "perm.read": "Read data",
  "perm.readHint": "Always allowed, inside a transaction that is rolled back.",
  "perm.always": "Always",
  "perm.insert": "Insert",
  "perm.insertHint": "Create rows, documents or keys.",
  "perm.update": "Update",
  "perm.updateHint": "Change rows, documents or keys that already exist.",
  "perm.delete": "Delete",
  "perm.deleteHint": "Removes data; there is no undo.",
  "perm.ddl": "Schema and admin",
  "perm.ddlHint": "CREATE, DROP, ALTER, EXEC, GRANT, SET… Full power over the database.",
  "form.limits": "Limits",
  "form.rows": "Rows per query",
  "form.rowsHint": "Up to {cap}, this server's ceiling.",
  "form.timeout": "Timeout, in seconds",
  "form.timeoutHint": "Up to 600.",
  "form.enabled": "Active connection",
  "form.enabledHint": "When off, the AI stops seeing it right away.",

  "valid.nameEmpty": "Give the connection a name. E.g. payments_prod",
  "valid.nameInvalid": "Use 2 to 63 characters: unaccented letters, digits and _, starting with a letter.",
  "valid.dsnTest": "Enter the connection string to test it.",
  "valid.dsnEmpty": "Enter the connection string.",
  // Server validation errors, by code (see web.go).
  "srv.name_invalid": "Use 2 to 63 characters: unaccented letters, digits and _, starting with a letter. E.g. payments_prod",
  "srv.name_taken": "There is already a connection named “{name}”. Choose another name.",
  "srv.type_invalid": "Unknown database type: {type}",
  "srv.dsn_required": "Enter the connection string. E.g. {example}",
  "srv.dsn_invalid": "Invalid connection string for {type}: {detail}",
  "srv.max_rows_cap": "This server's ceiling is {cap} rows per query.",
  "srv.timeout_max": "The maximum timeout is 600 seconds.",

  "audit.title": "Audit log",
  "audit.lede": "Every call from the AIs and every change made on this screen. Click a row to see the full command.",
  "audit.live": "Auto-refresh",
  "audit.export": "Export CSV",
  "audit.filtersLabel": "Filter the audit log",
  "audit.search": "Search the command, parameters or message",
  "audit.searchPlaceholder": "e.g. orders, DROP, timeout",
  "audit.result": "Result",
  "audit.connection": "Connection",
  "audit.tool": "Tool",
  "audit.operation": "Operation",
  "audit.since": "Since",
  "audit.all": "All",
  "audit.otherTools": "Other",
  "audit.adminTool": "Changes on this screen",
  "audit.connChange": "connection change",
  "audit.clear": "Clear filters",
  "audit.caption": "Audit records, newest first",
  "audit.when": "When",
  "audit.command": "Command",
  "audit.rows": "Rows",
  "audit.duration": "Duration",
  "audit.client": "Client",
  "audit.empty": "No records match these filters.",
  "audit.emptyHint": "Try another period or clear the filters.",
  "audit.more": "Load 100 more",
  "audit.retention": "Records are kept for {days} days.",
  "audit.count": { one: "{n} record, newest first.", other: "{n} records, newest first." },
  "audit.paused": " Auto-refresh is paused while older pages are open.",
  "audit.every5": " Refreshes every 5 s.",
  "audit.loadFailed": "Could not load the audit log: {msg}",
  "audit.adminChange": "change on this screen",
  "audit.details": "Details of record {id}",
  "audit.copyCommand": "Copy command",
  "audit.params": "Parameters",
  "audit.message": "Message",
  "audit.reason": "Reason",
  "audit.record": "Record",
  "audit.noDb": "no database",
  "status.ok": "OK",
  "status.denied": "Denied",
  "status.error": "Error",
  "time.now": "just now",
  "time.min": "{n} min ago",
  "time.h": "{n} h ago",
  "time.yesterday": "yesterday",
  "time.days": "{n} days ago",

  "clients.title": "Connect MCP clients",
  "clients.lede": "Each client uses its own token, set in <code>COMPORTA_MCP_TOKENS</code>. The token's label identifies the client in the audit log.",
  "clients.endpoint": "Server address",
  "clients.copyEndpoint": "Copy the server address",
  "clients.transport": "Streamable HTTP transport, with the header <code>Authorization: Bearer &lt;token&gt;</code>.",
  "clients.tokens": "Configured tokens:",
  "clients.vscode": "VS Code and GitHub Copilot",
  "clients.generic": "Copilot CLI, Gemini CLI and others",
  "clients.copyClaude": "Copy the Claude Code command",
  "clients.copyVscode": "Copy the VS Code configuration",
  "clients.copyGeneric": "Copy the generic configuration",
  "clients.token": "<YOUR_TOKEN>",
  "clients.seesTitle": "What the AI sees",
  "clients.sees1": "Tools are built from the active connections. <code>sql_query</code> lists only SQL connections; <code>sql_execute</code> appears only when some SQL connection allows writes; the <code>mongo_*</code> tools list only MongoDB connections.",
  "clients.sees2": "Each connection comes with its type, dialect, parameter style and <b>description</b>. The description is what makes the AI pick the right connection: say what is in that database.",
  "clients.sees3": "Clients that cache the tool list may need to reconnect after a change here. Permissions, however, are checked on every call.",
};

const pt = {
  "app.skip": "Pular para o conteúdo",
  "nav.label": "Principal",
  "nav.connections": "Conexões",
  "nav.audit": "Auditoria",
  "nav.clients": "Conectar clientes",
  "lang.label": "Idioma",
  "pulse.prefix": "Chamadas MCP nas últimas ",
  "pulse.calls": { one: "chamada", other: "chamadas" },
  "pulse.denied": { one: "negada", other: "negadas" },
  "pulse.errors": { one: "erro", other: "erros" },

  "conn.title": "Conexões",
  "conn.lede": "Cada conexão é um banco que a IA enxerga. Consultas de leitura estão sempre liberadas; escrever exige a permissão ligada aqui.",
  "conn.new": "Nova conexão",
  "conn.loading": "Carregando conexões…",
  "conn.loadFailed": "Não foi possível carregar as conexões: {msg}",
  "conn.listLabel": "Conexões cadastradas",
  "onboard.title": "Comece por aqui",
  "onboard.step1": "<b>Cadastre uma conexão.</b> Informe a connection string e o que a IA pode fazer nela. Por padrão, só leitura.",
  "onboard.step2": "<b>Conecte o cliente MCP.</b> Claude Code, VS Code, Copilot ou Gemini, com o token de cada um. <a href=\"#/clients\">Ver instruções</a>",
  "onboard.step3": "<b>Acompanhe na auditoria.</b> Toda chamada fica registrada: quem, onde, o quê e o resultado.",
  "onboard.cta": "Cadastrar a primeira conexão",

  "risk.full": "Acesso total",
  "risk.write": "Leitura e escrita",
  "risk.read": "Somente leitura",
  "conn.disabled": "Desativada",
  "conn.noDescription": "Sem descrição. Descreva o que há neste banco para a IA escolher a conexão certa.",
  "conn.limits": "até {rows} linhas · timeout {sec} s",
  "conn.visible": "Visível para a IA",
  "conn.hidden": "Oculta da IA",
  "conn.permsLabel": "Permissões de {name}",
  "conn.aiCan": "A IA pode:",
  "conn.selectAlways": "Leitura está sempre liberada",
  "conn.permOn": "Liberado. Clique para bloquear.",
  "conn.permOff": "Bloqueado. Clique para liberar.",
  "conn.permAria": "{sql}: {label} em {name}",
  "conn.test": "Testar",
  "conn.edit": "Editar",
  "conn.delete": "Excluir",
  "conn.deleteAria": "Excluir {name}",
  "activity.none": "Nenhuma chamada ainda",
  "activity.calls": { one: "{n} chamada em 24 h", other: "{n} chamadas em 24 h" },
  "activity.denied": { one: "{n} negada", other: "{n} negadas" },
  "activity.errors": { one: "{n} erro", other: "{n} erros" },
  "activity.last": "última {when}",

  "op.insert": "Inserir",
  "op.update": "Alterar",
  "op.delete": "Apagar",
  "op.ddl": "Estrutura",
  "risk.delete": "A IA poderá apagar linhas, documentos e chaves. Apagar não tem desfazer: confira se o usuário do banco tem só os grants necessários.",
  "risk.ddl": "A IA poderá criar, alterar e apagar tabelas, executar procedures e mudar permissões (CREATE, DROP, EXEC, GRANT, SET…). É poder total sobre o banco.",
  "confirm.allowTitleOn": "Liberar {sql} em {name}?",
  "confirm.allowTitle": "Liberar {sql}?",
  "confirm.allowNote": "A mudança vale na próxima chamada da IA e fica registrada na auditoria.",
  "confirm.allowOk": "Liberar {sql}",
  "confirm.deleteTitle": "Excluir a conexão {name}?",
  "confirm.deleteBody1": "A IA perde o acesso a este banco na hora. A connection string guardada é apagada e não dá para recuperar.",
  "confirm.deleteBody2": "Os registros de auditoria da conexão continuam disponíveis.",
  "confirm.deleteOk": "Excluir conexão",
  "confirm.discardTitle": "Descartar as alterações?",
  "confirm.discardBody": "O que foi digitado neste formulário será perdido.",
  "confirm.discardOk": "Descartar",
  "confirm.default": "Confirmar",
  "common.cancel": "Cancelar",
  "common.close": "Fechar",
  "common.copy": "Copiar",
  "common.undo": "Desfazer",

  "toast.permChanged": "{sql} {state} em {name}.",
  "toast.allowed": "liberado",
  "toast.blocked": "bloqueado",
  "toast.visible": "Conexão {name} visível para a IA.",
  "toast.hidden": "Conexão {name} oculta da IA.",
  "toast.testing": "Testando…",
  "toast.testOk": "{name}: conectou em {ms}.",
  "toast.testFail": "{name}: não conectou. {msg}",
  "toast.deleted": "Conexão {name} excluída.",
  "toast.updated": "Conexão {name} atualizada.",
  "toast.created": "Conexão {name} criada. A IA já pode usá-la.",
  "toast.copied": "Copiado para a área de transferência.",
  "toast.copyFailed": "O navegador não deixou copiar. Selecione o texto e use Ctrl+C.",
  "error.offline": "Sem resposta do servidor. Confira se o Comporta está no ar e tente de novo.",
  "error.status": "Erro {status}",

  "form.newTitle": "Nova conexão",
  "form.editTitle": "Editar {name}",
  "form.create": "Criar conexão",
  "form.save": "Salvar alterações",
  "form.identity": "Identificação",
  "form.type": "Tipo de banco",
  "form.name": "Nome",
  "form.nameHint": "É como a IA chama a conexão. Letras, números e _ — espaços e hífens viram _.",
  "form.namePlaceholder": "ex.: pagamentos_prod",
  "form.description": "Descrição para a IA",
  "form.optionalRecommended": "(opcional, recomendada)",
  "form.descriptionHint": "O que há neste banco. A IA usa este texto para escolher a conexão certa.",
  "form.descriptionPlaceholder": "ex.: Pagamentos de produção: transações, estornos e conciliação.",
  "form.access": "Acesso",
  "form.dsn": "Connection string",
  "form.dsnKeep": "Deixe em branco para manter a atual. ",
  "form.dsnFormat": "Formato: ",
  "form.show": "Mostrar",
  "form.hide": "Ocultar",
  "form.dsnUserHint": "Use um usuário do banco só com os grants necessários: ele é a última barreira.",
  "form.database": "Database padrão",
  "form.databaseOptional": "(opcional se já estiver na URI)",
  "form.test": "Testar conexão",
  "form.testing": "Testando… (até 15 s)",
  "form.testOk": "Conectou em {ms}",
  "form.perms": "O que a IA pode fazer",
  "perm.read": "Ler dados",
  "perm.readHint": "Sempre liberado, em transação que é desfeita.",
  "perm.always": "Sempre",
  "perm.insert": "Inserir",
  "perm.insertHint": "Criar linhas, documentos ou chaves.",
  "perm.update": "Alterar",
  "perm.updateHint": "Mudar linhas, documentos ou chaves que já existem.",
  "perm.delete": "Apagar",
  "perm.deleteHint": "Remove dados; não tem desfazer.",
  "perm.ddl": "Estrutura e administração",
  "perm.ddlHint": "CREATE, DROP, ALTER, EXEC, GRANT, SET… Poder total no banco.",
  "form.limits": "Limites",
  "form.rows": "Linhas por consulta",
  "form.rowsHint": "Até {cap}, o teto deste servidor.",
  "form.timeout": "Timeout, em segundos",
  "form.timeoutHint": "Até 600.",
  "form.enabled": "Conexão ativa",
  "form.enabledHint": "Desligada, a IA deixa de enxergá-la na hora.",

  "valid.nameEmpty": "Dê um nome à conexão. Ex.: pagamentos_prod",
  "valid.nameInvalid": "Use de 2 a 63 caracteres: letras sem acento, números e _, começando por letra.",
  "valid.dsnTest": "Informe a connection string para testar.",
  "valid.dsnEmpty": "Informe a connection string.",
  "srv.name_invalid": "Use de 2 a 63 caracteres: letras sem acento, números e _, começando por letra. Ex.: pagamentos_prod",
  "srv.name_taken": "Já existe uma conexão chamada “{name}”. Escolha outro nome.",
  "srv.type_invalid": "Tipo de banco desconhecido: {type}",
  "srv.dsn_required": "Informe a connection string. Ex.: {example}",
  "srv.dsn_invalid": "Connection string inválida para {type}: {detail}",
  "srv.max_rows_cap": "O teto deste servidor é {cap} linhas por consulta.",
  "srv.timeout_max": "O timeout máximo é 600 segundos.",

  "audit.title": "Auditoria",
  "audit.lede": "Toda chamada das IAs e toda mudança feita nesta tela. Clique numa linha para ver o comando completo.",
  "audit.live": "Atualizar sozinho",
  "audit.export": "Exportar CSV",
  "audit.filtersLabel": "Filtrar auditoria",
  "audit.search": "Buscar no comando, parâmetros ou mensagem",
  "audit.searchPlaceholder": "ex.: pedidos, DROP, timeout",
  "audit.result": "Resultado",
  "audit.connection": "Conexão",
  "audit.tool": "Ferramenta",
  "audit.operation": "Operação",
  "audit.since": "Desde",
  "audit.all": "Todos",
  "audit.otherTools": "Outras",
  "audit.adminTool": "Mudanças nesta tela",
  "audit.connChange": "mudança de conexão",
  "audit.clear": "Limpar filtros",
  "audit.caption": "Registros de auditoria, do mais recente para o mais antigo",
  "audit.when": "Quando",
  "audit.command": "Comando",
  "audit.rows": "Linhas",
  "audit.duration": "Duração",
  "audit.client": "Cliente",
  "audit.empty": "Nenhum registro com esses filtros.",
  "audit.emptyHint": "Tente outro período ou limpe os filtros.",
  "audit.more": "Carregar mais 100",
  "audit.retention": "Os registros ficam guardados por {days} dias.",
  "audit.count": { one: "{n} registro, do mais recente para o mais antigo.", other: "{n} registros, do mais recente para o mais antigo." },
  "audit.paused": " Atualização automática pausada enquanto houver páginas antigas abertas.",
  "audit.every5": " Atualiza a cada 5 s.",
  "audit.loadFailed": "Não foi possível carregar a auditoria: {msg}",
  "audit.adminChange": "mudança na tela",
  "audit.details": "Detalhes do registro {id}",
  "audit.copyCommand": "Copiar comando",
  "audit.params": "Parâmetros",
  "audit.message": "Mensagem",
  "audit.reason": "Motivo",
  "audit.record": "Registro",
  "audit.noDb": "sem banco",
  "status.ok": "OK",
  "status.denied": "Negado",
  "status.error": "Erro",
  "time.now": "agora",
  "time.min": "há {n} min",
  "time.h": "há {n} h",
  "time.yesterday": "ontem",
  "time.days": "há {n} dias",

  "clients.title": "Conectar clientes MCP",
  "clients.lede": "Cada cliente usa um token próprio, definido em <code>COMPORTA_MCP_TOKENS</code>. O rótulo do token identifica o cliente na auditoria.",
  "clients.endpoint": "Endereço do servidor",
  "clients.copyEndpoint": "Copiar endereço do servidor",
  "clients.transport": "Transporte Streamable HTTP, com o cabeçalho <code>Authorization: Bearer &lt;token&gt;</code>.",
  "clients.tokens": "Tokens configurados:",
  "clients.vscode": "VS Code e GitHub Copilot",
  "clients.generic": "Copilot CLI, Gemini CLI e outros",
  "clients.copyClaude": "Copiar comando do Claude Code",
  "clients.copyVscode": "Copiar configuração do VS Code",
  "clients.copyGeneric": "Copiar configuração genérica",
  "clients.token": "<SEU_TOKEN>",
  "clients.seesTitle": "O que a IA enxerga",
  "clients.sees1": "As ferramentas são montadas a partir das conexões ativas. <code>sql_query</code> lista só conexões SQL; <code>sql_execute</code> só aparece quando alguma conexão SQL tem escrita liberada; as ferramentas <code>mongo_*</code> listam só conexões MongoDB.",
  "clients.sees2": "Cada conexão vai com o tipo, o dialeto, o estilo de parâmetro e a <b>descrição</b>. É a descrição que faz a IA escolher a conexão certa: diga o que há naquele banco.",
  "clients.sees3": "Clientes que guardam a lista de ferramentas podem precisar reconectar depois de uma mudança aqui. As permissões, porém, são conferidas a cada chamada.",
};

export const DICTIONARIES = { en, pt };
export const LANGUAGES = [
  { code: "en", label: "English", locale: "en-US" },
  { code: "pt", label: "Português", locale: "pt-BR" },
];
const STORAGE_KEY = "comporta.lang";

let current = "en";

// detect picks the saved choice, then the browser's languages, then English.
function detect() {
  try {
    const saved = localStorage.getItem(STORAGE_KEY);
    if (saved && DICTIONARIES[saved]) return saved;
  } catch {
    // storage blocked: fall through to the browser languages
  }
  for (const tag of navigator.languages || [navigator.language || ""]) {
    const base = tag.toLowerCase().split("-")[0];
    if (DICTIONARIES[base]) return base;
  }
  return "en";
}

export function lang() {
  return current;
}

export function locale() {
  return LANGUAGES.find((l) => l.code === current).locale;
}

export function setLang(code, { remember = true } = {}) {
  current = DICTIONARIES[code] ? code : "en";
  document.documentElement.lang = locale();
  if (remember) {
    try {
      localStorage.setItem(STORAGE_KEY, current);
    } catch {
      // the choice just won't survive a reload
    }
  }
}

setLang(detect(), { remember: false });

const fill = (text, vars) => text.replace(/\{(\w+)\}/g, (m, k) => (k in vars ? String(vars[k]) : m));

// t returns the text for key in the current language (English if missing),
// with {placeholders} filled from vars. A plural entry uses vars.n.
export function t(key, vars = {}) {
  let entry = DICTIONARIES[current][key] ?? en[key];
  if (entry === undefined) return key;
  if (typeof entry === "object") {
    const n = Number(vars.n ?? 0);
    // CLDR puts 0 in Brazilian Portuguese's "one"; people say "0 chamadas".
    const form = n === 0 ? "other" : new Intl.PluralRules(locale()).select(n);
    entry = entry[form] ?? entry.other;
    vars = { ...vars, n: n.toLocaleString(locale()) };
  }
  return fill(entry, vars);
}

// applyStatic translates the marked elements under root.
export function applyStatic(root = document) {
  for (const el of root.querySelectorAll("[data-i18n]")) el.textContent = t(el.dataset.i18n);
  // Markup here comes from the dictionaries above, never from the API.
  for (const el of root.querySelectorAll("[data-i18n-html]")) el.innerHTML = t(el.dataset.i18nHtml);
  for (const el of root.querySelectorAll("[data-i18n-attr]")) {
    for (const pair of el.dataset.i18nAttr.split(";")) {
      const [attr, key] = pair.split(":");
      el.setAttribute(attr.trim(), t(key.trim()));
    }
  }
}
