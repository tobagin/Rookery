# PRD — Rookery

**Rookery — a Quadlet-native web UI for Podman hosts.** (A rookery is where a pod of seals gathers.)
Version 0.2 · 2026-07-04 · Author: tobagin · License: Apache-2.0

---

## 1. Problem

Podman is the default container runtime on Fedora/RHEL and Red Hat is pushing
Quadlets (systemd-managed containers defined by `.container`/`.pod`/`.network`
unit files) as the canonical way to run them. But the management-UI ecosystem
ignores this:

- **Arcane, Dockhand, Portainer** speak the Docker API. Pointed at Podman's
  compatibility socket they see plain containers only — Quadlets, Pods,
  rootless multi-user setups and SELinux contexts are invisible or read-only.
- **cockpit-podman** can start/stop/restart Quadlets and show logs, but cannot
  create or edit the unit files that define them.
- **Podman Desktop** manages Quadlets fully but is a developer desktop app —
  wrong shape for a headless server.
- **quadletman / Podman+** validate the demand but are single-maintainer,
  early-stage projects.
- As of June 2026, **no web UI manages GPU-attached Quadlets at all**.

The result: anyone running a Fedora/RHEL homelab or small fleet "the right
way" (Quadlets + systemd) manages it over SSH with a text editor, while
Docker users get half a dozen polished UIs.

## 2. Product thesis

Don't clone Portainer against the Docker API — that lane is commoditized.
Build the tool that treats **unit files on disk as the single source of
truth** (the same on-disk, Git-friendly model that made Arcane compelling for
compose), and make the UI a thin, honest layer over systemd + Podman.
Delete the daemon, keep the workflow.

## 3. Target users

| Persona | Situation | What they need |
|---|---|---|
| Homelab operator (primary) | 10–50 containers on 1–3 Fedora/Debian boxes, migrating from Docker/Portainer or Pi-based setups | See everything at a glance, edit Quadlets in the browser, survive reboots, phone-friendly |
| Small-org sysadmin | A handful of RHEL/Alma servers, no Kubernetes budget or appetite | Rootless multi-tenant, SELinux done right, agentless multi-host, audit trail via Git |
| GPU self-hoster | Jellyfin/Immich/LLM/game-streaming (Wolf) workloads | Attach and monitor GPUs on Quadlets — currently served by nobody |

**Non-target:** Kubernetes/OpenShift shops; Docker-only users (Arcane already
serves them well).

## 4. Goals / Non-goals

**Goals**
1. Full Quadlet lifecycle in a browser: list, create, edit, validate, start,
   stop, logs, health — including `.pod`, `.network`, `.volume` units.
2. Files on disk remain authoritative; the tool never hides state in its own
   database. A user can always fall back to SSH + `systemctl` with zero loss.
3. Rootless-first: manage units for multiple Linux users, not just root.
4. Single static Go binary (or one container), arm64 + x86_64, no external
   dependencies (no Postgres/Redis/NATS stack).
5. GPU visibility: show device attachments, VRAM/utilization per container.

**Non-goals (v1)**
- Docker API support (compatibility-socket read-only view at most).
- Kubernetes anything.
- Image building, registries, CI — out of scope; Podman CLI does this.
- RBAC/SSO beyond a single admin login (v2, and keep it free if built).

## 5. Core features

### MVP (v0.x)
- **Dashboard**: units by state (running/stopped/failed), host metrics strip.
- **Quadlet editor**: syntax-highlighted editor for `.container`/`.pod`/
  `.network`/`.volume` files with `podman-system-generator --dryrun`
  validation before save; write → `daemon-reload` → restart flow with diff
  preview.
- **Generator/importer**: convert `podman run` commands and compose files
  into Quadlet units (wraps `podlet`); import existing running containers.
- **Lifecycle & logs**: start/stop/restart/enable, live `journalctl` streaming,
  exit-code and restart-loop surfacing.
- **Rootless awareness**: enumerate configured users' `~/.config/containers/systemd/`,
  act via each user's systemd session; SELinux label hints on volume mounts.
- **Mobile-responsive UI** (the "restart it from the couch" test).

### v1
- **Agentless multi-host over SSH** (like Ansible: no agent to babysit).
- **Git integration**: unit-file directory as a repo; commit on save, show
  history, one-click rollback.
- **GPU panel**: per-container device attachments, utilization/VRAM via
  nvidia-smi / amdgpu exporters; add-GPU-to-unit helper (CDI syntax).
