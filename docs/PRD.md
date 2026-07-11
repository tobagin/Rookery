# PRD — Rookery

**Rookery — a Quadlet-native web UI for Podman hosts.** (A rookery is where a pod of seals gathers.)
Version 0.6 · 2026-07-11 · Author: tobagin · License: Apache-2.0

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
Docker users get half a dozen polished UIs and a clear commercial product path.

## 2. Product thesis

Rookery should be **Portainer-class for Podman and Quadlet**, not a Portainer
clone aimed at the Docker API. The Docker lane is commoditized; the open lane
is a first-class server UI for Podman's real operating model: systemd-managed
Quadlet files, rootless users, SELinux, GPUs, and small fleets.

Build the tool that treats **unit files on disk as the single source of
truth** (the same on-disk, Git-friendly model that made Arcane compelling for
compose), and make the UI a thin, honest layer over systemd + Podman. Rookery
may keep local control-plane metadata for accounts, settings, fleet inventory,
licensing, audit, and UI-managed state, but it must never make that metadata
authoritative for workloads.

The commercial strategy is an edition model: keep Community genuinely useful
for Podman/Quadlet management, and monetize Enterprise fleet operations,
governance, optional agents, backups, audit, policy, and support. Enterprise
is free for up to three managed nodes and paid beyond that.

## 3. Target users

| Persona | Situation | What they need |
|---|---|---|
| Homelab operator (primary adoption wedge) | 10–50 containers on 1–3 Fedora/Debian boxes, migrating from Docker/Portainer or Pi-based setups | See everything at a glance, edit Quadlets in the browser, survive reboots, phone-friendly, no lock-in |
| Small-org sysadmin | A handful of RHEL/Alma servers, no Kubernetes budget or appetite | Rootless multi-tenant, SELinux done right, multi-host, audit trail via Git, simple auth |
| GPU self-hoster | Jellyfin/Immich/LLM/game-streaming workloads | Attach and monitor GPUs on Quadlets — currently served by nobody |
| Podman fleet operator | 4+ Podman hosts across teams, labs, edge boxes, or small production estates | Node inventory, optional agents, RBAC, audit, backups, drift detection, bulk operations |
| Kubernetes avoider | Needs team controls and repeatable deployments but does not want a cluster platform | A product-grade Podman control plane without forcing Kubernetes or Docker compatibility layers |

**Non-target:** Kubernetes/OpenShift shops that already want cluster-native
orchestration; Docker-only users who do not intend to move to Podman/Quadlet.

## 4. Goals / Non-goals

**Goals**
1. Full Quadlet lifecycle in a browser: list, create, edit, validate, start,
   stop, logs, health — including `.pod`, `.network`, `.volume`, `.kube`,
   `.image`, and `.build` units.
2. Files on disk remain authoritative for workloads; the tool never hides
   unit definitions or lifecycle state in its own database. A user can always
   fall back to SSH + `systemctl` with zero loss of workload control.
3. Rootless-first: manage units for multiple Linux users, not just root.
4. Single Go binary (or one container), arm64 + x86_64, no required external
   service dependencies (no mandatory Postgres/Redis/NATS stack).
5. GPU visibility: show device attachments, VRAM/utilization per container.
6. Product-grade fleet management for Podman hosts, including optional agents
   where SSH-only management is not enough.

**Non-goals (v1)**
- Docker API support beyond a compatibility-socket read-only view, if ever.
- Kubernetes or OpenShift management.
- Image building, registries-as-a-service, or CI orchestration; Podman and
  existing registry/Git providers do this already.
- Making licensing or Rookery metadata authoritative for workload operation.

## 5. Core features

### MVP / Community baseline (v0.x)
- **Dashboard**: units by state (running/stopped/failed), host metrics strip.
- **Quadlet editor**: syntax-highlighted editor for `.container`/`.pod`/
  `.network`/`.volume` files with `podman-system-generator --dryrun`
  validation before save; write → `daemon-reload` → restart flow with diff
  preview.
- **Generator/importer**: convert `podman run` commands and compose files
  into Quadlet units (native Go conversion, no podlet dependency); import
  existing running containers.
- **Lifecycle & logs**: start/stop/restart/enable, live `journalctl` streaming,
  exit-code and restart-loop surfacing.
- **Rootless awareness**: enumerate configured users' `~/.config/containers/systemd/`,
  act via each user's systemd session; SELinux label hints on volume mounts.
- **Mobile-responsive UI** (the "restart it from the couch" test).

### v1 / Product-ready Podman management
- **Agentless multi-host over SSH** (like Ansible): quick fleet onboarding
  without installing anything on the target.
- **Git integration**: unit-file directory as a repo; commit on save, show
  history, one-click rollback.
- **GPU panel**: per-container device attachments, utilization/VRAM via
  nvidia-smi / amdgpu / Intel tooling; add-GPU-to-unit helper (CDI syntax).
