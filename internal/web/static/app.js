// Comporta admin UI. No framework and no build step: the file is embedded in
// the Go binary and served under a CSP without 'unsafe-inline', so all markup
// is built here and every value from the API goes through esc(). Every text
// comes from i18n.js.

import { t, lang, setLang, locale, applyStatic, LANGUAGES } from "./i18n.js";

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
  route: "connections",
  auditCursor: 0,
  auditPages: 0,
  auditTimer: null,
  statsTimer: null,
  formDirty: false,
  editing: null,
};

const OPS = [
  { field: "allow_insert", key: "insert", sql: "INSERT" },
  { field: "allow_update", key: "update", sql: "UPDATE" },
  { field: "allow_delete", key: "delete", sql: "DELETE", risky: true },
  { field: "allow_ddl", key: "ddl", sql: "DDL", risky: true },
];
const opLabel = (op) => t("op." + op.key);
const opRisk = (op) => t("risk." + op.key);

// --------------------------------------------------------------------- api
class ApiError extends Error {
  constructor(message, field, status, code, params) {
    super(message);
    this.field = field;
    this.status = status;
    this.code = code;
    this.params = params;
  }

  // localized is the server's message in the current language, when the
  // server sent a code for it; otherwise the server's (English) text.
  get localized() {
    if (!this.code) return this.message;
    const text = t(`srv.${this.code}`, this.params || {});
    return text === `srv.${this.code}` ? this.message : text;
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
    throw new ApiError(t("error.offline"));
  }
  if (res.status === 204) return null;
  const data = await res.json().catch(() => ({}));
  if (!res.ok) throw new ApiError(data.error || t("error.status", { status: res.status }), data.field, res.status, data.code, data.params);
  return data;
}

// ------------------------------------------------------------------ format
const fmtNumber = (n) => Number(n || 0).toLocaleString(locale());

function fmtAbsolute(ts) {
  const d = new Date(ts);
  return d.toLocaleString(locale(), { day: "2-digit", month: "2-digit", year: "numeric", hour: "2-digit", minute: "2-digit", second: "2-digit" });
}

function fmtRelative(ts) {
  const sec = Math.round((Date.now() - new Date(ts).getTime()) / 1000);
  if (sec < 45) return t("time.now");
  const min = Math.round(sec / 60);
  if (min < 60) return t("time.min", { n: min });
  const h = Math.round(min / 60);
  if (h < 24) return t("time.h", { n: h });
  const d = Math.round(h / 24);
  return d === 1 ? t("time.yesterday") : t("time.days", { n: d });
}

