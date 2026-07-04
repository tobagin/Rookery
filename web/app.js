/* Rookery SPA — no framework, no build step. Hash routing:
   #/                     dashboard
   #/unit/<scope>/<name>  unit detail + editor + logs
   #/new                  create a unit                                     */
"use strict";

const $app = document.getElementById("app");
const $hoststrip = document.getElementById("hoststrip");

let refreshTimer = null;
let logSource = null;

/* ---------- helpers ---------- */

function esc(s) {
  return String(s ?? "").replace(/[&<>"']/g, c => ({
    "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;", "'": "&#39;",
  }[c]));
}

async function api(path, opts = {}) {
  const res = await fetch(path, {
    headers: { "Content-Type": "application/json" },
    ...opts,
  });
  const body = await res.json().catch(() => ({}));
  if (!res.ok && res.status !== 422) {
    throw new Error(body.error || `${res.status} ${res.statusText}`);
  }
  return { status: res.status, body };
}

function stateClass(u) {
  if (u.active === "failed") return "failed";
  if (u.active === "active") return "running";
  if (u.active === "activating" || u.active === "deactivating") return "pending";
  if (u.load === "unknown") return "unknown";
  return "stopped";
}

function stateLabel(u) {
  if (u.load === "unknown") return "unknown";
  return u.sub && u.sub !== u.active ? `${u.active} (${u.sub})` : u.active || "unknown";
}

function stopStreams() {
  if (refreshTimer) { clearInterval(refreshTimer); refreshTimer = null; }
  if (logSource) { logSource.close(); logSource = null; }
}

function toast(msg, isError) {
  const t = document.createElement("div");
  t.className = "toast" + (isError ? " toast-error" : "");
  t.textContent = msg;
  document.body.appendChild(t);
  setTimeout(() => t.remove(), 4000);
}

/* ---------- host strip ---------- */

async function renderHostStrip() {
  try {
    const { body } = await api("/api/host");
    const m = body.metrics || {};
    const memPct = m.memTotalKb ? Math.round(100 * (1 - m.memAvailKb / m.memTotalKb)) : null;
    const bits = [];
    if (m.hostname) bits.push(`<span title="kernel ${esc(m.kernel)}">${esc(m.hostname)}</span>`);
    if (m.load1 != null) bits.push(`<span>load ${m.load1.toFixed(2)}</span>`);
    if (memPct != null) bits.push(`<span>mem ${memPct}%</span>`);
    if (body.podman) bits.push(`<span>podman ${esc(body.podman.version)} · ${body.podman.containersRunning}/${body.podman.containersTotal} running</span>`);
    if (!body.generatorAvailable) bits.push(`<span class="warn" title="podman quadlet generator not found; validation disabled">no validator</span>`);
    $hoststrip.innerHTML = bits.join('<span class="sep">·</span>');
  } catch { /* strip is decorative; never block the app on it */ }
}

/* ---------- dashboard ---------- */

async function renderDashboard() {
  const { body } = await api("/api/units");
  const units = body.units || [];
  const groups = { failed: [], running: [], pending: [], stopped: [], unknown: [] };
  units.forEach(u => groups[stateClass(u)].push(u));

  const scopeErrors = Object.entries(body.scopeErrors || {})
    .map(([s, e]) => `<p class="banner banner-warn">scope <b>${esc(s)}</b>: ${esc(e)}</p>`).join("");

  const section = (title, list, cls) => !list.length ? "" : `
    <h2 class="group-title ${cls}">${title} <span class="count">${list.length}</span></h2>
    <div class="grid">${list.map(card).join("")}</div>`;

  $app.innerHTML = `
    ${scopeErrors}
    ${units.length ? "" : `<div class="empty">
       <p>No Quadlet units found.</p>
       <p class="muted">Create your first one with <a href="#/new">＋ New unit</a> — the file lands in the Quadlet
       directory on disk, exactly where <code>systemctl</code> expects it.</p></div>`}
    ${section("Failed", groups.failed, "failed")}
    ${section("Running", groups.running, "running")}
    ${section("Transitioning", groups.pending, "pending")}
    ${section("Stopped", groups.stopped, "stopped")}
    ${section("State unknown", groups.unknown, "unknown")}`;

  $app.querySelectorAll("[data-action]").forEach(btn => {
    btn.addEventListener("click", async ev => {
      ev.preventDefault(); ev.stopPropagation();
      const { scope, name, action } = btn.dataset;
      btn.disabled = true;
      try {
        await api(`/api/units/${encodeURIComponent(scope)}/${encodeURIComponent(name)}/action`,
          { method: "POST", body: JSON.stringify({ action }) });
        toast(`${action} ${name}: ok`);
      } catch (e) { toast(`${action} ${name}: ${e.message}`, true); }
      render();
    });
  });
}