- **Update checks**: image digest drift per unit, one-click pull + restart.
- **Auth hardening**: multi-admin local accounts, viewer role, read-only
  share links, and optional OIDC SSO with claim-based admin mapping.
- **Secrets**: podman secret listing, create/delete, reference detection, and
  editor insertion helpers with write-only values.

### Enterprise / Fleet governance
- **Optional agents** *(shipped as `rookery-agent`)*: a per-host HTTP daemon
  (shared bearer token) serving every scope on its host — system plus each
  rootless user — with full read/write parity: units, lifecycle, logs, unit
  files, live stats, resources, host metrics, and GPUs. Quadlet files remain
  the source of truth. Heartbeat/last-seen and event streaming are still
  open.
- **Node groups and labels** *(shipped)*: organize hosts by environment,
  team, hardware, site, GPU class, or maintenance window; per-node display
  names and colors.
- **Node-scoped management** *(shipped)*: a global node picker scopes every
  view (dashboard, containers, pods, images, volumes, networks) and action
  (prune, update all) to one node; Fleet is the cross-node view.
- **Bulk operations** *(shipped for units and updates)*: controlled
  start/stop/restart across selected units; update-all with per-unit results.
- **Advanced RBAC**: permissions by node, group, scope, and action; team or
  group mapping from enterprise identity providers.
- **Audit and compliance**: durable audit log for logins, lifecycle actions,
  file changes, rollbacks, share links, user changes, and license events.
- **Backup and recovery**: scheduled backup of `ROOKERY_DATA_DIR` plus managed
  Quadlet directories to local or remote targets; restore verification.
- **Advanced GitOps**: repo sync, proposed changes, environment promotion,
  drift detection, and protected production workflows.
- **Policy**: fleet checks for privileged containers, unpinned images,
  unlabeled bind mounts, exposed ports, restart policy, and host capability
  mismatch.

### Later / v2+
- App catalog / templates for reusable Quadlet bundles.
- Systemd credentials alongside podman secrets.
- Pod-level log interleaving and fleet-wide log search.
- SAML/SCIM and deeper enterprise identity lifecycle if OIDC group mapping is
  not enough.

## 6. Editions and commercial model

- **Community**: no license required; useful Podman/Quadlet management for
  single-host and small self-managed setups. Core lifecycle, editing,
  validation, logs, importer, local accounts, and local-first operations must
  not become hostage features.
- **Enterprise Free**: full Enterprise feature set for up to **three managed
  nodes**. This is the default commercial adoption path for homelabs and small
  teams that want to evaluate the complete product. Local users and OIDC/SSO
  identities are unlimited; secure authentication is not a paid-seat boundary.
- **Enterprise Paid**: required above three managed nodes, sold on fleet
  scale, governance, audit, backup, optional agents, policy, and support.
- **Node definition**: a node is any host managed by Rookery, including the
  controller host. If the controller and agent run on the same host, it counts
  as one node.
- **Trust boundary**: exceeding a license limit may restrict Enterprise fleet
  features, but it must not stop workloads, delete Quadlet files, or prevent
  direct systemd/Podman operation outside Rookery.

## 7. Architecture sketch

- **Backend**: Go. Talks to (a) systemd via
  `systemctl` (per-user sessions via `--user --machine user@.host`),
  (b) Podman via its native REST socket (not the Docker shim), (c) the
  filesystem for unit files, (d) logs via `journalctl`, and (e) a local
  SQLite database for accounts, settings, fleet inventory, licensing, audit,
  and other durable Rookery-owned metadata. The database is not part of the
  workload source of truth; the server re-reads unit files from disk per
  request.
- **Frontend**: single-page app embedded in the same binary; Server-Sent
  Events for log streams, polled JSON for host/GPU stats, fleet views, and
  update/policy status.
- **Deploy**: one binary + a systemd unit (dogfood: ship it as a Quadlet).
  Rootful install manages all users; rootless install manages self only.
- **Agentless multi-host**: SSH out from the primary node; every remote
  operation is a single `ssh` invocation wrapping a POSIX shell script.
  This remains the simplest and most transparent multi-host path.
- **Optional agent** *(shipped)*: `rookery-agent`, a per-host HTTP daemon
  authenticated by a shared bearer token (`-agents alias=url` +
  `-agent-token` on the control plane), for hosts where persistent
  connectivity beats SSH-only operation. One agent serves all scopes on its
  host (system + each rootless user); it executes the same categories of
  local systemd/Podman/filesystem operations and does not own workload
  state.

## 8. Competitive landscape (June 2026)

