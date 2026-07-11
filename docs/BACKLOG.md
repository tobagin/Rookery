# Backlog — missing features & QoL

**Status (2026-07-09): all tiers implemented and verified.** Known accepted
limitation: backup archives are unsigned, so restore integrity checking only
catches checksum *mismatches* — an entry omitted from the manifest's sha256
map skips verification (`internal/server/backup.go`). Closing that fully
needs archive signing; revisit if backups ever cross a trust boundary.

**Shipped since (through 2026-07-11), beyond this list:** the rookery-agent
connector (multi-scope, full read/write parity, host metrics + GPUs); typed
resource pages (Containers/Pods/Networks/Volumes/Images, Updates folded into
Images); live podman resources with managed/unmanaged and used/unused flags,
inspect overlays, and unmanaged-object delete; node-scoped management (global
node picker scoping every view and action, Fleet "manage" shortcut); prune of
all unused images across nodes; runtime ssh node add/remove; per-node display
names/colors; CodeMirror editor with Quadlet completion. Remaining agent
gaps: heartbeat/last-seen, event streaming, agent-side dangling-image prune.

Prioritized work list derived from a full code/PRD gap analysis (2026-07-09).
Each item is self-contained: context, pointers, and done-criteria. Work top to
bottom; items within a tier are independent unless noted.

Conventions for whoever (or whatever) picks these up:

- Unit files on disk stay the single source of truth. Never store workload
  state in `rookery.db`.
- Backend routes live in `internal/server/server.go`; handlers per-domain in
  sibling files. Frontend is one SPA: `web/src/App.tsx`.
- Admin-only mutations must go through the existing role gate in
  `ServeHTTP` (`internal/server/server.go:233`) and write an audit event.
- Keep remote (ssh) parity in mind; when a feature is local-only, the UI must
  say so instead of showing nothing.

---

## Tier 1 — trust gaps (fix before alpha)

### 1.1 Backup restore endpoint

Export exists (`GET /api/backup`, `internal/server/backup.go:22`) and writes a
manifest that nothing consumes. There is no restore path, but DOGFOOD.md's
recovery pass and the PRD ("restore verification") assume one.

- Add `POST /api/restore` (admin): accepts the tar.gz produced by
  `/api/backup`, validates the manifest, restores `rookery.db` metadata and
  managed Quadlet files. Quadlet writes must reuse the existing validated
  write path (validate → atomic write → `daemon-reload`), not raw untar.
- Add a dry-run mode that reports what would change (files added/overwritten)
  before applying.
- UI: Settings → Backup gets an upload + preview + confirm flow.
- Done when: export → wipe test host → restore → accounts, settings, and unit
  files return; a tampered/mismatched manifest is rejected.

### 1.2 Self-service password change

After one-time onboarding (`internal/server/auth.go:363`), only admins can
reset passwords (`internal/server/users.go:101`). Non-admin and second-admin
accounts can never change their own.

- Add `POST /api/me/password` (any authenticated non-share session): requires
  current password, sets new one, invalidates the user's other sessions,
  writes an audit event, revokes share links (match existing password-change
  semantics).
- UI: small "Change password" form (account menu or Settings visible to
  non-admins).
- Done when: a viewer account can rotate its own password without an admin.

### 1.3 Persist sessions across restarts

Sessions are in-memory (`internal/server/auth.go:38`); every upgrade logs
everyone out.

- Store sessions in `rookery.db` (id hash, user, expiry, last-seen); keep the
  sliding TTL behavior. In-memory cache in front is fine.
- Expired-session cleanup on startup or lazily on access.
- Done when: restart Rookery, existing browser session still works.

### 1.4 Lifecycle action safety

`UnitRow.action` (`web/src/App.tsx:673`) and `UnitDetail.lifecycle`
(`App.tsx:746`) fire immediately, keep buttons enabled during the request
(double-fire), and show no pending state.

- Disable the acting button and show a spinner while the request is in
  flight (per-row state, not the global overlay).
- Confirm before `stop` on a running unit (skip for start/restart).
- Done when: rapid double-click issues one request; stop asks first.

### 1.5 Editor unsaved-changes guard

`changed` state (`App.tsx:800`) only gates the Save button; navigating away
discards edits silently.

- Add `beforeunload` + route-leave confirmation while `changed` is true, in
  `EditorTab`, `NewUnitForm`, and `ImportResult` editors.
- Done when: navigating away from a dirty editor prompts.

### 1.6 Log stream resilience

SSE follow mode fails silently (`App.tsx:961`) and the buffer string grows
unbounded (`App.tsx:959`).

- On stream error while following: show a "stream lost — reconnecting"
  indicator and retry with backoff.
- Cap the client buffer (e.g. last 5000 lines, matching the server cap in
  `internal/server/api.go:666`).
- Done when: killing the connection mid-follow shows the indicator and
  recovers; an hour-long follow doesn't grow memory unbounded.

---

## Tier 2 — make the dashboard truthful

### 2.1 Per-container CPU/mem stats

The Podman client only reads version + counts (`internal/podman/podman.go:20`).
No per-container stats anywhere, while the PRD promises per-container
GPU/VRAM visibility — CPU/mem is the same story.

