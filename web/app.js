/* Rookery SPA — no framework, no build step. Hash routing:
   #/                     dashboard
   #/unit/<scope>/<name>  unit detail + editor + logs
   #/new                  create a unit
   #/import               convert podman run / compose / running containers  */
"use strict";

const $app = document.getElementById("app");
const $hoststrip = document.getElementById("hoststrip");

let refreshTimer = null;
let logSource = null;
let authState = { required: false, authenticated: true };
// Last "check image updates" result, keyed by scope/name; survives the
// dashboard's periodic re-render.
let updateInfo = {};
let updateSummary = "";

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
  if (res.status === 401 && path !== "/api/login") {
    authState.authenticated = false;
    renderLogin();
    throw new Error("authentication required");
  }
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
  let label = u.sub && u.sub !== u.active ? `${u.active} (${u.sub})` : u.active || "unknown";
  if (u.result === "exit-code") label += ` · exit ${u.exitCode}`;
  return label;
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
  setTimeout(() => t.remove(), 5000);
}

/* ---------- diff ---------- */

// lineDiff returns [op, line] pairs (op: " ", "-", "+") via LCS; unit files
// are small, so the quadratic table is fine.
function lineDiff(before, after) {
  const A = before.split("\n"), B = after.split("\n");
  const m = A.length, n = B.length;
  const dp = Array.from({ length: m + 1 }, () => new Array(n + 1).fill(0));
  for (let i = m - 1; i >= 0; i--) {
    for (let j = n - 1; j >= 0; j--) {
      dp[i][j] = A[i] === B[j] ? dp[i + 1][j + 1] + 1 : Math.max(dp[i + 1][j], dp[i][j + 1]);
    }
  }
  const out = [];
  let i = 0, j = 0;
  while (i < m && j < n) {
    if (A[i] === B[j]) { out.push([" ", A[i]]); i++; j++; }
    else if (dp[i + 1][j] >= dp[i][j + 1]) { out.push(["-", A[i]]); i++; }
    else { out.push(["+", B[j]]); j++; }
  }
  while (i < m) { out.push(["-", A[i]]); i++; }
  while (j < n) { out.push(["+", B[j]]); j++; }
  return out;
}

function diffHTML(before, after) {
  return `<pre class="output diff">${lineDiff(before, after).map(([op, line]) => {
    const cls = op === "+" ? "diff-add" : op === "-" ? "diff-del" : "diff-ctx";
    return `<span class="${cls}">${esc(op)} ${esc(line)}</span>`;
  }).join("\n")}</pre>`;
}

/* ---------- syntax highlighting ----------
   A <pre> with highlighted markup sits behind a transparent-text textarea
   with identical metrics; input and scroll keep them in sync. */