| Tool | Quadlet create/edit | Rootless multi-user | GPU | Fleet / agents | Server web UI | Maturity |
|---|---|---|---|---|---|---|
| cockpit-podman | ✗ (run only) | partial | ✗ | ✗ | ✓ | high |
| Arcane | ✗ | ✗ | ✗ | Docker-focused | ✓ | mid |
| Portainer | ✗ | ✗ | ✗ | ✓ | ✓ | high |
| Podman Desktop | ✓ | n/a | partial | ✗ | ✗ (desktop) | high |
| quadletman | ✓ | ✓ | ✗ | ✗ | ✓ | very early |
| Podman+ (cockpit app) | ✓ | partial | partial | ✗ | ✓ | early |
| **Rookery** | ✓ | ✓ | ✓ | ✓ | ✓ | — |

Moat: none of the mature tools can pivot cheaply — Cockpit is constrained by
its framework, Arcane and Portainer by their Docker-API foundation, Podman
Desktop by being a desktop app. Rookery wins by being Podman-native,
Quadlet-native, rootless-aware, GPU-aware, and product-grade for fleets.

## 9. Success metrics

- MVP: manage a real 27-container migration end-to-end without touching SSH
  except for install (dogfood target: the N5 Air NAS).
- Public alpha: at least 10 outside installs with issue feedback across
  Fedora/RHEL/Debian-family hosts.
- Adoption: 1k GitHub stars / first outside contributor within 6 months of
  public beta.
- Positioning: referenced as the Quadlet answer in the r/selfhosted
  "Portainer alternatives" threads that today have no good reply.
- Commercial signal: at least 5 credible requests for 4+ node fleet features
  before implementing license enforcement.

## 10. Risks

| Risk | Mitigation |
|---|---|
| Red Hat ships Quadlet editing in Cockpit | Ship first; differentiate on GPU, Git, multi-host, agents, fleet governance, and UX. Cockpit's cadence is slow and framework-bound. |
| Commercialization alienates the self-hosted adoption wedge | Keep Community useful and non-hostile. Monetize fleet scale, governance, audit, backups, policy, optional agents, and support rather than basic survival features. |
| Optional agents violate the original trust model | Agents must be transport/execution helpers only. Quadlet files and systemd remain authoritative; no agent-owned workload database. |
| License enforcement damages trust | Enforcement must never stop workloads, delete files, or block direct systemd/Podman access. Restrict Enterprise fleet features only. |
| Quadlet spec churn across Podman versions | Validate via the host's own `podman-system-generator`, not a vendored parser. |
| Single-maintainer burnout (see quadletman) | MVP must be useful standalone even if development stops; the files-on-disk model guarantees no workload lock-in, while paid Enterprise funds sustainability. |

## 11. Decisions & open questions

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
- **Commercial model**: Community remains useful; Enterprise is free for the
  first three managed nodes and paid beyond that.
- **Paid boundary**: Enterprise monetizes fleet scale, governance, audit,
  backups, policy, optional agents, support, and advanced team controls — not
  basic Quadlet lifecycle, local accounts, or OIDC/SSO user count.
- **Agent posture**: agentless SSH remains core; optional agents are an
  Enterprise fleet capability.

**Open**
- Whether to absorb quadletman/Podman+ maintainers rather than compete —
  the niche is too small for three half-finished tools.
- Namespace grabs before announcement: github.com/rookery (or org), domain
  (getrookery.app?), Fedora COPR name.
- Exact license key format, offline grace behavior, and license server design.
- Whether Enterprise source remains in-tree with build tags, ships as a
  separate private module, or stays disabled until commercial demand is proven.

## 12. Rollout

1. **Weeks 1–4**: spike — D-Bus/systemd control of user sessions, unit-file
   editor with generator validation, single host, read-only GPU info.
2. **Weeks 5–8**: MVP feature-complete; dogfood on the N5 Air (migrate the 27
   Pi containers to Quadlets using only the tool).
3. **Alpha hardening**: keep docs aligned with the SQLite-backed admin
   metadata, verify `users.json` migration, document backup/restore for
   `ROOKERY_DATA_DIR` plus Quadlet dirs, prove install/upgrade/container
   deployments, and publish known limitations.
4. **Public alpha**: post to r/selfhosted + Podman Discourse; iterate on the
   importer (the migration story is the growth engine) and collect 4+ node
   fleet-management feedback.
5. **v1**: product-ready Community/Enterprise Free baseline: lifecycle,
   importer, Git, GPU panel, update checks, auth, secrets, and agentless
   multi-host. Submit to Fedora COPR and Flathub if packaging demand exists.
6. **Enterprise design spike**: specify licensing, audit storage, node
   inventory, RBAC boundaries, backup targets, policy checks, and optional
   agent protocol. Do not implement enforcement until outside demand validates
   the commercial line.
7. **v2**: Enterprise fleet release: optional agents, node groups, bulk
   operations, advanced RBAC, audit, backups, advanced GitOps, policy/drift
   detection, and paid scale beyond three nodes.