function fmtDuration(ms) {
  if (ms < 1000) return `${ms} ms`;
  return `${(ms / 1000).toLocaleString(locale(), { maximumFractionDigits: 1 })} s`;
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
const ROUTES = ["connections", "audit", "clients"];
// Links saved before the routes were in English still open the right page.
const ROUTE_ALIASES = new Map([["conexoes", "connections"], ["auditoria", "audit"], ["clientes", "clients"]]);
const ROUTE_SET = new Set(ROUTES);
const ROUTE_TITLES = { connections: "nav.connections", audit: "nav.audit", clients: "nav.clients" };

function parseHash() {
  const [raw, query = ""] = location.hash.replace(/^#\/?/, "").split("?");
  const path = ROUTE_ALIASES.get(raw) || raw;
  return { route: ROUTE_SET.has(path) ? path : "connections", params: new URLSearchParams(query) };
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
  setTitle();
  if (route === "audit") {
    applyFiltersFromParams(params);
    void loadAudit(true);
  }
  scheduleAudit();
  // Moving focus to the new heading tells screen reader users the page changed.
  if (fromUser && changed) $(`#h-${route}`).focus({ preventScroll: true });
}

function setTitle() {
  document.title = `${t(ROUTE_TITLES[state.route])} · Comporta`;
}

// ------------------------------------------------------------------- stats
async function loadStats() {
  try {
    const s = await api("GET", "api/audit/stats");
    state.activity = s.connections || {};
    state.stats = s;
    renderPulse();
    $("#pulse").hidden = false;
    if (state.route === "connections") renderActivity();
  } catch {
    // The header summary is a convenience: a failed refresh keeps the last values.
  }
}

function renderPulse() {
  const s = state.stats;
  if (!s) return;
  const set = (id, n, key) => {
    const el = $(id);
    const b = document.createElement("b");
    b.textContent = fmtNumber(n);
    el.replaceChildren(b, document.createTextNode(` ${t(key, { n })}`));
    el.classList.toggle("has-value", n > 0);
  };
  set("#pulse-total", s.total, "pulse.calls");
  set("#pulse-denied", s.denied, "pulse.denied");
  set("#pulse-error", s.error, "pulse.errors");
}

// ------------------------------------------------------------- connections
function riskOf(c) {
  if (c.allow_ddl) return { cls: "badge-risk-full", icon: "alert", text: t("risk.full") };
  if (c.allow_insert || c.allow_update || c.allow_delete) return { cls: "badge-risk-write", icon: "edit", text: t("risk.write") };
  return { cls: "badge-risk-read", icon: "lock", text: t("risk.read") };
}

function activityHTML(c) {
  const a = state.activity[c.name];
  if (!a?.last_call) return `<span>${esc(t("activity.none"))}</span>`;
  const parts = [`<span>${esc(t("activity.calls", { n: a.calls_24h }))}</span>`];
  if (a.denied_24h) parts.push(`<span class="is-denied">${icon("deny")} ${esc(t("activity.denied", { n: a.denied_24h }))}</span>`);
  if (a.errors_24h) parts.push(`<span class="is-error">${icon("error")} ${esc(t("activity.errors", { n: a.errors_24h }))}</span>`);
  parts.push(`<span title="${esc(fmtAbsolute(a.last_call))}">${esc(t("activity.last", { when: fmtRelative(a.last_call) }))}</span>`);
  return parts.join("");
}

function connCard(c) {
  const info = state.meta.db_types[c.type] || {};
  const risk = riskOf(c);
  const chips = OPS.map((op) => {
    const on = !!c[op.field];
    return `<button type="button" class="chip${op.risky ? " is-risky" : ""}" data-act="perm" data-field="${op.field}" aria-pressed="${on}"
      aria-label="${esc(t("conn.permAria", { sql: op.sql, label: opLabel(op).toLowerCase(), name: c.name }))}" title="${esc(t(on ? "conn.permOn" : "conn.permOff"))}">${icon("check")}${op.sql}</button>`;
  }).join("");
  return `<li class="conn${c.enabled ? "" : " is-off"}" data-id="${c.id}">
    <div class="conn-main">
      <div class="conn-title">
        <h2 class="conn-name">${esc(c.name)}</h2>
        <span class="badge badge-type">${icon("db")}${esc(info.label || c.type)}</span>
        <span class="badge ${risk.cls}">${icon(risk.icon)}${esc(risk.text)}</span>
        ${c.enabled ? "" : `<span class="badge">${esc(t("conn.disabled"))}</span>`}
      </div>
      <p class="conn-desc${c.description ? "" : " is-missing"}">${esc(c.description || t("conn.noDescription"))}</p>
      <div class="conn-meta">
        <code class="conn-dsn" title="${esc(c.dsn_masked)}">${esc(c.dsn_masked)}</code>
        ${c.database ? `<span>database: <code>${esc(c.database)}</code></span>` : ""}
        <span>${esc(t("conn.limits", { rows: fmtNumber(c.max_rows), sec: c.timeout_sec }))}</span>
      </div>
    </div>
    <div class="conn-state">
      <label class="switch-label">
        <input type="checkbox" class="switch" data-act="enabled" ${c.enabled ? "checked" : ""} aria-describedby="state-${c.id}">
        <span id="state-${c.id}">${esc(t(c.enabled ? "conn.visible" : "conn.hidden"))}</span>
      </label>
    </div>
    <div class="conn-perms" role="group" aria-label="${esc(t("conn.permsLabel", { name: c.name }))}">
      <span class="conn-perms-label">${esc(t("conn.aiCan"))}</span>
      <span class="chip is-locked" title="${esc(t("conn.selectAlways"))}">${icon("check")}SELECT</span>
      ${chips}
    </div>
    <div class="conn-foot">
      <div class="conn-activity" data-activity>${activityHTML(c)}</div>
      <span class="spacer"></span>
      <button type="button" class="btn btn-sm" data-act="test">${icon("plug")}${esc(t("conn.test"))}</button>
      <button type="button" class="btn btn-sm" data-act="edit">${icon("edit")}${esc(t("conn.edit"))}</button>
      <button type="button" class="btn btn-sm btn-quiet-danger" data-act="delete" aria-label="${esc(t("conn.deleteAria", { name: c.name }))}">${icon("trash")}${esc(t("conn.delete"))}</button>
    </div>
  </li>`;
}

function renderConnections() {
  $("#conn-loading").hidden = true;
  $("#conn-empty").hidden = state.conns.length > 0;
  $("#conn-list").innerHTML = state.conns.map(connCard).join("");
  const sel = $("#f-connection");
  const cur = sel.value;
  sel.innerHTML = `<option value="">${esc(t("audit.all"))}</option>` + state.conns.map((c) => `<option>${esc(c.name)}</option>`).join("");
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
    showMessage($("#conn-loading"), "error", t("conn.loadFailed", { msg: err.localized }));
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
    let message = t(value ? "toast.visible" : "toast.hidden", { name: c.name });
    if (op) message = t("toast.permChanged", { sql: op.sql, state: t(value ? "toast.allowed" : "toast.blocked"), name: c.name });
    toast(message, undo ? { action: { label: t("common.undo"), run: () => undoField(c.id, field, !value) } } : {});
    void loadStats();
  } catch (err) {
    control.removeAttribute("aria-busy");
    control.disabled = false;
    if (control.type === "checkbox") control.checked = !value;
    toast(err.localized, { error: true });
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
      title: t("confirm.allowTitleOn", { sql: op.sql, name: c.name }),
      body: `<p>${esc(opRisk(op))}</p><p>${esc(t("confirm.allowNote"))}</p>`,
      ok: t("confirm.allowOk", { sql: op.sql }),
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
  button.innerHTML = `<span class="spinner" aria-hidden="true"></span>${esc(t("toast.testing"))}`;
  try {
    const r = await api("POST", "api/test", { id: c.id, type: c.type, database: c.database });
    if (r.ok) toast(t("toast.testOk", { name: c.name, ms: fmtDuration(r.ms) }));
    else toast(t("toast.testFail", { name: c.name, msg: r.error }), { error: true });
  } catch (err) {
    toast(err.localized, { error: true });
  } finally {
    button.innerHTML = label;
    button.removeAttribute("aria-busy");
    button.disabled = false;
  }
}

async function deleteConn(c) {
  const ok = await confirmDialog({
    title: t("confirm.deleteTitle", { name: c.name }),
    body: `<p>${esc(t("confirm.deleteBody1"))}</p><p>${esc(t("confirm.deleteBody2"))}</p>`,
    ok: t("confirm.deleteOk"),
  });
  if (!ok) return;
  try {
    await api("DELETE", `api/connections/${c.id}`);
    toast(t("toast.deleted", { name: c.name }));
    await loadConnections();
    $("#new-conn").focus();
  } catch (err) {
    toast(err.localized, { error: true });
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

$("#view-connections").addEventListener("click", (e) => {
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
  const type = $("#c-type").value;
  const info = state.meta.db_types[type] || {};
  const keep = state.editing ? t("form.dsnKeep") : "";
  $("#c-dsn-hint").innerHTML = `${esc(keep + t("form.dsnFormat"))}<code>${esc(info.dsn_example || "")}</code>`;
  $("#db-field").hidden = type !== "mongodb";
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
  $("#dlg-conn-title").textContent = c ? t("form.editTitle", { name: c.name }) : t("form.newTitle");
  $("#save-btn").textContent = t(c ? "form.save" : "form.create");
  $("#c-type").value = c?.type || "postgres";
  $("#c-name").value = c?.name || "";
  $("#c-desc").value = c?.description || "";
  $("#c-dsn").value = "";
  $("#c-dsn").required = !c;
  setDsnVisible(false);
  $("#c-db").value = c?.database || "";
  $("#c-rows").value = c?.max_rows || Math.min(100, state.meta.max_rows_cap || 100);
  $("#c-rows").max = state.meta.max_rows_cap;
  $("#c-rows-hint").textContent = t("form.rowsHint", { cap: fmtNumber(state.meta.max_rows_cap) });
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
  if (!value) problem = t("valid.nameEmpty");
  else if (!NAME_RE.test(value)) problem = t("valid.nameInvalid");
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
  $("span", b).textContent = t(show ? "form.hide" : "form.show");
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
    const ok = await confirmDialog({ title: t("confirm.allowTitle", { sql: op.sql }), body: `<p>${esc(opRisk(op))}</p>`, ok: t("confirm.allowOk", { sql: op.sql }) });
    e.target.checked = ok;
    e.target.focus();
  });
}

$("#test-btn").addEventListener("click", async () => {
  const r = $("#test-result");
  const b = formBody();
  if (!b.dsn && !state.editing) {
    setFieldError("dsn", t("valid.dsnTest"));
    return $("#c-dsn").focus();
  }
  const btn = $("#test-btn");
  btn.setAttribute("aria-busy", "true");
  btn.disabled = true;
  r.className = "test-result";
  r.innerHTML = `<span class="spinner" aria-hidden="true"></span>${esc(t("form.testing"))}`;
  try {
    const res = await api("POST", "api/test", { id: state.editing?.id || 0, type: b.type, dsn: b.dsn, database: b.database });
    r.className = "test-result " + (res.ok ? "is-ok" : "is-error");
    if (res.ok) showMessage(r, "ok", t("form.testOk", { ms: fmtDuration(res.ms) }));
    else showMessage(r, "error", res.error);
  } catch (err) {
    r.className = "test-result is-error";
    showMessage(r, "error", err.localized);
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
    setFieldError("dsn", t("valid.dsnEmpty"));
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
    toast(t(state.editing ? "toast.updated" : "toast.created", { name: body.name }));
    await loadConnections();
    void loadStats();
  } catch (err) {
    if (err.field && setFieldError(err.field, err.localized)) {
      $(`[aria-invalid="true"]`, form)?.focus();
    } else {
      const box = $("#form-error");
      box.textContent = err.localized;
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
    const ok = await confirmDialog({ title: t("confirm.discardTitle"), body: `<p>${esc(t("confirm.discardBody"))}</p>`, ok: t("confirm.discardOk") });
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
  ok: { icon: "ok", cls: "badge-ok" },
  denied: { icon: "deny", cls: "badge-denied" },
  error: { icon: "error", cls: "badge-error" },
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
  history.replaceState(null, "", "#/audit" + (q ? "?" + q : ""));
  $("#f-reset").hidden = !q;
}

function auditRow(e) {
  const st = STATUS[e.status] || { icon: "alert", cls: "" };
  const statusLabel = STATUS[e.status] ? t(`status.${e.status}`) : e.status;
  const admin = e.tool === "admin";
  const open = state.openDetails.has(String(e.id));
  const messageLabel = t(e.status === "ok" ? "audit.message" : "audit.reason");
  return `<tr class="entry${open ? " is-open" : ""}" data-id="${e.id}">
    <td class="when c-when"><span title="${esc(fmtAbsolute(e.ts))}">${esc(fmtRelative(e.ts))}</span><small>${esc(new Date(e.ts).toLocaleTimeString(locale()))}</small></td>
    <td class="c-status"><span class="badge ${st.cls}">${icon(st.icon)}${esc(statusLabel)}</span></td>
    <td data-label="${esc(t("audit.connection"))}"><code>${esc(e.connection) || "—"}</code></td>
    <td data-label="${esc(t("audit.tool"))}">${admin ? `<span class="tool-admin">${esc(t("audit.adminChange"))}</span>` : `<code>${esc(e.tool)}</code>`}<br><small class="muted">${esc(e.operation)}</small></td>
    <td class="c-stmt"><div class="stmt">
      <button type="button" class="disclose" aria-expanded="${open}" aria-controls="detail-${e.id}" aria-label="${esc(t("audit.details", { id: e.id }))}">${icon("chevron")}</button>
      <code title="${esc(e.statement)}">${esc(e.statement) || "—"}</code></div></td>
    <td class="num" data-label="${esc(t("audit.rows"))}">${fmtNumber(e.rows)}</td>
    <td class="num" data-label="${esc(t("audit.duration"))}">${fmtDuration(e.duration_ms)}</td>
    <td class="client" data-label="${esc(t("audit.client"))}">${esc(e.client)}</td>
  </tr>
  <tr class="detail" id="detail-${e.id}" data-for="${e.id}" ${open ? "" : "hidden"}><td colspan="8"><dl class="detail-grid">
    <dt>${esc(t("audit.command"))}</dt><dd><pre id="stmt-${e.id}">${esc(e.statement) || "—"}</pre>
      <div class="detail-actions"><button type="button" class="btn btn-sm" data-copy="#stmt-${e.id}">${icon("copy")}${esc(t("audit.copyCommand"))}</button></div></dd>
    ${e.params ? `<dt>${esc(t("audit.params"))}</dt><dd><pre>${esc(e.params)}</pre></dd>` : ""}
    ${e.message ? `<dt>${esc(messageLabel)}</dt><dd><pre>${esc(e.message)}</pre></dd>` : ""}
    <dt>${esc(t("audit.client"))}</dt><dd>${esc(e.client) || "—"}</dd>
    <dt>${esc(t("audit.record"))}</dt><dd>#${e.id} · ${esc(e.db_type || t("audit.noDb"))} · ${esc(fmtAbsolute(e.ts))}</dd>
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
    if (state.auditPages > 1) note = t("audit.paused");
    else if (live) note = t("audit.every5");
    $("#audit-summary").textContent = count ? t("audit.count", { n: count }) + note : "";
  } catch (err) {
    $("#audit-summary").textContent = t("audit.loadFailed", { msg: err.localized });
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
  if (state.route !== "audit" || !$("#live").checked) return;
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
  hint.textContent = t("clients.tokens");
  $("#client-labels").replaceChildren(...(labels.length ? [hint, ...badges] : []));
  const token = t("clients.token");
  const auth = { Authorization: `Bearer ${token}` };
  $("#snip-claude").textContent = `claude mcp add --transport http --scope user comporta ${url} \\\n  --header "Authorization: Bearer ${token}"`;
  $("#snip-vscode").textContent = JSON.stringify({ servers: { comporta: { type: "http", url, headers: auth } } }, null, 2);
  $("#snip-generic").textContent = JSON.stringify({ mcpServers: { comporta: { type: "http", url, headers: auth } } }, null, 2);
}

document.addEventListener("click", async (e) => {
  const b = e.target.closest("[data-copy]");
  if (!b) return;
  try {
    await navigator.clipboard.writeText($(b.dataset.copy).textContent);
    toast(t("toast.copied"));
  } catch {
    toast(t("toast.copyFailed"), { error: true });
  }
});

// --------------------------------------------------------------- shortcuts
document.addEventListener("keydown", (e) => {
  if (e.ctrlKey || e.metaKey || e.altKey || document.querySelector("dialog[open]")) return;
  if (e.target.closest("input, textarea, select, [contenteditable]")) return;
  if (e.key === "n" && state.route === "connections") {
    e.preventDefault();
    openForm();
  } else if (e.key === "/") {
    e.preventDefault();
    if (state.route !== "audit") location.hash = "#/audit";
    setTimeout(() => $("#f-q").focus());
  }
});

// -------------------------------------------------------------------- boot
window.addEventListener("hashchange", () => navigate(true));
document.addEventListener("visibilitychange", () => { if (!document.hidden) void loadStats(); });

// ----------------------------------------------------------------- storage
const STORAGE_NOTE_KEY = "comporta.storageNoteDismissed";

// renderStorageNote warns when the data dies with the container. Dismissing
// it is remembered for that data path only.
function renderStorageNote() {
  const st = state.meta.storage;
  let dismissed = false;
  try {
    dismissed = localStorage.getItem(STORAGE_NOTE_KEY) === st?.path;
  } catch {
    // storage blocked: show the note every time
  }
  $("#storage-note").hidden = !st || st.persistent || dismissed;
}

$("#storage-dismiss").addEventListener("click", () => {
  try {
    localStorage.setItem(STORAGE_NOTE_KEY, state.meta.storage?.path || "");
  } catch {
    // nothing to remember it in
  }
  $("#storage-note").hidden = true;
  $("#new-conn").focus();
});

// ---------------------------------------------------------------- language
// renderTexts redraws everything that holds text, in the current language.
function renderTexts() {
  applyStatic();
  setTitle();
  setDsnVisible($("#c-dsn").type === "text");
  if (state.meta.retention_days) $("#retention").textContent = t("audit.retention", { days: state.meta.retention_days });
  renderPulse();
  renderClients();
  renderConnections();
  if (state.route === "audit") void loadAudit(true);
}

function setupLanguage() {
  const sel = $("#lang");
  sel.replaceChildren(...LANGUAGES.map((l) => new Option(l.label, l.code)));
  sel.value = lang();
  sel.addEventListener("change", () => {
    setLang(sel.value);
    renderTexts();
  });
}

// app.js is a module: top-level await runs once, after the document parsed.
async function boot() {
  setupLanguage();
  applyStatic();
  state.meta = await api("GET", "api/meta");
  $("#version").textContent = state.meta.version;
  $("#retention").textContent = t("audit.retention", { days: state.meta.retention_days });
  fillTypes();
  renderClients();
  renderStorageNote();
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