- Backend: call Podman's stats endpoint over the existing socket client
  (one-shot sample, not a streaming daemon); map container → unit via the
  existing unit/container correlation used by update checks.
- Expose on the unit detail payload and as a `GET /api/stats` batch for the
  units list.
- UI: CPU/mem columns or badges on unit rows and the unit overview tab.
- Remote: reuse the ssh-script pattern (`podman stats --no-stream --format
  json` over ssh), same as remote GPU probing (`internal/server/api.go:706`).
- Done when: a busy container shows nonzero CPU/mem locally and on a remote.

### 2.2 Remote host metrics

`/api/host` reads local `/proc` only (`internal/hostinfo`); Fleet shows unit
counts but no CPU/mem/load/uptime for remotes.

- Extend the remote probe script (`internal/rhost`) to snapshot
  `/proc/loadavg`, `/proc/meminfo`, `/proc/stat`, uptime; surface per-node
  metrics in `GET /api/nodes`.
- UI: metric chips per node on the Fleet page.
- Done when: Fleet shows live-ish CPU/mem/load per remote node.

### 2.3 Healthcheck surfacing

Unit state carries Active/Sub/ExitCode/Restarts only (`internal/server/api.go:25`).
Container health (`HealthCmd` results) is invisible.

- Read container health status from Podman inspect (local socket; ssh for
  remote) for units whose Quadlet declares a healthcheck.
- Add `health` to the unit JSON; show a badge (healthy/unhealthy/starting) on
  rows and detail; unhealthy units join the dashboard "needs attention" panel.
- Done when: a container with a failing healthcheck shows unhealthy while
  systemd still reports active.

### 2.4 UnitDetail live state

The unit detail overview loads once and never polls (`App.tsx:744`) — stale
during the exact moments users watch it (restarts).

- Poll the unit endpoint every 5s while the tab is visible (reuse the
  `document.hidden` pause pattern from `useUnits`, `App.tsx:540`).
- Done when: restarting a unit updates its detail page state without
  navigation.

---

## Tier 3 — daily-driver features

### 3.1 Bulk unit actions

No multi-select anywhere. PRD lists bulk operations explicitly.

- Backend: `POST /api/units/bulk-action` (admin): list of `{scope, name}` +
  action (start/stop/restart); execute sequentially per host, return per-unit
  results; one audit event with the set.
- UI: checkboxes on the Units list (and failed view), select-all-within-
  filter, action bar with result summary. Confirm before bulk stop/restart.
- Done when: selecting 5 units and hitting restart restarts all 5 and reports
  any per-unit failure individually.

### 3.2 Update all

`GET /api/updates` finds drift; applying is one unit at a time
(`internal/server/updates.go:124`).

- Backend: `POST /api/updates/apply` (admin) taking a list of unit refs (or
  `all-drifted`), running pull+restart per unit with the existing per-unit
  path, bounded concurrency, per-unit results.
- UI: "Update all" (and update-selected once 3.1 lands) on the Updates page
  with a progress/result list.
- Done when: three drifted units update from one click with per-unit outcomes.

### 3.3 One-click container adopt

`GET /api/import/containers` lists candidates but import is convert-only —
the client must convert, review, then PUT manually.

- Add an end-to-end flow: convert → present draft + warnings (existing UI) →
  single "adopt" action that writes the unit via the normal validated PUT,
  and offers to stop the original container and start the Quadlet.
- Keep it explicit — never auto-stop the source container without a checkbox.
- Done when: a running container becomes a managed Quadlet from one screen.

### 3.4 Runtime node management

Remotes come only from the `-remotes` flag; every DB-backed setting is
`RestartRequired: true` (`cmd/rookery/main.go:318`); no node add/remove API.

- Make remotes hot-reloadable: `POST/DELETE /api/nodes` (admin) persisting to
  `rookery.db` settings, rebuilding the area/scope set without restart.
  Include a "test connection" probe before saving.
- Flag-provided remotes stay locked (current behavior); DB-provided ones are
  editable.
- Done when: adding a remote from the Fleet page shows its units without
  restarting Rookery.

---

## Tier 4 — the two surfaces people live in

### 4.1 Real editor (CodeMirror)

The editor is a plain `<textarea>` (`App.tsx:915`): no highlighting, line
numbers, find/replace, or Ctrl+S.

- Adopt CodeMirror 6 with an INI/systemd-unit grammar for all three editor
  sites (EditorTab, NewUnitForm, ImportResult). Keep the existing
  diff-review-before-save gate and `baseContent` conflict detection intact.
- Ctrl+S triggers the same review→save flow as the button.
- Autocomplete: static per-section Quadlet key list ([Container], [Pod],
  [Network], [Volume], [Kube], [Image], [Build], [Unit], [Service],
  [Install]) sourced from `podman-systemd.unit(5)`; keep it a data table, not
  a language server.
- Done when: keys highlight, Ctrl+S saves, typing `Pub` in [Container]
  offers `PublishPort=`.

### 4.2 Log viewer upgrades

`LogsTab` (`App.tsx:941`): fixed 200-line tail, no search, no download, no
time range, timestamps always on.