function highlightUnit(text) {
  return esc(text)
    .replace(/^([#;].*)$/gm, '<span class="hl-comment">$1</span>')
    .replace(/^(\[[^\]\n]*\])[ \t]*$/gm, '<span class="hl-section">$1</span>')
    .replace(/^([A-Za-z0-9_.-]+)(=)(.*)$/gm,
      '<span class="hl-key">$1</span><span class="hl-eq">$2</span><span class="hl-val">$3</span>');
}

function enhanceEditor($ta) {
  const wrap = document.createElement("div");
  wrap.className = "editor-wrap";
  $ta.parentNode.insertBefore(wrap, $ta);
  const hl = document.createElement("pre");
  hl.className = "editor-hl";
  hl.setAttribute("aria-hidden", "true");
  wrap.appendChild(hl);
  wrap.appendChild($ta);
  const sync = () => {
    hl.innerHTML = highlightUnit($ta.value) + "\n";
    hl.scrollTop = $ta.scrollTop;
    hl.scrollLeft = $ta.scrollLeft;
  };
  $ta.addEventListener("input", sync);
  $ta.addEventListener("scroll", () => {
    hl.scrollTop = $ta.scrollTop;
    hl.scrollLeft = $ta.scrollLeft;
  });
  sync();
  return sync;
}

/* ---------- auth ---------- */

async function checkAuth() {
  try {
    const { body } = await api("/api/auth");
    authState = { required: !!body.required, authenticated: !!body.authenticated };
  } catch { /* open mode if unreachable; the next call will re-ask */ }
}

function renderLogin() {
  stopStreams();
  $hoststrip.innerHTML = "";
  $app.innerHTML = `
    <div class="login">
      <h1>🦭 Rookery</h1>
      <p class="muted">Sign in to manage this host's Quadlets.</p>
      <form id="login-form" class="toolbar">
        <input type="password" id="login-pass" class="input" placeholder="Admin password" autocomplete="current-password">
        <button class="btn btn-accent">Sign in</button>
      </form>
      <p id="login-err" class="banner banner-error" hidden></p>
    </div>`;
  const $err = document.getElementById("login-err");
  document.getElementById("login-form").addEventListener("submit", async ev => {
    ev.preventDefault();
    try {
      await api("/api/login", {
        method: "POST",
        body: JSON.stringify({ password: document.getElementById("login-pass").value }),
      });
      authState.authenticated = true;
      render();
    } catch (e) {
      $err.hidden = false;
      $err.textContent = e.message;
    }
  });
  document.getElementById("login-pass").focus();
}

async function logout() {
  try { await api("/api/logout", { method: "POST" }); } catch { /* session may be gone already */ }
  authState.authenticated = false;
  renderLogin();
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
    if (body.selinuxEnforcing) bits.push(`<span title="SELinux is enforcing; Rookery will hint about unlabeled bind mounts">selinux</span>`);
    if (!body.generatorAvailable) bits.push(`<span class="warn" title="podman quadlet generator not found; validation disabled">no validator</span>`);
    if (authState.required && authState.authenticated) {
      bits.push(`<a href="#" id="btn-logout" title="sign out">logout</a>`);
    }
    $hoststrip.innerHTML = bits.join('<span class="sep">·</span>');
    const lo = document.getElementById("btn-logout");
    if (lo) lo.addEventListener("click", ev => { ev.preventDefault(); logout(); });
  } catch { /* strip is decorative; never block the app on it */ }
}

/* ---------- dashboard ---------- */

function meter(label, pct, text) {
  return `<span class="meter" title="${esc(label)}">
    <span class="meter-fill" style="width:${Math.min(100, Math.max(0, pct))}%"></span>
    <span class="meter-text">${esc(label)} ${esc(text)}</span>
  </span>`;
}

async function gpuPanelHTML() {
  try {
    const { body } = await api("/api/gpus");
    const devices = body.devices || [];
    if (!devices.length) return "";
    return `<h2 class="group-title">GPUs</h2><div class="gpu-panel">` + devices.map(d => {
      const memText = d.memoryTotalMb > 0
        ? `${d.memoryUsedMb >= 0 ? d.memoryUsedMb : "?"} / ${d.memoryTotalMb} MB` : "";
      const memPct = d.memoryTotalMb > 0 && d.memoryUsedMb >= 0
        ? Math.round(100 * d.memoryUsedMb / d.memoryTotalMb) : null;
      return `<div class="gpu-row">
        <span class="badge badge-gpu">${esc(d.vendor)}</span>
        <span class="gpu-name">${esc(d.name)}</span>
        ${d.utilizationPct >= 0 ? meter("util", d.utilizationPct, d.utilizationPct + "%") : ""}
        ${memPct != null ? meter("vram", memPct, memText) : ""}
      </div>`;
    }).join("") + `</div>`;
  } catch { return ""; }
}

async function renderDashboard() {
  const [{ body }, gpuHTML] = await Promise.all([api("/api/units"), gpuPanelHTML()]);
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
    ${gpuHTML}
    ${units.length ? "" : `<div class="empty">
       <p>No Quadlet units found.</p>
       <p class="muted">Create one with <a href="#/new">＋ New unit</a>, or convert an existing
       <code>podman run</code> command, compose file, or running container with <a href="#/import">⤵ Import</a>.</p></div>`}
    ${section("Failed", groups.failed, "failed")}
    ${section("Running", groups.running, "running")}
    ${section("Transitioning", groups.pending, "pending")}
    ${section("Stopped", groups.stopped, "stopped")}
    ${section("State unknown", groups.unknown, "unknown")}
    ${units.length ? `<div class="toolbar updates-bar">
      <button class="btn" id="btn-check-updates">Check image updates</button>
      <span class="muted">${esc(updateSummary)}</span>
    </div>` : ""}`;

  const $chk = document.getElementById("btn-check-updates");
  if ($chk) {
    $chk.addEventListener("click", async () => {
      $chk.disabled = true;
      $chk.textContent = "checking registries…";
      try {
        const { body } = await api("/api/updates");
        updateInfo = {};
        let available = 0, checked = 0;
        for (const row of body.updates || []) {
          updateInfo[`${row.scope}/${row.name}`] = row;
          if (!row.note) checked++;
          if (row.updateAvailable) available++;
        }
        updateSummary = available
          ? `${available} image update${available === 1 ? "" : "s"} available (${checked} tags checked)`
          : `all ${checked} checked tags up to date`;
        if ((body.skippedScopes || []).length) {
          updateSummary += ` — remote scopes skipped: ${body.skippedScopes.join(", ")}`;
        }
      } catch (e) {
        toast(e.message, true);
      }
      renderDashboard();
    });
  }

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
  const loop = u.restarts > 0 && (cls === "failed" || u.sub === "auto-restart");
  return `
  <a class="card ${cls}" href="#/unit/${encodeURIComponent(u.scope)}/${encodeURIComponent(u.name)}">
    <div class="card-head">
      <span class="dot"></span>
      <span class="card-name">${esc(u.name)}</span>
      <span class="badge">${esc(u.kind)}</span>
      ${u.scope !== "system" ? `<span class="badge badge-user" title="rootless unit of ${esc(u.scope)}">${esc(u.scope)}</span>` : ""}
      ${loop ? `<span class="badge badge-loop" title="service restarted ${u.restarts} times — likely a crash loop">↻${u.restarts}</span>` : ""}
      ${u.gpus && u.gpus.length ? `<span class="badge badge-gpu" title="${esc(u.gpus.join(", "))}">gpu</span>` : ""}
      ${updateInfo[`${u.scope}/${u.name}`]?.updateAvailable ? `<span class="badge badge-update" title="registry serves a newer digest for ${esc(u.image)}">update</span>` : ""}
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

function validationHTML(v, hints) {
  let out = "";
  if (v) {
    out += `<pre class="output ${v.valid ? "ok" : "err"}">${esc(
      (v.available ? (v.valid ? "✓ valid" : "✗ invalid") : "validator unavailable") +
      (v.output ? "\n\n" + v.output : ""))}</pre>`;
  }
  for (const h of hints || []) {
    out += `<p class="banner banner-warn">${esc(h)}</p>`;
  }
  return out;
}

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
  const loop = unit.restarts > 0;
  $app.innerHTML = `
    <div class="detail-head">
      <a class="btn btn-sm" href="#/">←</a>
      <h1><span class="dot ${cls}"></span> ${esc(unit.name)}</h1>
      <span class="badge">${esc(unit.kind)}</span>
      ${scope !== "system" ? `<span class="badge badge-user">${esc(scope)}</span>` : ""}
      ${loop ? `<span class="badge badge-loop" title="restart count since last stop">↻${unit.restarts}</span>` : ""}
      ${(unit.gpus || []).map(g => `<span class="badge badge-gpu">${esc(g)}</span>`).join("")}
      <span class="state">${esc(stateLabel(unit))}${unit.unitFile ? " · " + esc(unit.unitFile) : ""}</span>
    </div>
    <p class="muted mono">${esc(unit.path)}${unit.readOnly ? " (read-only)" : ""}</p>
    ${updateInfo[`${scope}/${name}`]?.updateAvailable ? `
    <div class="banner banner-warn update-banner">
      The registry serves a newer digest for <code>${esc(unit.image || "this image")}</code>.
      <button class="btn btn-accent btn-sm" id="btn-pull-update">Pull new image + restart</button>
    </div>` : ""}
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
        ${unit.kind === "container" && !unit.readOnly ? `
        <select id="gpu-helper" class="input" title="insert a GPU attachment into [Container]">
          <option value="">Add GPU…</option>
          <option value="nvidia">NVIDIA — all GPUs (CDI)</option>
          <option value="vaapi">Intel/AMD video (VAAPI, /dev/dri)</option>
          <option value="rocm">AMD compute (ROCm)</option>
        </select>` : ""}
      </div>
      <div id="validation"></div>
    </section>
    <section>
      <h2>History</h2>
      <div id="history"><p class="muted">loading…</p></div>
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
  enhanceEditor($editor);
  const $validation = document.getElementById("validation");

  document.getElementById("btn-validate").addEventListener("click", async () => {
    try {
      const { body } = await api("/api/validate", {
        method: "POST",
        body: JSON.stringify({ scope, name, content: $editor.value }),
      });
      $validation.innerHTML = validationHTML(body.validation, body.hints);
    } catch (e) { toast(e.message, true); }
  });

  let savedContent = content;

  async function doSave() {
    try {
      const { status, body } = await api(`/api/units/${encodeURIComponent(scope)}/${encodeURIComponent(name)}`, {
        method: "PUT",
        body: JSON.stringify({ content: $editor.value, restart: document.getElementById("chk-restart").checked }),
      });
      $validation.innerHTML = validationHTML(body.validation, body.hints);
      if (status === 422) { toast("rejected by validator", true); return; }
      savedContent = $editor.value;
      (body.warnings || []).forEach(warning => toast(warning, true));
      toast(`saved ${name} + daemon-reload`);
    } catch (e) { toast(e.message, true); }
  }

  // The PRD save flow: show what will change on disk, then
  // write -> daemon-reload (-> restart).
  document.getElementById("btn-save").addEventListener("click", () => {
    if ($editor.value === savedContent) {
      toast("no changes to save");
      return;
    }
    $validation.innerHTML = `
      <h2>Review changes</h2>
      ${diffHTML(savedContent, $editor.value)}
      <div class="toolbar">
        <button class="btn btn-accent" id="btn-confirm-save">Confirm save + reload</button>
        <button class="btn" id="btn-cancel-save">Cancel</button>
      </div>`;
    document.getElementById("btn-confirm-save").addEventListener("click", doSave);
    document.getElementById("btn-cancel-save").addEventListener("click", () => { $validation.innerHTML = ""; });
  });

  const $pullUpdate = document.getElementById("btn-pull-update");
  if ($pullUpdate) {
    $pullUpdate.addEventListener("click", async () => {
      $pullUpdate.disabled = true;
      $pullUpdate.textContent = "pulling…";
      try {
        const { body } = await api(`/api/units/${encodeURIComponent(scope)}/${encodeURIComponent(name)}/update`,
          { method: "POST", body: "{}" });
        (body.warnings || []).forEach(warning => toast(warning, true));
        toast(`pulled ${body.pulled} and restarted`);
        delete updateInfo[`${scope}/${name}`];
        render();
      } catch (e) {
        toast(e.message, true);
        $pullUpdate.disabled = false;
        $pullUpdate.textContent = "Pull new image + restart";
      }
    });
  }

  const $gpuHelper = document.getElementById("gpu-helper");
  if ($gpuHelper) {
    const GPU_SNIPPETS = {
      nvidia: ["AddDevice=nvidia.com/gpu=all"],
      vaapi: ["AddDevice=/dev/dri"],
      rocm: ["AddDevice=/dev/dri", "AddDevice=/dev/kfd"],
    };
    $gpuHelper.addEventListener("change", () => {
      const lines = GPU_SNIPPETS[$gpuHelper.value];
      $gpuHelper.value = "";
      if (!lines) return;
      $editor.value = insertIntoSection($editor.value, "Container", lines);
      $editor.dispatchEvent(new Event("input")); // refresh highlighting
      if (lines[0].includes("nvidia")) {
        toast("CDI attachment added — requires nvidia-container-toolkit with generated CDI specs on the host");
      }
    });
  }

  loadHistory(scope, name, () => $editor.value);

  startLogs(scope, name);
  document.getElementById("chk-follow").addEventListener("change", () => startLogs(scope, name));
}

// insertIntoSection adds lines right below the [section] header, creating
// the section at the end when it's missing.
function insertIntoSection(text, section, lines) {
  const marker = `[${section}]`;
  const idx = text.indexOf(marker);
  if (idx < 0) {
    return text.trimEnd() + `\n\n${marker}\n${lines.join("\n")}\n`;
  }
  const nl = text.indexOf("\n", idx + marker.length);
  const pos = nl < 0 ? text.length : nl + 1;
  return text.slice(0, pos) + lines.join("\n") + "\n" + text.slice(pos);
}

async function loadHistory(scope, name, currentContent) {
  const $hist = document.getElementById("history");
  let body;
  try {
    body = (await api(`/api/units/${encodeURIComponent(scope)}/${encodeURIComponent(name)}/history`)).body;
  } catch (e) {
    $hist.innerHTML = `<p class="banner banner-warn">${esc(e.message)}</p>`;
    return;
  }
  if (!body.enabled) {
    $hist.innerHTML = `<p class="muted">Git history is off for this scope — start Rookery with <code>-git</code>
      (or make the unit directory a git repository) to record every save and enable rollback.</p>`;
    return;
  }
  const commits = body.commits || [];
  if (!commits.length) {
    $hist.innerHTML = `<p class="muted">No commits for this unit yet; the next save will create one.</p>`;
    return;
  }
  $hist.innerHTML = commits.map(c => `
    <div class="history-row" data-hash="${esc(c.hash)}">
      <span class="mono muted">${esc(c.hash.slice(0, 10))}</span>
      <span class="muted">${new Date(c.time * 1000).toLocaleString()}</span>
      <span class="hist-subject">${esc(c.subject)}</span>
      <span class="actions">
        <button class="btn btn-sm hist-diff">diff</button>
        <button class="btn btn-sm hist-restore">restore</button>
      </span>
    </div>`).join("") + `<div id="hist-view"></div>`;

  const $view = document.getElementById("hist-view");
  $hist.querySelectorAll(".history-row").forEach(row => {
    const hash = row.dataset.hash;
    const short = hash.slice(0, 10);
    row.querySelector(".hist-diff").addEventListener("click", async () => {
      try {
        const { body } = await api(`/api/units/${encodeURIComponent(scope)}/${encodeURIComponent(name)}/history/${hash}`);
        $view.innerHTML = `<p class="muted">what restoring ${esc(short)} would change (current → ${esc(short)}):</p>` +
          diffHTML(currentContent(), body.content);
      } catch (e) { toast(e.message, true); }
    });
    row.querySelector(".hist-restore").addEventListener("click", async () => {
      if (!confirm(`Restore ${name} to ${short}? The content is validated, written to disk, and daemon-reload runs.`)) return;
      try {
        const { status, body } = await api(`/api/units/${encodeURIComponent(scope)}/${encodeURIComponent(name)}/rollback`, {
          method: "POST",
          body: JSON.stringify({ commit: hash }),
        });
        if (status === 422) {
          toast("rollback rejected: that revision no longer validates on this host", true);
          return;
        }
        (body.warnings || []).forEach(warning => toast(warning, true));
        toast(`restored ${name} to ${short}`);
        render();
      } catch (e) { toast(e.message, true); }
    });
  });
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

async function fetchScopes() {
  try {
    const { body } = await api("/api/host");
    return body.scopes || ["system"];
  } catch { return ["system"]; }
}

async function renderNew() {
  const scopes = await fetchScopes();
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
    <div id="validation"></div>`;

  const $kind = document.getElementById("new-kind");
  const $editor = document.getElementById("editor");
  const syncHl = enhanceEditor($editor);
  $kind.addEventListener("change", () => { $editor.value = TEMPLATES[$kind.value]; syncHl(); });

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
      $validation.innerHTML = validationHTML(body.validation, body.hints);
      if (status === 422) { toast("rejected by validator", true); return; }
      toast(`created ${name}`);
      location.hash = `#/unit/${encodeURIComponent(scope)}/${encodeURIComponent(name)}`;
    } catch (e) { toast(e.message, true); }
  });
}