function card(u) {
  const cls = stateClass(u);
  const canStart = cls === "stopped" || cls === "failed";
  return `
  <a class="card ${cls}" href="#/unit/${encodeURIComponent(u.scope)}/${encodeURIComponent(u.name)}">
    <div class="card-head">
      <span class="dot"></span>
      <span class="card-name">${esc(u.name)}</span>
      <span class="badge">${esc(u.kind)}</span>
      ${u.scope !== "system" ? `<span class="badge badge-user" title="rootless unit of ${esc(u.scope)}">${esc(u.scope)}</span>` : ""}
    </div>
    <div class="card-sub">${esc(u.description || u.image || "")}</div>
    <div class="card-foot">
      <span class="state">${esc(stateLabel(u))}</span>
      <span class="actions">
        ${canStart ? btnAction(u, "start", "▶") : ""}
        ${cls === "running" || cls === "pending" ? btnAction(u, "stop", "■") : ""}
        ${btnAction(u, "restart", "↻")}
      </span>
    </div>
  </a>`;
}

function btnAction(u, action, icon) {
  return `<button class="btn btn-sm" title="${action}" data-scope="${esc(u.scope)}" data-name="${esc(u.name)}" data-action="${action}">${icon}</button>`;
}

/* ---------- unit detail ---------- */

async function renderUnit(scope, name) {
  let unit, content;
  try {
    const { body } = await api(`/api/units/${encodeURIComponent(scope)}/${encodeURIComponent(name)}`);
    unit = body.unit; content = body.content;
  } catch (e) {
    $app.innerHTML = `<p class="banner banner-error">${esc(e.message)}</p>`;
    return;
  }
  const cls = stateClass(unit);
  $app.innerHTML = `
    <div class="detail-head">
      <a class="btn btn-sm" href="#/">←</a>
      <h1><span class="dot ${cls}"></span> ${esc(unit.name)}</h1>
      <span class="badge">${esc(unit.kind)}</span>
      ${scope !== "system" ? `<span class="badge badge-user">${esc(scope)}</span>` : ""}
      <span class="state">${esc(stateLabel(unit))}${unit.unitFile ? " · " + esc(unit.unitFile) : ""}</span>
    </div>
    <p class="muted mono">${esc(unit.path)}${unit.readOnly ? " (read-only)" : ""}</p>
    <div class="toolbar">
      ${["start", "stop", "restart", "enable", "disable"].map(a =>
        `<button class="btn" data-act="${a}">${a}</button>`).join("")}
      <button class="btn btn-danger" data-act="delete">delete</button>
    </div>
    <section>
      <h2>Unit file</h2>
      <textarea id="editor" spellcheck="false" ${unit.readOnly ? "readonly" : ""}>${esc(content)}</textarea>
      <div class="toolbar">
        <button class="btn" id="btn-validate">Validate</button>
        <button class="btn btn-accent" id="btn-save" ${unit.readOnly ? "disabled" : ""}>Save + reload</button>
        <label class="chk"><input type="checkbox" id="chk-restart"> restart after save</label>
      </div>
      <pre id="validation" class="output" hidden></pre>
    </section>
    <section>
      <h2>Logs <label class="chk"><input type="checkbox" id="chk-follow" checked> follow</label></h2>
      <pre id="logs" class="output logs">connecting…</pre>
    </section>`;

  const service = unit.service;
  $app.querySelectorAll("[data-act]").forEach(btn => {
    btn.addEventListener("click", async () => {
      const act = btn.dataset.act;
      if (act === "delete") {
        if (!confirm(`Delete ${name}? The unit file is removed from disk and ${service} is stopped.`)) return;
        try {
          await api(`/api/units/${encodeURIComponent(scope)}/${encodeURIComponent(name)}`, { method: "DELETE" });
          toast(`deleted ${name}`);
          location.hash = "#/";
        } catch (e) { toast(e.message, true); }
        return;
      }
      btn.disabled = true;
      try {
        await api(`/api/units/${encodeURIComponent(scope)}/${encodeURIComponent(name)}/action`,
          { method: "POST", body: JSON.stringify({ action: act }) });
        toast(`${act}: ok`);
      } catch (e) { toast(`${act}: ${e.message}`, true); }
      render();
    });
  });

  const $editor = document.getElementById("editor");
  const $validation = document.getElementById("validation");

  function showValidation(v) {
    $validation.hidden = false;
    $validation.textContent = (v.available
      ? (v.valid ? "✓ valid" : "✗ invalid") : "validator unavailable") +
      (v.output ? "\n\n" + v.output : "");
    $validation.className = "output " + (v.valid ? "ok" : "err");
  }

  document.getElementById("btn-validate").addEventListener("click", async () => {
    try {
      const { body } = await api("/api/validate", {
        method: "POST",
        body: JSON.stringify({ scope, name, content: $editor.value }),
      });
      showValidation(body.validation);
    } catch (e) { toast(e.message, true); }
  });

  document.getElementById("btn-save").addEventListener("click", async () => {
    try {
      const { status, body } = await api(`/api/units/${encodeURIComponent(scope)}/${encodeURIComponent(name)}`, {
        method: "PUT",
        body: JSON.stringify({ content: $editor.value, restart: document.getElementById("chk-restart").checked }),
      });
      if (body.validation) showValidation(body.validation);
      if (status === 422) { toast("rejected by validator", true); return; }
      (body.warnings || []).forEach(warning => toast(warning, true));
      toast(`saved ${name} + daemon-reload`);
    } catch (e) { toast(e.message, true); }
  });

  startLogs(scope, name);
  document.getElementById("chk-follow").addEventListener("change", () => startLogs(scope, name));
}