- Client-side filter box (substring, highlight matches) over the buffer.
- Download buffer as text; copy button.
- Tail-size selector (200/1000/5000) and optional `since` (server already
  caps at 5000, `internal/server/api.go:666` — plumb `since` through to
  journalctl).
- Timestamps toggle.
- Done when: you can grep, download, and widen a unit's logs from the UI.

### 4.3 Pod-aggregated logs

Pod members must be opened one at a time (`App.tsx:835`).

- For `.pod` units: a logs tab that streams all member units' journals
  interleaved by timestamp, each line prefixed with the member name.
  Server-side: one SSE stream multiplexing existing per-unit journal reads.
- Done when: a 3-member pod shows one merged, labeled log stream.

---

## Tier 5 — integration tier

### 5.1 Private registry auth for update checks

Drift checks are anonymous-only (`internal/registry/registry.go:4`); private
registries get nothing.

- Read Podman's own credential store (`${XDG_RUNTIME_DIR}/containers/auth.json`
  / `~/.config/containers/auth.json`; per managed user for rootless scopes)
  and use basic/bearer auth per registry. Never store credentials in
  `rookery.db`; read the file per check.
- Report "auth failed" distinctly from "no update".
- Done when: a private ghcr image shows drift status instead of an error.

### 5.2 API tokens

Only cookie sessions + share links exist. Scripts/automation have no auth.

- Admin-mintable bearer tokens: name, role (admin/viewer), optional expiry;
  hash stored in `rookery.db`; value shown once. `Authorization: Bearer`
  accepted alongside cookies; audit events carry the token name as actor.
- UI: Settings → Tokens (create, list with last-used, revoke).
- Done when: `curl -H "Authorization: Bearer …" /api/units` works and shows
  up in audit.

### 5.3 Prometheus /metrics

- `GET /metrics` (behind auth or a separate `-metrics-listen`): unit counts
  by state and scope, failed units, drift count, per-node reachability,
  build info. Hand-rolled text exposition is fine; no client library needed.
- Done when: Prometheus scrapes it and `rookery_units{state="failed"}` is
  graphable.

### 5.4 Alert QoL

`internal/alert/alert.go` supports ntfy/telegram/webhook with a fixed 30s
poll and active↔failed transitions only.

- "Send test notification" button in Settings (POST endpoint that fires a
  test message through the configured notifier).
- Configurable watch interval (`-alert-interval`, default 30s).
- Optional flap suppression: don't re-alert on a unit that failed <N times
  in M minutes since last alert (simple per-unit cooldown).
- Done when: the test button pings your phone; a crash-looping unit doesn't
  spam.

---

## Tier 6 — papercuts (batchable, small)

- **6.1** Non-loopback bind warning: `isLoopback` (`cmd/rookery/main.go:545`)
  is defined but unused. On startup, if listening beyond loopback without a
  reverse-proxy hint, log a prominent plain-HTTP warning.
- **6.2** Bootstrap admin email is hardcoded `admin@example.com`
  (`main.go:429`) — leave empty and require it at onboarding instead.
- **6.3** Make share-link TTL configurable (`-share-ttl`, default 7d;
  currently hardcoded in `internal/server/auth.go:24`).
- **6.4** Audit log retention: `-audit-retention` (default keep-all), pruning
  on startup; plus CSV/JSON export button on Settings → Audit.
- **6.5** Column sorting on Units, Updates, Audit, Users tables (client-side).
- **6.6** Put list filters in URL query params (Units, Updates, Audit) so
  filtered views are bookmarkable; keep localStorage for density/theme.
- **6.7** Add free-text search to Fleet, Policies, and Secrets lists.
- **6.8** Replace native `prompt()`/`confirm()` (node labels `App.tsx:1121`,
  waiver reason `App.tsx:1213`, password reset `App.tsx:1772`) with the
  existing overlay/dialog component + validation.
- **6.9** Keyboard shortcuts: `/` focuses search, `Esc` closes overlays
  (OperationOverlay currently can't be dismissed), Ctrl+S covered by 4.1.
- **6.10** Mobile bottom nav shows first 5 of 10 items (`App.tsx:328`) —
  reorder so Units/Failed/Updates/Logs-adjacent pages win; audit the "More"
  menu ordering.
- **6.11** Label local-only features in the UI for remote scopes (secrets,
  image prune, container import all silently no-op on remotes — say
  "local host only").
- **6.12** OIDC: support ES256 (and ideally EdDSA) id_token algs
  (`internal/oidc/oidc.go:237`); some Authelia/Zitadel setups default to ES.
- **6.13** FAQ/docs entry: no exec-into-container and no volume browsing, by
  design (mutations go through systemd) — Portainer migrants will look for
  exec on day one.

---

## Explicitly not doing (YAGNI, revisit on demand)

- Websockets — SSE + polling covers current needs.
- Exec/attach terminal — violates the "all mutations through systemd" model.
- Custom policy-rule builder — the three built-in checks + waivers are enough
  until users ask.
- Server-side per-user UI preferences — localStorage is fine pre-beta.
- Pagination — lists are small at target scale; add when a real fleet hurts.