/* ---------- import / convert ---------- */

const IMPORT_MODES = {
  run: {
    label: "podman run command",
    help: "Paste a `podman run ...` (or `docker run ...`) command; multi-line with backslashes is fine.",
    placeholder: "podman run -d --name jellyfin -p 8096:8096 -v /srv/media:/media:Z docker.io/jellyfin/jellyfin:latest",
  },
  compose: {
    label: "compose file",
    help: "Paste a docker-compose / podman-compose YAML file. Each service becomes a .container unit; declared volumes and networks become .volume/.network units.",
    placeholder: "services:\n  app:\n    image: ...",
  },
  container: {
    label: "running container",
    help: "Import an existing container's configuration as a Quadlet unit. The container itself is not touched — stop it once the unit runs.",
  },
};

async function renderImport() {
  const scopes = await fetchScopes();
  $app.innerHTML = `
    <div class="detail-head"><a class="btn btn-sm" href="#/">←</a><h1>Import to Quadlet</h1></div>
    <div class="toolbar">
      <select id="imp-kind" class="input">
        ${Object.entries(IMPORT_MODES).map(([k, m]) => `<option value="${k}">${m.label}</option>`).join("")}
      </select>
      <select id="imp-scope" class="input" title="scope for created units">
        ${scopes.map(s => `<option>${esc(s)}</option>`).join("")}
      </select>
    </div>
    <p id="imp-help" class="muted"></p>
    <div id="imp-input"></div>
    <div class="toolbar"><button class="btn btn-accent" id="btn-convert">Convert</button></div>
    <div id="imp-results"></div>`;

  const $kind = document.getElementById("imp-kind");
  const $input = document.getElementById("imp-input");
  const $help = document.getElementById("imp-help");
  const $results = document.getElementById("imp-results");

  async function renderInput() {
    const kind = $kind.value;
    $help.textContent = IMPORT_MODES[kind].help;
    $results.innerHTML = "";
    if (kind === "container") {
      $input.innerHTML = `<p class="muted">loading containers…</p>`;
      try {
        const { body } = await api("/api/import/containers");
        const rows = body.containers || [];
        if (!rows.length) {
          $input.innerHTML = `<p class="banner banner-warn">No containers found via the Podman API socket.</p>`;
          return;
        }
        $input.innerHTML = `
          <select id="imp-container" class="input">
            ${rows.map(c => `<option value="${esc(c.id)}" ${c.managed ? "disabled" : ""}>
              ${esc(c.name)} — ${esc(c.image)} (${esc(c.state)})${c.managed ? " — already systemd-managed" : ""}
            </option>`).join("")}
          </select>`;
      } catch (e) {
        $input.innerHTML = `<p class="banner banner-error">${esc(e.message)}</p>`;
      }
    } else {
      $input.innerHTML = `<textarea id="imp-text" spellcheck="false" placeholder="${esc(IMPORT_MODES[kind].placeholder)}"></textarea>`;
    }
  }

  $kind.addEventListener("change", renderInput);
  await renderInput();

  document.getElementById("btn-convert").addEventListener("click", async () => {
    const kind = $kind.value;
    const input = kind === "container"
      ? (document.getElementById("imp-container") || {}).value
      : (document.getElementById("imp-text") || {}).value;
    if (!input) { toast("nothing to convert", true); return; }
    $results.innerHTML = `<p class="muted">converting…</p>`;
    try {
      const { status, body } = await api("/api/convert", {
        method: "POST",
        body: JSON.stringify({ kind, input }),
      });
      if (status === 422) {
        $results.innerHTML = `<p class="banner banner-error">${esc(body.error || "conversion failed")}</p>`;
        return;
      }
      renderResults(body.units || []);
    } catch (e) {
      $results.innerHTML = `<p class="banner banner-error">${esc(e.message)}</p>`;
    }
  });

  function renderResults(units) {
    $results.innerHTML = `<h2>${units.length} unit${units.length === 1 ? "" : "s"} generated — review, then create</h2>` +
      units.map((u, i) => `
      <section class="import-unit" data-i="${i}">
        <div class="toolbar">
          <input class="input imp-name" value="${esc(u.name)}">
          <button class="btn btn-accent imp-create">Create</button>
          <span class="imp-status muted"></span>
        </div>
        ${(u.warnings || []).map(w => `<p class="banner banner-warn">${esc(w)}</p>`).join("")}
        <textarea class="imp-editor" spellcheck="false">${esc(u.content || "")}</textarea>
      </section>`).join("");

    $results.querySelectorAll(".import-unit").forEach(sec => {
      const $editor = sec.querySelector(".imp-editor");
      enhanceEditor($editor);
      sec.querySelector(".imp-create").addEventListener("click", async () => {
        const name = sec.querySelector(".imp-name").value.trim();
        const scope = document.getElementById("imp-scope").value;
        const $status = sec.querySelector(".imp-status");
        try {
          const { status, body } = await api(`/api/units/${encodeURIComponent(scope)}/${encodeURIComponent(name)}`, {
            method: "PUT",
            body: JSON.stringify({ content: $editor.value }),
          });
          if (status === 422) {
            $status.innerHTML = `<span class="warn">rejected by validator</span>`;
            sec.insertAdjacentHTML("beforeend", validationHTML(body.validation, body.hints));
            return;
          }
          $status.innerHTML = `created — <a href="#/unit/${encodeURIComponent(scope)}/${encodeURIComponent(name)}">open ${esc(name)}</a>`;
          (body.hints || []).forEach(h => toast(h, true));
        } catch (e) {
          $status.innerHTML = `<span class="warn">${esc(e.message)}</span>`;
        }
      });
    });
  }
}

/* ---------- router ---------- */

async function render() {
  stopStreams();
  if (authState.required && !authState.authenticated) {
    renderLogin();
    return;
  }
  renderHostStrip();
  const hash = location.hash || "#/";
  const parts = hash.slice(2).split("/").filter(Boolean).map(decodeURIComponent);
  try {
    if (parts[0] === "unit" && parts.length === 3) {
      await renderUnit(parts[1], parts[2]);
    } else if (parts[0] === "new") {
      await renderNew();
    } else if (parts[0] === "import") {
      await renderImport();
    } else {
      await renderDashboard();
      refreshTimer = setInterval(() => {
        if (!document.hidden) { renderDashboard(); renderHostStrip(); }
      }, 5000);
    }
  } catch (e) {
    if (!authState.authenticated) return; // login view already rendered
    $app.innerHTML = `<p class="banner banner-error">${esc(e.message)}</p>`;
  }
}

window.addEventListener("hashchange", render);
checkAuth().then(render);