- **Update checks**: image digest drift per unit, one-click pull + restart.

### Later / v2
- Multi-admin auth (OIDC), read-only share links, alerting hooks
  (ntfy/Telegram), pod-level composition view, secrets integration
  (`podman secret` + systemd credentials).

## 6. Architecture sketch

- **Backend**: Go. Talks to (a) systemd via D-Bus (per-user sessions included),
  (b) Podman via its native REST socket (not the Docker shim), (c) the
  filesystem for unit files, (d) `journald` for logs. No database — state on
  disk plus an optional small bbolt cache.
- **Frontend**: single-page app served from the same binary; WebSocket for
  logs/stats streams.
- **Deploy**: one binary + a systemd unit (dogfood: ship it as a Quadlet).
  Rootful install manages all users; rootless install manages self only.
- **Multi-host**: SSH out from the primary node, run a bundled static helper
  remotely (pattern proven by Ansible/rport) — no long-lived agents.

## 7. Competitive landscape (June 2026)

| Tool | Quadlet create/edit | Rootless multi-user | GPU | Server web UI | Maturity |
|---|---|---|---|---|---|
| cockpit-podman | ✗ (run only) | partial | ✗ | ✓ | high |
| Arcane | ✗ | ✗ | ✗ | ✓ | mid |
| Podman Desktop | ✓ | n/a | partial | ✗ (desktop) | high |
| quadletman | ✓ | ✓ | ✗ | ✓ | very early |
| Podman+ (cockpit app) | ✓ | partial | partial | ✓ | early |
| **Rookery** | ✓ | ✓ | ✓ | ✓ | — |

Moat: none of the mature tools can pivot cheaply — Cockpit is constrained by
its framework, Arcane by its Docker-API foundation, Podman Desktop by being a
desktop app.

## 8. Success metrics

- MVP: manage a real 27-container migration end-to-end without touching SSH
  except for install (dogfood target: the N5 Air NAS).
- 1k GitHub stars / first outside contributor within 6 months of public beta.
- Referenced as the Quadlet answer in the r/selfhosted "Portainer
  alternatives" threads that today have no good reply.

## 9. Risks

| Risk | Mitigation |
|---|---|
| Red Hat ships Quadlet editing in Cockpit | Ship first; differentiate on GPU, Git, multi-host, UX. Cockpit's cadence is slow and framework-bound. |
| Sustainability (this is reputation-ware, not revenue-ware) | Scope ruthlessly: no daemon, no DB, no agents = low maintenance surface. GitHub Sponsors; never paywall features (that's the wound Portainer died of). |
| Quadlet spec churn across Podman versions | Validate via the host's own `podman-system-generator`, not a vendored parser. |
| Single-maintainer burnout (see quadletman) | MVP must be useful standalone even if development stops — files-on-disk model guarantees no lock-in. |

## 10. Decisions & open questions

**Decided**
- **Name**: Rookery — where a pod of seals gathers (Podman's mascot is a pod
  of selkies). No collision with existing container tooling (checked
  2026-07-04).
- **Repo description**: "Quadlet-native web UI for Podman — manage systemd
  containers, pods, and GPUs from your browser, with unit files on disk as
  the single source of truth."
- **License**: Apache-2.0. Matches the Podman/Buildah/Skopeo ecosystem,
  enterprise-frictionless, explicit patent grant. AGPL rejected: its
  protection targets SaaS rehosting, which doesn't apply to an inherently
  self-hosted tool, while its adoption tax is real.

**Open**
- Whether to absorb quadletman/Podman+ maintainers rather than compete —
  the niche is too small for three half-finished tools.
- Namespace grabs before announcement: github.com/rookery (or org), domain
  (getrookery.app?), Fedora COPR name.

## 11. Rollout

1. **Weeks 1–4**: spike — D-Bus/systemd control of user sessions, unit-file
   editor with generator validation, single host, read-only GPU info.
2. **Weeks 5–8**: MVP feature-complete; dogfood on the N5 Air (migrate the 27
   Pi containers to Quadlets using only the tool).
3. **Public alpha**: post to r/selfhosted + Podman Discourse; iterate on the
   importer (the migration story is the growth engine).
4. **v1**: multi-host + Git + GPU panel; submit to Fedora COPR and Flathub
   (Cockpit-style bridge later if demanded).
