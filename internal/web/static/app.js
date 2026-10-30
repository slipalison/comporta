// Comporta admin UI. No framework and no build step: the file is embedded in
// the Go binary and served under a CSP without 'unsafe-inline', so all markup
// is built here and every value from the API goes through esc().

const $ = (sel, el = document) => el.querySelector(sel);
const $$ = (sel, el = document) => [...el.querySelectorAll(sel)];
const esc = (s) => String(s ?? "").replace(/[&<>"']/g, (c) => ({ "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;", "'": "&#39;" })[c]);
// iconNode is icon() as a DOM node, for text set with textContent.
function iconNode(name) {
  const svg = document.createElementNS("http://www.w3.org/2000/svg", "svg");
  svg.setAttribute("class", "icon");
  svg.setAttribute("aria-hidden", "true");
  const use = document.createElementNS("http://www.w3.org/2000/svg", "use");
  use.setAttribute("href", `#i-${name}`);
  svg.append(use);
  return svg;
}

// showMessage replaces el's content with an icon and text. Text from the
// server goes in as a text node, never as markup.
function showMessage(el, iconName, text) {
  el.replaceChildren(iconNode(iconName), document.createTextNode(` ${text}`));
}

const icon = (name, cls = "icon") => `<svg class="${cls}" aria-hidden="true"><use href="#i-${name}"/></svg>`;

const state = {
  meta: { db_types: {}, mcp_clients: [] },
  conns: [],
  activity: {},
  route: "conexoes",
  auditCursor: 0,
  auditPages: 0,
  auditTimer: null,
  statsTimer: null,
  formDirty: false,
  editing: null,
};

const OPS = [
  { field: "allow_insert", key: "insert", label: "Inserir", sql: "INSERT" },
  { field: "allow_update", key: "update", label: "Alterar", sql: "UPDATE" },
  { field: "allow_delete", key: "delete", label: "Apagar", sql: "DELETE", risky: true },
  { field: "allow_ddl", key: "ddl", label: "Estrutura", sql: "DDL", risky: true },
];

const RISK = {
  delete: "A IA poderá apagar linhas e documentos. Apagar não tem desfazer: confira se o usuário do banco tem só os grants necessários.",
  ddl: "A IA poderá criar, alterar e apagar tabelas, executar procedures e mudar permissões (CREATE, DROP, EXEC, GRANT, SET…). É poder total sobre o banco.",
};

// --------------------------------------------------------------------- api
class ApiError extends Error {
  constructor(message, field, status) {
    super(message);
    this.field = field;
    this.status = status;
  }
}

async function api(method, path, body) {
  const opts = { method, headers: {} };
  if (body !== undefined) {
    opts.headers["Content-Type"] = "application/json";
    opts.body = JSON.stringify(body);
  }
  let res;
  try {
    res = await fetch(path, opts);
  } catch {
    throw new ApiError("Sem resposta do servidor. Confira se o Comporta está no ar e tente de novo.");
  }
  if (res.status === 204) return null;
  const data = await res.json().catch(() => ({}));
  if (!res.ok) throw new ApiError(data.error || `Erro ${res.status}`, data.field, res.status);
  return data;
}

// ------------------------------------------------------------------ format
const fmtNumber = (n) => Number(n || 0).toLocaleString("pt-BR");
const plural = (n, one, many) => `${fmtNumber(n)} ${n === 1 ? one : many}`;

function fmtAbsolute(ts) {
  const d = new Date(ts);
  return d.toLocaleString("pt-BR", { day: "2-digit", month: "2-digit", year: "numeric", hour: "2-digit", minute: "2-digit", second: "2-digit" });
}

function fmtRelative(ts) {
  const sec = Math.round((Date.now() - new Date(ts).getTime()) / 1000);
  if (sec < 45) return "agora";
  const min = Math.round(sec / 60);
  if (min < 60) return `há ${min} min`;
  const h = Math.round(min / 60);
  if (h < 24) return `há ${h} h`;
  const d = Math.round(h / 24);
  return d === 1 ? "ontem" : `há ${d} dias`;
}

function fmtDuration(ms) {
  if (ms < 1000) return `${ms} ms`;
  return `${(ms / 1000).toLocaleString("pt-BR", { maximumFractionDigits: 1 })} s`;
}

// Same rule as web.NormalizeName on the server: lower case, spaces, dots and
// hyphens become "_".
const normalizeName = (s) => s.trim().toLowerCase().replace(/[\s.-]+/g, "_");
const NAME_RE = /^[a-z][a-z0-9_]{1,62}$/;

// ------------------------------------------------------------------ toasts
function toast(message, { error = false, action } = {}) {
  const el = document.createElement("div");
  el.className = "toast" + (error ? " is-error" : "");
  if (error) el.setAttribute("role", "alert");
  const text = document.createElement("span");
  text.textContent = message;
  el.append(text);
  if (action) {
    const b = document.createElement("button");
    b.type = "button";
    b.className = "btn btn-sm";
    b.textContent = action.label;
    b.addEventListener("click", () => {
      el.remove();
      action.run();
    });
    el.append(b);
  }
  $("#toasts").append(el);
  setTimeout(() => el.remove(), error || action ? 8000 : 4000);
}

// ----------------------------------------------------------------- confirm
function confirmDialog({ title, body, ok }) {
  const dlg = $("#dlg-confirm");
  $("#confirm-title").textContent = title;
  $("#confirm-body").innerHTML = body;
  $("#confirm-ok").textContent = ok;
  dlg.returnValue = "";
  dlg.showModal();
  $("#confirm-cancel").focus();
  return new Promise((resolve) => {
    dlg.addEventListener("close", () => resolve(dlg.returnValue === "ok"), { once: true });
  });
}

// ------------------------------------------------------------------ router
const ROUTES = ["conexoes", "auditoria", "clientes"];
const ROUTE_SET = new Set(ROUTES);

function parseHash() {
  const [path, query = ""] = location.hash.replace(/^#\/?/, "").split("?");
  return { route: ROUTE_SET.has(path) ? path : "conexoes", params: new URLSearchParams(query) };
}

function navigate(fromUser) {
  const { route, params } = parseHash();
  const changed = route !== state.route;
  state.route = route;
  for (const r of ROUTES) $(`#view-${r}`).hidden = r !== route;
  for (const a of $$(".mainnav a")) {
    if (a.dataset.route === route) a.setAttribute("aria-current", "page");
    else a.removeAttribute("aria-current");
  }
  document.title = { conexoes: "Conexões", auditoria: "Auditoria", clientes: "Conectar clientes" }[route] + " · Comporta";
  if (route === "auditoria") {
    applyFiltersFromParams(params);
    void loadAudit(true);
  }
  scheduleAudit();
  // Moving focus to the new heading tells screen reader users the page changed.
  if (fromUser && changed) $(`#h-${route}`).focus({ preventScroll: true });
}

// ------------------------------------------------------------------- stats
async function loadStats() {
  try {
    const s = await api("GET", "api/audit/stats");
    state.activity = s.connections || {};
    const set = (id, n, one, many) => {
      const el = $(id);
      el.innerHTML = `<b>${fmtNumber(n)}</b> ${n === 1 ? one : many}`;
      el.classList.toggle("has-value", n > 0);
    };
    set("#pulse-total", s.total, "chamada", "chamadas");
    set("#pulse-denied", s.denied, "negada", "negadas");
    set("#pulse-error", s.error, "erro", "erros");
    $("#pulse").hidden = false;
    if (state.route === "conexoes") renderActivity();
  } catch {
    // The header summary is a convenience: a failed refresh keeps the last values.
  }
}

// ------------------------------------------------------------- connections
function riskOf(c) {
  if (c.allow_ddl) return { cls: "badge-risk-full", icon: "alert", text: "Acesso total" };
  if (c.allow_insert || c.allow_update || c.allow_delete) return { cls: "badge-risk-write", icon: "edit", text: "Leitura e escrita" };
  return { cls: "badge-risk-read", icon: "lock", text: "Somente leitura" };
}

function activityHTML(c) {
  const a = state.activity[c.name];
  if (!a?.last_call) return `<span>Nenhuma chamada ainda</span>`;
  const parts = [`<span>${plural(a.calls_24h, "chamada", "chamadas")} em 24 h</span>`];
  if (a.denied_24h) parts.push(`<span class="is-denied">${icon("deny")} ${plural(a.denied_24h, "negada", "negadas")}</span>`);
  if (a.errors_24h) parts.push(`<span class="is-error">${icon("error")} ${plural(a.errors_24h, "erro", "erros")}</span>`);
  parts.push(`<span title="${esc(fmtAbsolute(a.last_call))}">última ${esc(fmtRelative(a.last_call))}</span>`);
  return parts.join("");
}

function connCard(c) {
  const info = state.meta.db_types[c.type] || {};
  const risk = riskOf(c);
  const chips = OPS.map((op) => {
    const on = !!c[op.field];
    return `<button type="button" class="chip${op.risky ? " is-risky" : ""}" data-act="perm" data-field="${op.field}" aria-pressed="${on}"
      aria-label="${op.sql}: ${esc(op.label.toLowerCase())} em ${esc(c.name)}" title="${on ? "Liberado. Clique para bloquear." : "Bloqueado. Clique para liberar."}">${icon("check")}${op.sql}</button>`;
  }).join("");
  return `<li class="conn${c.enabled ? "" : " is-off"}" data-id="${c.id}">
    <div class="conn-main">
      <div class="conn-title">
        <h2 class="conn-name">${esc(c.name)}</h2>
        <span class="badge badge-type">${icon("db")}${esc(info.label || c.type)}</span>
        <span class="badge ${risk.cls}">${icon(risk.icon)}${risk.text}</span>
        ${c.enabled ? "" : `<span class="badge">Desativada</span>`}
      </div>
      <p class="conn-desc${c.description ? "" : " is-missing"}">${c.description ? esc(c.description) : "Sem descrição. Descreva o que há neste banco para a IA escolher a conexão certa."}</p>
      <div class="conn-meta">
        <code class="conn-dsn" title="${esc(c.dsn_masked)}">${esc(c.dsn_masked)}</code>
        ${c.database ? `<span>database: <code>${esc(c.database)}</code></span>` : ""}
        <span>até ${fmtNumber(c.max_rows)} linhas · timeout ${c.timeout_sec} s</span>
      </div>
    </div>
    <div class="conn-state">
      <label class="switch-label">
        <input type="checkbox" class="switch" data-act="enabled" ${c.enabled ? "checked" : ""} aria-describedby="state-${c.id}">
        <span id="state-${c.id}">${c.enabled ? "Visível para a IA" : "Oculta da IA"}</span>
      </label>
    </div>
    <div class="conn-perms" role="group" aria-label="Permissões de ${esc(c.name)}">
      <span class="conn-perms-label">A IA pode:</span>
      <span class="chip is-locked" title="Leitura está sempre liberada">${icon("check")}SELECT</span>
      ${chips}
    </div>
    <div class="conn-foot">
      <div class="conn-activity" data-activity>${activityHTML(c)}</div>
      <span class="spacer"></span>
      <button type="button" class="btn btn-sm" data-act="test">${icon("plug")}Testar</button>
      <button type="button" class="btn btn-sm" data-act="edit">${icon("edit")}Editar</button>
      <button type="button" class="btn btn-sm btn-quiet-danger" data-act="delete" aria-label="Excluir ${esc(c.name)}">${icon("trash")}Excluir</button>
    </div>
  </li>`;
}

function renderConnections() {
  $("#conn-loading").hidden = true;
  $("#conn-empty").hidden = state.conns.length > 0;
  $("#conn-list").innerHTML = state.conns.map(connCard).join("");
  const sel = $("#f-connection");
  const cur = sel.value;
  sel.innerHTML = `<option value="">Todas</option>` + state.conns.map((c) => `<option>${esc(c.name)}</option>`).join("");
  sel.value = cur;
}

function renderActivity() {
  for (const li of $$("#conn-list .conn")) {
    const c = state.conns.find((x) => x.id === Number(li.dataset.id));
    if (c) $("[data-activity]", li).innerHTML = activityHTML(c);
  }
}

async function loadConnections() {
  try {
    state.conns = await api("GET", "api/connections");
    renderConnections();
  } catch (err) {
    showMessage($("#conn-loading"), "error", `Não foi possível carregar as conexões: ${err.message}`);
  }
}

function payload(c) {
  return {
    name: c.name, type: c.type, description: c.description, dsn: "", database: c.database, enabled: c.enabled,
    allow_insert: c.allow_insert, allow_update: c.allow_update, allow_delete: c.allow_delete, allow_ddl: c.allow_ddl,
    max_rows: c.max_rows, timeout_sec: c.timeout_sec,
  };
}

// putField saves one field of a connection and offers to undo it.
async function putField(c, field, value, control, { undo = true } = {}) {
  const body = payload(c);
  body[field] = value;
  control.setAttribute("aria-busy", "true");
  control.disabled = true;
  try {
    const saved = await api("PUT", `api/connections/${c.id}`, body);
    Object.assign(c, saved);
    renderConnections();
    const op = OPS.find((o) => o.field === field);
    let what = value ? "Conexão visível para a IA" : "Conexão oculta da IA";
    if (op) what = `${op.sql} ${value ? "liberado" : "bloqueado"}`;
    toast(`${what} em ${c.name}.`, undo ? { action: { label: "Desfazer", run: () => undoField(c.id, field, !value) } } : {});
    void loadStats();
  } catch (err) {
    control.removeAttribute("aria-busy");
    control.disabled = false;
    if (control.type === "checkbox") control.checked = !value;
    toast(err.message, { error: true });
  }
}

function undoField(id, field, value) {
  const c = state.conns.find((x) => x.id === id);
  const li = $(`#conn-list .conn[data-id="${id}"]`);
  if (!c || !li) return;
  const control = field === "enabled" ? $('[data-act="enabled"]', li) : $(`[data-field="${field}"]`, li);
  void putField(c, field, value, control, { undo: false });
}

async function togglePerm(c, field, chip) {
  const op = OPS.find((o) => o.field === field);
  const next = !c[field];
  if (next && op.risky) {
    const ok = await confirmDialog({
      title: `Liberar ${op.sql} em ${c.name}?`,
      body: `<p>${esc(RISK[op.key])}</p><p>A mudança vale na próxima chamada da IA e fica registrada na auditoria.</p>`,
      ok: `Liberar ${op.sql}`,
    });
    if (!ok) return chip.focus();
  }
  await putField(c, field, next, chip);
  $(`#conn-list .conn[data-id="${c.id}"] [data-field="${field}"]`)?.focus();
}

async function testSaved(c, button) {
  button.setAttribute("aria-busy", "true");
  button.disabled = true;
  const label = button.innerHTML;
  button.innerHTML = `<span class="spinner" aria-hidden="true"></span>Testando…`;
  try {
    const r = await api("POST", "api/test", { id: c.id, type: c.type, database: c.database });
    if (r.ok) toast(`${c.name}: conectou em ${fmtDuration(r.ms)}.`);
    else toast(`${c.name}: não conectou. ${r.error}`, { error: true });
  } catch (err) {
    toast(err.message, { error: true });
  } finally {
    button.innerHTML = label;
    button.removeAttribute("aria-busy");
    button.disabled = false;
  }
}

async function deleteConn(c) {
  const ok = await confirmDialog({
    title: `Excluir a conexão ${c.name}?`,
    body: `<p>A IA perde o acesso a este banco na hora. A connection string guardada é apagada e não dá para recuperar.</p>
      <p>Os registros de auditoria da conexão continuam disponíveis.</p>`,
    ok: "Excluir conexão",
  });
  if (!ok) return;
  try {
    await api("DELETE", `api/connections/${c.id}`);
    toast(`Conexão ${c.name} excluída.`);
    await loadConnections();
    $("#new-conn").focus();
  } catch (err) {
    toast(err.message, { error: true });
  }
}

$("#conn-list").addEventListener("click", (e) => {
  const el = e.target.closest("[data-act]");
  if (!el || el.dataset.act === "enabled") return;
  const c = state.conns.find((x) => x.id === Number(el.closest(".conn").dataset.id));
  if (!c) return;
  switch (el.dataset.act) {
    case "perm": return togglePerm(c, el.dataset.field, el);
    case "test": return testSaved(c, el);
    case "edit": return openForm(c);
    case "delete": return deleteConn(c);
  }
});

$("#conn-list").addEventListener("change", (e) => {
  if (e.target.dataset.act !== "enabled") return;
  const c = state.conns.find((x) => x.id === Number(e.target.closest(".conn").dataset.id));
  if (c) void putField(c, "enabled", e.target.checked, e.target);
});

$("#view-conexoes").addEventListener("click", (e) => {
  if (e.target.closest('[data-act="new"]')) openForm();
});
$("#new-conn").addEventListener("click", () => openForm());

// -------------------------------------------------------------------- form
const form = $("#conn-form");
const dlg = $("#dlg-conn");

function fillTypes() {
  $("#c-type").innerHTML = Object.entries(state.meta.db_types)
    .sort((a, b) => a[1].label.localeCompare(b[1].label))
    .map(([k, v]) => `<option value="${esc(k)}">${esc(v.label)}</option>`)
    .join("");
}

function onTypeChange() {
  const t = $("#c-type").value;
  const info = state.meta.db_types[t] || {};
  const keep = state.editing ? "Deixe em branco para manter a atual. " : "";
  $("#c-dsn-hint").innerHTML = `${keep}Formato: <code>${esc(info.dsn_example || "")}</code>`;
  $("#db-field").hidden = t !== "mongodb";
  clearTest();
}

function setFieldError(name, message) {
  const map = { name: "c-name", dsn: "c-dsn", max_rows: "c-rows", timeout_sec: "c-timeout", type: "c-type" };
  const id = map[name];
  const input = id && $(`#${id}`);
  const box = id && $(`#${id}-err`);
  if (!input) return false;
  if (message) input.setAttribute("aria-invalid", "true");
  else input.removeAttribute("aria-invalid");
  if (box) {
    box.textContent = message || "";
    box.hidden = !message;
  }
  return true;
}

function clearErrors() {
  for (const f of ["name", "dsn", "max_rows", "timeout_sec", "type"]) setFieldError(f, "");
  $("#form-error").hidden = true;
}

function clearTest() {
  const r = $("#test-result");
  r.className = "test-result";
  r.textContent = "";
}

function openForm(c) {
  state.editing = c || null;
  clearErrors();
  form.reset();
  $("#dlg-conn-title").textContent = c ? `Editar ${c.name}` : "Nova conexão";
  $("#save-btn").textContent = c ? "Salvar alterações" : "Criar conexão";
  $("#c-type").value = c?.type || "postgres";
  $("#c-name").value = c?.name || "";
  $("#c-desc").value = c?.description || "";
  $("#c-dsn").value = "";
  $("#c-dsn").required = !c;
  setDsnVisible(false);
  $("#c-db").value = c?.database || "";
  $("#c-rows").value = c?.max_rows || Math.min(100, state.meta.max_rows_cap || 100);
  $("#c-rows").max = state.meta.max_rows_cap;
  $("#c-rows-hint").textContent = `Até ${fmtNumber(state.meta.max_rows_cap)}, o teto deste servidor.`;
  $("#c-timeout").value = c?.timeout_sec || 30;
  for (const op of OPS) $(`#c-${op.key}`).checked = !!c?.[op.field];
  $("#c-enabled").checked = c ? c.enabled : true;
  onTypeChange();
  state.formDirty = false;
  dlg.showModal();
  (c ? $("#c-desc") : $("#c-type")).focus();
}

function formBody() {
  const type = $("#c-type").value;
  return {
    name: normalizeName($("#c-name").value), type, description: $("#c-desc").value.trim(), dsn: $("#c-dsn").value.trim(),
    database: type === "mongodb" ? $("#c-db").value.trim() : "", enabled: $("#c-enabled").checked,
    allow_insert: $("#c-insert").checked, allow_update: $("#c-update").checked,
    allow_delete: $("#c-delete").checked, allow_ddl: $("#c-ddl").checked,
    max_rows: Number($("#c-rows").value), timeout_sec: Number($("#c-timeout").value),
  };
}

function validateName({ quiet = false } = {}) {
  const input = $("#c-name");
  const value = normalizeName(input.value);
  if (value !== input.value && value) input.value = value;
  let problem = "";
  if (!value) problem = "Dê um nome à conexão. Ex.: pagamentos_prod";
  else if (!NAME_RE.test(value)) problem = "Use de 2 a 63 caracteres: letras sem acento, números e _, começando por letra.";
  if (problem) {
    if (!quiet) setFieldError("name", problem);
    return false;
  }
  setFieldError("name", "");
  return true;
}

function setDsnVisible(show) {
  const b = $("#toggle-dsn");
  $("#c-dsn").type = show ? "text" : "password";
  b.setAttribute("aria-pressed", String(show));
  $("span", b).textContent = show ? "Ocultar" : "Mostrar";
}

$("#c-type").addEventListener("change", onTypeChange);
$("#c-name").addEventListener("blur", () => { if ($("#c-name").value) validateName(); });
$("#c-name").addEventListener("input", () => { if ($("#c-name").getAttribute("aria-invalid")) validateName({ quiet: true }); });
$("#c-dsn").addEventListener("input", () => { setFieldError("dsn", ""); clearTest(); });
$("#toggle-dsn").addEventListener("click", () => setDsnVisible($("#c-dsn").type === "password"));
form.addEventListener("input", () => { state.formDirty = true; });

// Turning on a risky permission in the form explains what it means.
for (const op of OPS.filter((o) => o.risky)) {
  $(`#c-${op.key}`).addEventListener("change", async (e) => {
    if (!e.target.checked) return;
    const ok = await confirmDialog({ title: `Liberar ${op.sql}?`, body: `<p>${esc(RISK[op.key])}</p>`, ok: `Liberar ${op.sql}` });
    e.target.checked = ok;
    e.target.focus();
  });
}

$("#test-btn").addEventListener("click", async () => {
  const r = $("#test-result");
  const b = formBody();
  if (!b.dsn && !state.editing) {
    setFieldError("dsn", "Informe a connection string para testar.");
    return $("#c-dsn").focus();
  }
  const btn = $("#test-btn");
  btn.setAttribute("aria-busy", "true");
  btn.disabled = true;
  r.className = "test-result";
  r.innerHTML = `<span class="spinner" aria-hidden="true"></span>Testando… (até 15 s)`;
  try {
    const res = await api("POST", "api/test", { id: state.editing?.id || 0, type: b.type, dsn: b.dsn, database: b.database });
    r.className = "test-result " + (res.ok ? "is-ok" : "is-error");
    if (res.ok) showMessage(r, "ok", `Conectou em ${fmtDuration(res.ms)}`);
    else showMessage(r, "error", res.error);
  } catch (err) {
    r.className = "test-result is-error";
    showMessage(r, "error", err.message);
  } finally {
    btn.removeAttribute("aria-busy");
    btn.disabled = false;
  }
});

form.addEventListener("submit", async (e) => {
  e.preventDefault();
  clearErrors();
  const body = formBody();
  if (!validateName()) return $("#c-name").focus();
  if (!body.dsn && !state.editing) {
    setFieldError("dsn", "Informe a connection string.");
    return $("#c-dsn").focus();
  }
  const save = $("#save-btn");
  save.setAttribute("aria-busy", "true");
  save.disabled = true;
  try {
    if (state.editing) await api("PUT", `api/connections/${state.editing.id}`, body);
    else await api("POST", "api/connections", body);
    state.formDirty = false;
    dlg.close();
    toast(state.editing ? `Conexão ${body.name} atualizada.` : `Conexão ${body.name} criada. A IA já pode usá-la.`);
    await loadConnections();
    void loadStats();
  } catch (err) {
    if (err.field && setFieldError(err.field, err.message)) {
      $(`[aria-invalid="true"]`, form)?.focus();
    } else {
      const box = $("#form-error");
      box.textContent = err.message;
      box.hidden = false;
      box.scrollIntoView({ block: "nearest" });
    }
  } finally {
    save.removeAttribute("aria-busy");
    save.disabled = false;
  }
});

async function closeForm() {
  if (state.formDirty) {
    const ok = await confirmDialog({ title: "Descartar as alterações?", body: "<p>O que foi digitado neste formulário será perdido.</p>", ok: "Descartar" });
    if (!ok) return;
  }
  state.formDirty = false;
  dlg.close();
}

for (const b of $$("[data-close]", dlg)) b.addEventListener("click", closeForm);
dlg.addEventListener("cancel", (e) => {
  e.preventDefault();
  void closeForm();
});

// ------------------------------------------------------------------- audit
const STATUS = {
  ok: { label: "OK", icon: "ok", cls: "badge-ok" },
  denied: { label: "Negado", icon: "deny", cls: "badge-denied" },
  error: { label: "Erro", icon: "error", cls: "badge-error" },
};
const FILTERS = ["q", "status", "connection", "tool", "operation", "since"];

function applyFiltersFromParams(params) {
  for (const k of FILTERS) {
    const el = $(`#f-${k}`);
    const v = params.get(k) || "";
    if (el.tagName === "SELECT" && v && ![...el.options].some((o) => o.value === v)) el.add(new Option(v, v));
    el.value = v;
  }
  $("#f-reset").hidden = !FILTERS.some((k) => params.get(k));
}

function filterParams() {
  const p = new URLSearchParams();
  for (const k of FILTERS) {
    const v = $(`#f-${k}`).value.trim();
    if (v) p.set(k, v);
  }
  return p;
}

// apiParams converts the form to what the API reads: `since` goes as an
// absolute instant, so the server's time zone does not shift the filter.
function apiParams() {
  const p = filterParams();
  if (p.get("since")) p.set("since", new Date(p.get("since")).toISOString());
  return p;
}

function syncHash() {
  const q = filterParams().toString();
  history.replaceState(null, "", "#/auditoria" + (q ? "?" + q : ""));
  $("#f-reset").hidden = !q;
}

function auditRow(e) {
  const st = STATUS[e.status] || { label: e.status, icon: "alert", cls: "" };
  const admin = e.tool === "admin";
  const open = state.openDetails.has(String(e.id));
  const messageLabel = e.status === "ok" ? "Mensagem" : "Motivo";
  return `<tr class="entry${open ? " is-open" : ""}" data-id="${e.id}">
    <td class="when c-when"><span title="${esc(fmtAbsolute(e.ts))}">${esc(fmtRelative(e.ts))}</span><small>${esc(new Date(e.ts).toLocaleTimeString("pt-BR"))}</small></td>
    <td class="c-status"><span class="badge ${st.cls}">${icon(st.icon)}${esc(st.label)}</span></td>
    <td data-label="Conexão"><code>${esc(e.connection) || "—"}</code></td>
    <td data-label="Ferramenta">${admin ? `<span class="tool-admin">mudança na tela</span>` : `<code>${esc(e.tool)}</code>`}<br><small class="muted">${esc(e.operation)}</small></td>
    <td class="c-stmt"><div class="stmt">
      <button type="button" class="disclose" aria-expanded="${open}" aria-controls="detail-${e.id}" aria-label="Detalhes do registro ${e.id}">${icon("chevron")}</button>
      <code title="${esc(e.statement)}">${esc(e.statement) || "—"}</code></div></td>
    <td class="num" data-label="Linhas">${fmtNumber(e.rows)}</td>
    <td class="num" data-label="Duração">${fmtDuration(e.duration_ms)}</td>
    <td class="client" data-label="Cliente">${esc(e.client)}</td>
  </tr>
  <tr class="detail" id="detail-${e.id}" data-for="${e.id}" ${open ? "" : "hidden"}><td colspan="8"><dl class="detail-grid">
    <dt>Comando</dt><dd><pre id="stmt-${e.id}">${esc(e.statement) || "—"}</pre>
      <div class="detail-actions"><button type="button" class="btn btn-sm" data-copy="#stmt-${e.id}">${icon("copy")}Copiar comando</button></div></dd>
    ${e.params ? `<dt>Parâmetros</dt><dd><pre>${esc(e.params)}</pre></dd>` : ""}
    ${e.message ? `<dt>${messageLabel}</dt><dd><pre>${esc(e.message)}</pre></dd>` : ""}
    <dt>Cliente</dt><dd>${esc(e.client) || "—"}</dd>
    <dt>Registro</dt><dd>#${e.id} · ${esc(e.db_type) || "sem banco"} · ${esc(fmtAbsolute(e.ts))}</dd>
  </dl></td></tr>`;
}

state.openDetails = new Set();

async function loadAudit(reset) {
  const p = apiParams();
  p.set("limit", "100");
  if (!reset && state.auditCursor) p.set("before_id", String(state.auditCursor));
  $("#export").href = "api/audit/export?" + apiParams();
  try {
    const rows = await api("GET", "api/audit?" + p);
    const tb = $("#audit-rows");
    const html = rows.map(auditRow).join("");
    if (reset) {
      tb.innerHTML = html;
      state.auditPages = 1;
    } else {
      tb.insertAdjacentHTML("beforeend", html);
      state.auditPages++;
    }
    if (rows.length) state.auditCursor = rows.at(-1).id;
    else if (reset) state.auditCursor = 0;
    const count = $$("tr.entry", tb).length;
    $("#audit-empty").hidden = count > 0;
    $(".table-wrap").hidden = count === 0;
    $("#audit-more").hidden = rows.length < 100;
    const live = $("#live").checked && state.auditPages === 1;
    let note = "";
    if (state.auditPages > 1) note = " Atualização automática pausada enquanto houver páginas antigas abertas.";
    else if (live) note = " Atualiza a cada 5 s.";
    $("#audit-summary").textContent = count ? `${plural(count, "registro", "registros")}, do mais recente para o mais antigo.${note}` : "";
  } catch (err) {
    $("#audit-summary").textContent = `Não foi possível carregar a auditoria: ${err.message}`;
  }
}

$("#audit-rows").addEventListener("click", (e) => {
  if (e.target.closest("[data-copy]")) return;
  const tr = e.target.closest("tr.entry");
  if (!tr || (e.target.closest("tr.detail"))) return;
  if (window.getSelection()?.toString() && !e.target.closest(".disclose")) return; // selecting text, not opening
  const id = tr.dataset.id;
  const d = $(`#detail-${id}`);
  const open = d.hidden;
  d.hidden = !open;
  tr.classList.toggle("is-open", open);
  $(".disclose", tr).setAttribute("aria-expanded", String(open));
  if (open) state.openDetails.add(id);
  else state.openDetails.delete(id);
});

let searchTimer;
$("#filters").addEventListener("input", (e) => {
  clearTimeout(searchTimer);
  const wait = e.target.id === "f-q" ? 300 : 0;
  searchTimer = setTimeout(() => { syncHash(); void loadAudit(true); }, wait);
});
$("#filters").addEventListener("submit", (e) => e.preventDefault());
$("#filters").addEventListener("reset", () => {
  setTimeout(() => { syncHash(); void loadAudit(true); $("#f-q").focus(); });
});
$("#load-more").addEventListener("click", () => loadAudit(false));
$("#live").addEventListener("change", () => { scheduleAudit(); void loadAudit(true); });

function scheduleAudit() {
  clearInterval(state.auditTimer);
  if (state.route !== "auditoria" || !$("#live").checked) return;
  state.auditTimer = setInterval(() => {
    if (document.hidden || state.auditPages > 1 || $("#dlg-conn").open) return;
    void loadAudit(true);
  }, 5000);
}

// ----------------------------------------------------------------- clients
function renderClients() {
  const url = new URL("mcp", location.href).href;
  $("#endpoint").textContent = url;
  const labels = state.meta.mcp_clients || [];
  const badges = labels.map((l) => {
    const b = document.createElement("span");
    b.className = "badge badge-type";
    b.textContent = l;
    return b;
  });
  const hint = document.createElement("span");
  hint.className = "hint";
  hint.textContent = "Tokens configurados:";
  $("#client-labels").replaceChildren(...(labels.length ? [hint, ...badges] : []));
  const auth = { Authorization: "Bearer <SEU_TOKEN>" };
  $("#snip-claude").textContent = `claude mcp add --transport http --scope user comporta ${url} \\\n  --header "Authorization: Bearer <SEU_TOKEN>"`;
  $("#snip-vscode").textContent = JSON.stringify({ servers: { comporta: { type: "http", url, headers: auth } } }, null, 2);
  $("#snip-generic").textContent = JSON.stringify({ mcpServers: { comporta: { type: "http", url, headers: auth } } }, null, 2);
}

document.addEventListener("click", async (e) => {
  const b = e.target.closest("[data-copy]");
  if (!b) return;
  try {
    await navigator.clipboard.writeText($(b.dataset.copy).textContent);
    toast("Copiado para a área de transferência.");
  } catch {
    toast("O navegador não deixou copiar. Selecione o texto e use Ctrl+C.", { error: true });
  }
});

// --------------------------------------------------------------- shortcuts
document.addEventListener("keydown", (e) => {
  if (e.ctrlKey || e.metaKey || e.altKey || document.querySelector("dialog[open]")) return;
  if (e.target.closest("input, textarea, select, [contenteditable]")) return;
  if (e.key === "n" && state.route === "conexoes") {
    e.preventDefault();
    openForm();
  } else if (e.key === "/") {
    e.preventDefault();
    if (state.route !== "auditoria") location.hash = "#/auditoria";
    setTimeout(() => $("#f-q").focus());
  }
});

// -------------------------------------------------------------------- boot
window.addEventListener("hashchange", () => navigate(true));
document.addEventListener("visibilitychange", () => { if (!document.hidden) void loadStats(); });

// app.js is a module: top-level await runs once, after the document parsed.
async function boot() {
  state.meta = await api("GET", "api/meta");
  $("#version").textContent = state.meta.version;
  $("#retention").textContent = `Os registros ficam guardados por ${state.meta.retention_days} dias.`;
  fillTypes();
  renderClients();
  await loadStats();
  await loadConnections();
  navigate(false);
  state.statsTimer = setInterval(() => { if (!document.hidden) void loadStats(); }, 15000);
}

try {
  await boot();
} catch (err) {
  showMessage($("#conn-loading"), "error", err.message);
}