function startLogs(scope, name) {
  if (logSource) { logSource.close(); logSource = null; }
  const follow = document.getElementById("chk-follow").checked ? "1" : "0";
  const $logs = document.getElementById("logs");
  $logs.textContent = "";
  logSource = new EventSource(
    `/api/units/${encodeURIComponent(scope)}/${encodeURIComponent(name)}/logs?follow=${follow}&lines=200`);
  logSource.onmessage = ev => {
    let line = ev.data;
    try {
      const j = JSON.parse(ev.data);
      const ts = j.__REALTIME_TIMESTAMP ? new Date(j.__REALTIME_TIMESTAMP / 1000).toLocaleTimeString() : "";
      const msg = typeof j.MESSAGE === "string" ? j.MESSAGE : JSON.stringify(j.MESSAGE);
      line = `${ts}  ${msg}`;
    } catch { /* show raw line */ }
    const atBottom = $logs.scrollHeight - $logs.scrollTop - $logs.clientHeight < 40;
    $logs.textContent += line + "\n";
    if (atBottom) $logs.scrollTop = $logs.scrollHeight;
  };
  logSource.onerror = () => { if (follow === "0" && logSource) { logSource.close(); logSource = null; } };
}

/* ---------- new unit ---------- */

const TEMPLATES = {
  container: `[Unit]
Description=My container

[Container]
Image=docker.io/library/nginx:latest
PublishPort=8080:80
# Volume=/srv/data:/data:Z

[Service]
Restart=always

[Install]
WantedBy=default.target
`,
  pod: `[Pod]
PublishPort=8080:80
`,
  network: `[Network]
Subnet=10.89.0.0/24
`,
  volume: `[Volume]
`,
  kube: `[Kube]
Yaml=deployment.yml
`,
  image: `[Image]
Image=docker.io/library/nginx:latest
`,
  build: `[Build]
ImageTag=localhost/myimage:latest
File=Containerfile
`,
};

async function renderNew() {
  let scopes = ["system"];
  try { scopes = (await api("/api/host")).body.scopes || scopes; } catch { /* keep default */ }
  $app.innerHTML = `
    <div class="detail-head"><a class="btn btn-sm" href="#/">←</a><h1>New unit</h1></div>
    <div class="toolbar">
      <input id="new-name" class="input" placeholder="name (e.g. jellyfin)" autocomplete="off">
      <select id="new-kind" class="input">${Object.keys(TEMPLATES).map(k => `<option>${k}</option>`).join("")}</select>
      <select id="new-scope" class="input">${scopes.map(s => `<option>${esc(s)}</option>`).join("")}</select>
    </div>
    <textarea id="editor" spellcheck="false">${esc(TEMPLATES.container)}</textarea>
    <div class="toolbar">
      <button class="btn btn-accent" id="btn-create">Validate + create</button>
    </div>
    <pre id="validation" class="output" hidden></pre>`;

  const $kind = document.getElementById("new-kind");
  const $editor = document.getElementById("editor");
  $kind.addEventListener("change", () => { $editor.value = TEMPLATES[$kind.value]; });

  document.getElementById("btn-create").addEventListener("click", async () => {
    const base = document.getElementById("new-name").value.trim();
    if (!base) { toast("name required", true); return; }
    const name = `${base}.${$kind.value}`;
    const scope = document.getElementById("new-scope").value;
    const $validation = document.getElementById("validation");
    try {
      const { status, body } = await api(`/api/units/${encodeURIComponent(scope)}/${encodeURIComponent(name)}`, {
        method: "PUT",
        body: JSON.stringify({ content: $editor.value }),
      });
      if (body.validation && (!body.validation.valid || body.validation.output)) {
        $validation.hidden = false;
        $validation.textContent = body.validation.output || "";
        $validation.className = "output " + (body.validation.valid ? "ok" : "err");
      }
      if (status === 422) { toast("rejected by validator", true); return; }
      toast(`created ${name}`);
      location.hash = `#/unit/${encodeURIComponent(scope)}/${encodeURIComponent(name)}`;
    } catch (e) { toast(e.message, true); }
  });
}

/* ---------- router ---------- */

async function render() {
  stopStreams();
  renderHostStrip();
  const hash = location.hash || "#/";
  const parts = hash.slice(2).split("/").filter(Boolean).map(decodeURIComponent);
  try {
    if (parts[0] === "unit" && parts.length === 3) {
      await renderUnit(parts[1], parts[2]);
    } else if (parts[0] === "new") {
      await renderNew();
    } else {
      await renderDashboard();
      refreshTimer = setInterval(() => {
        if (!document.hidden) { renderDashboard(); renderHostStrip(); }
      }, 5000);
    }
  } catch (e) {
    $app.innerHTML = `<p class="banner banner-error">${esc(e.message)}</p>`;
  }
}

window.addEventListener("hashchange", render);
render();
