# <img src="assets/logo.svg" alt="" width="28" align="top"> Rookery

**Quadlet-native web UI for Podman** — manage systemd containers, pods, and
GPUs from your browser, with unit files on disk as the single source of truth.

[![CI](https://github.com/rookerylabs/rookery/actions/workflows/ci.yml/badge.svg)](https://github.com/rookerylabs/rookery/actions/workflows/ci.yml)
[![License](https://img.shields.io/badge/license-Apache--2.0-blue.svg)](LICENSE)
![Go](https://img.shields.io/badge/go-%E2%89%A51.25-00ADD8?logo=go)
![Runtime deps](https://img.shields.io/badge/runtime%20deps-systemd%20%7C%20podman-lightgrey)

> A rookery is where a pod of seals gathers.

## Why

Quadlets — containers defined by `.container`/`.pod`/`.network` systemd unit
files — are the canonical way to run Podman on Fedora/RHEL. But the polished
management UIs (Portainer, Arcane, Dockhand) speak the Docker API and can't
see them, and cockpit-podman can start Quadlets but not create or edit them.
If you run containers "the right way", your UI is SSH and a text editor.

Rookery is a thin, honest layer over systemd and Podman. It reads and writes
the unit files that already define your system, validates them with the
host's own Quadlet generator, and drives them through `systemctl`.
**No daemon, no agents, and no hidden workload state.** Stop using Rookery
tomorrow and you lose nothing about how your containers run — the Quadlet
files on disk *are* the workload state. Rookery keeps only local admin
metadata, such as accounts and UI-managed settings, in `rookery.db`.

|  | Quadlet create/edit | Rootless multi-user | GPU | Multi-host | Server web UI |
|---|:---:|:---:|:---:|:---:|:---:|
| cockpit-podman | ✗ (run only) | partial | ✗ | ✗ | ✓ |
| Portainer / Arcane | ✗ | ✗ | ✗ | ✓ (agents) | ✓ |
| Podman Desktop | ✓ | n/a | partial | ✗ | ✗ (desktop app) |
| **Rookery** | ✓ | ✓ | ✓ | ✓ (agentless) | ✓ |

## Features

- **Dashboard** — every Quadlet unit grouped by state (failed / running /
  stopped), host metrics, restart-loop and exit-code surfacing.
- **Browser editor** — syntax highlighting, diff preview before every save,
  validation with the host's own `podman-system-generator --dryrun`, then
  atomic write → `daemon-reload` → optional restart. SELinux hints for
  unlabeled bind mounts on enforcing hosts.
- **Importer** — turn `podman run` commands, compose files, or already-running
  containers into Quadlet units; anything the converter has to guess about
  becomes an explicit warning on the draft.
- **Full lifecycle** — create, start, stop, restart, enable, disable, delete;
  every Quadlet kind (`.container`, `.pod`, `.network`, `.volume`, `.kube`,
  `.image`, `.build`) with starter templates.
- **Pod composition** — pod cards roll up member state (per-member dots,
  up/failed counts), member containers link to their pod, and the pod page
  lists every unit that declares `Pod=`.
- **Live logs** — `journalctl` streamed to the browser, follow mode.
- **Rootless multi-user** — rootless Rookery manages your own
  `~/.config/containers/systemd/`; rootful auto-discovers every user with a
  Quadlet tree (or take control with `-users alice,bob` / `-users none`) and
  manages their sessions via `systemctl --user --machine`.
- **Git history & rollback** — with `-git`, every save/delete/rollback is a
  commit; per-revision diffs and one-click restore (re-validated before
  writing). It's plain git in the unit directory — fully usable without
  Rookery.
- **GPU panel** — NVIDIA (`nvidia-smi`), AMD (amdgpu sysfs) and Intel
  inventory, per-unit attachment badges, and an editor helper that inserts
  CDI / VAAPI / ROCm device lines. No other web UI does this.
- **Agentless multi-host** — `-remotes nas=root@nas.local` adds another box's
  Quadlet tree to the same dashboard: list, edit, validate (with the *remote*
  host's generator), lifecycle, and logs over plain ssh. Use explicit grouped
  aliases such as `pi.root=pi-root,pi.user=pi-user` to show rootful and
  rootless scopes for one host under the same fleet node. Nothing to install
  on the target beyond sshd and Podman.
- **Image-update checks** — compare every unit's tag against the digest its
  registry serves (docker.io, ghcr.io, quay.io, …), flag drift, one-click
  pull + restart — on remote hosts too (podman over ssh). Digest-pinned
  images are correctly reported as unable to drift, and the dangling images
  updates leave behind get a one-click prune with reclaimable size shown.
- **Failure alerts** — `-alerts ntfy://ntfy.sh/topic` (or
  `telegram://BOT_TOKEN@CHAT_ID`, or any JSON webhook) notifies when a unit
  enters or recovers from `failed`, with exit code and restart count.
- **Accounts & roles** — a first-run wizard creates the admin account
  (PBKDF2-hashed in local SQLite metadata); admins can add more
  admins or **viewer** accounts that get a read-only dashboard. Sessions are
  HttpOnly cookies with a sliding idle timeout (`-session-ttl`, default 24h).
- **Read-only share links** — one click mints a 7-day link for a dashboard
  view without a login: enforced GET-only on the server, no secrets, no
  actions. Changing any password revokes all links.
- **Secrets** — list `podman secret`s with the units that reference them,
  create and delete from the browser (delete refuses while referenced);
  the editor inserts `Secret=` lines from a picker. Values are write-only.
- **Mobile-responsive operations UI** — a dense React console with desktop
  sidebar navigation, mobile bottom tabs, and a "restart it from the couch"
  triage path.
- **One static binary** — amd64 + arm64; the web UI is built with Vite and
  embedded into the binary. At runtime, Rookery shells out to the host's
  systemd, Podman, journal, git, and ssh tools as features require.

## Quick start

```sh
git clone https://github.com/rookerylabs/rookery && cd rookery
make build          # builds the Vite UI, then go build ./cmd/rookery
./rookery           # → http://127.0.0.1:7665
```

Run it rootless to manage your own `~/.config/containers/systemd/`, or
rootful to manage `/etc/containers/systemd/` (add `-users alice` to also
manage alice's rootless units).

> On a fresh install, Rookery creates the initial `admin` account at startup.
> If no password is provided, it prints a temporary password to the process
> output/container logs. Sign in with `admin`, then complete the first-login
> setup screen to set the admin email and replace the temporary password.
> (`ROOKERY_PASSWORD` / `-password-file` can provide the initial password; the
> first login will still ask for the admin email.) Put TLS in front of Rookery
> before exposing it beyond `127.0.0.1` — the built-in server speaks plain HTTP.

### Configuration

| Flag | Env | Default | Meaning |
|---|---|---|---|
| `-listen` | `ROOKERY_LISTEN` | `127.0.0.1:7665` | listen address ("ROOK" on a phone keypad) |
| `-users` | `ROOKERY_USERS` | auto-discover | rootless users to manage (rootful only); `none` disables |
| `-password-file` | `ROOKERY_PASSWORD_FILE` | — | legacy single-admin password (or `ROOKERY_PASSWORD`); the wizard is nicer |
| `-disable-password-login` | `ROOKERY_DISABLE_PASSWORD_LOGIN` | `false` | disable local username/password login; requires OIDC |
| `-data-dir` | `ROOKERY_DATA_DIR` | `/etc/rookery` (rootful) | where `rookery.db` lives |
| `-session-ttl` | `ROOKERY_SESSION_TTL` | `24h` | idle timeout for login sessions (sliding) |
| `-share-ttl` | `ROOKERY_SHARE_TTL` | `168h` | lifetime of read-only share links |
| `-audit-retention` | `ROOKERY_AUDIT_RETENTION` | `0` | prune audit events older than this on startup; `0` keeps everything |
| `-git` | `ROOKERY_GIT=1` | auto-detect | track unit dirs in git: commit on save, history, rollback |
| `-remotes` | `ROOKERY_REMOTES` | — | remote hosts over ssh, `alias=user@host,...`; `node.root=target,node.user=target` groups rootful/rootless targets under one fleet node |
| `-alerts` | `ROOKERY_ALERTS` | — | failure alerts: `ntfy://host/topic`, `telegram://TOKEN@CHAT`, webhook URL |
| `-alert-interval` | `ROOKERY_ALERT_INTERVAL` | `30s` | failure-alert polling interval |
| `-alert-cooldown` | `ROOKERY_ALERT_COOLDOWN` | `0` | minimum time between repeated failure alerts for the same unit |
| `-oidc-issuer` | `ROOKERY_OIDC_ISSUER` | — | OIDC issuer URL for SSO |
| `-oidc-client-id` | `ROOKERY_OIDC_CLIENT_ID` | — | OIDC client ID |
| `-oidc-client-secret` | `ROOKERY_OIDC_CLIENT_SECRET` | — | OIDC client secret |
| `-oidc-redirect-url` | `ROOKERY_OIDC_REDIRECT_URL` | derived | public callback URL; usually `https://host/api/oidc/callback` |
| `-oidc-name` | `ROOKERY_OIDC_NAME` | `SSO` | label on the login button |
| `-oidc-admins` | `ROOKERY_OIDC_ADMINS` | — | comma-separated OIDC `sub`, `email`, or `preferred_username` values that get admin |
| `-oidc-admin-groups` | `ROOKERY_OIDC_ADMIN_GROUPS` | — | comma-separated OIDC `groups` values that get admin |
| `-oidc-default-role` | `ROOKERY_OIDC_DEFAULT_ROLE` | `viewer` | role for other OIDC users: `viewer` or `admin` |

### OIDC / SSO

Register Rookery as an OIDC confidential web application with callback:

```text
https://your-rookery.example/api/oidc/callback
```

Then configure it with flags or environment variables:

```sh
ROOKERY_OIDC_ISSUER=https://idp.example/application/o/rookery/
ROOKERY_OIDC_CLIENT_ID=rookery
ROOKERY_OIDC_CLIENT_SECRET=...
ROOKERY_OIDC_REDIRECT_URL=https://your-rookery.example/api/oidc/callback
ROOKERY_OIDC_ADMIN_GROUPS=rookery-admins
```

OIDC can run alongside local accounts and the legacy single-password mode.
When OIDC is the only configured credential source, the first-run local
account wizard is disabled and `/api/*` is protected by SSO. OIDC users are
viewers by default; grant admin either with `ROOKERY_OIDC_ADMINS` matching
`sub`, `email`, or `preferred_username`, or with
`ROOKERY_OIDC_ADMIN_GROUPS` matching the token's `groups` claim. Set
`ROOKERY_OIDC_DEFAULT_ROLE=admin` only if the identity provider already
limits access to trusted admins.

For SSO-only deployments, set:

```sh
ROOKERY_DISABLE_PASSWORD_LOGIN=true
```

That hides the username/password form and rejects `/api/login`; Rookery will
refuse to start in this mode unless OIDC is configured.

### FAQ

**Can I exec into containers or browse volumes?** No. Rookery deliberately
keeps workload mutations flowing through Quadlet files and systemd. It edits,
validates, starts, stops, restarts, and reads journal logs; it does not provide
an interactive container shell or volume file manager. Use `podman exec` or SSH
on the host when you need break-glass inspection.

### Install as a service

[packaging/rookery.service](packaging/rookery.service) is a plain systemd
unit for the binary — the simplest install.

Or run the image (amd64 + arm64, published from CI):
[packaging/rookery.container](packaging/rookery.container) runs
`ghcr.io/rookerylabs/rookery` as a Quadlet, dogfooding Rookery on itself. The
container needs the host mounts listed there (unit dirs, `/run/systemd`,
journal, Podman socket, and the host's quadlet generator for validation).

## Alpha readiness

Rookery is pre-alpha. The v1-shaped feature set is present, but the next
milestone is proving install, migration, upgrade, and recovery paths on real
hosts before a public alpha announcement. Use [docs/DOGFOOD.md](docs/DOGFOOD.md)
for the homelab validation checklist and [docs/RELEASE.md](docs/RELEASE.md)
for the release process.

### Install checklist

- Build with `make build`, or use the published image with
  [packaging/rookery.container](packaging/rookery.container).
- Run rootless for a single user's Quadlets, or rootful with
  [packaging/rookery.service](packaging/rookery.service) to manage
  `/etc/containers/systemd/` and selected users' rootless trees.
- Keep Rookery bound to `127.0.0.1` unless it is behind a reverse proxy with
  TLS. The built-in HTTP server does not terminate TLS.
- Persist `ROOKERY_DATA_DIR` (`/etc/rookery` by default when rootful). It
  contains `rookery.db`, which stores local accounts, password hashes,
  settings, and other durable admin metadata.
- If using the container deployment, keep the host mounts in
  [packaging/rookery.container](packaging/rookery.container) aligned with the
  host: unit directories, `/run/systemd`, journal, Podman socket, data dir,
  and the host's Quadlet generator when available.

### Upgrade notes

- Older builds used `users.json` for local accounts. Current builds migrate
  that file into sibling `rookery.db` when the database has no users.
- Back up `ROOKERY_DATA_DIR` before upgrading. The workload definitions
  themselves remain the Quadlet files under `/etc/containers/systemd/` and
  each managed user's `~/.config/containers/systemd/`.
- Environment variables and command-line flags override UI-saved settings.
  UI setting changes may require restarting Rookery to affect process-level
  configuration such as listen address, OIDC, remotes, or alert sinks.

### Backup and recovery

- Back up `ROOKERY_DATA_DIR` for accounts, password hashes, and UI-managed
  settings. Login sessions are in-memory, and share links are stateless tokens
  revoked by changing account credential material.
- Back up the Quadlet directories separately. If `-git` is enabled, those
  directories are plain git repositories; Rookery's history is still usable
  with normal git commands.
- Losing Rookery metadata does not delete or stop workloads. You can still
  use `systemctl`, `journalctl`, Podman, and the Quadlet files directly.

### Known alpha limits

- Rookery is intended to sit behind your own TLS/reverse-proxy layer when
  exposed beyond localhost.
- Remote hosts are SSH-only and agentless; the target still needs sshd,
  Podman, systemd, and compatible permissions.
- SELinux bind-mount hints and podman secret management are local-host only.
- Containerized Rookery depends on host namespace and socket mounts for full
  lifecycle control; missing mounts degrade specific features rather than
  changing the underlying Quadlet files.
- Importers cover common `podman run`, compose, and existing-container cases
  and emit warnings for guessed or unsupported fields. Review generated units
  before saving them.

## Architecture

```
browser ──HTTP/SSE──▶ rookery (one static Go binary)
                        ├─ unit files   ~/.config/containers/systemd/, /etc/containers/systemd/
                        ├─ metadata     rookery.db (accounts + Rookery settings)
                        ├─ validation   podman-system-generator --dryrun (the host's own)
                        ├─ lifecycle    systemctl [--user [--machine user@.host]]
                        ├─ logs         journalctl -o json (-f)
                        ├─ host info    Podman native REST socket (read-only) + /proc
                        ├─ updates      registry v2 digest HEAD + podman pull
                        └─ remote hosts ssh user@host -- <the same commands over there>
```

Design rules (from the [PRD](docs/PRD.md)):

1. **Files on disk are authoritative.** Rookery never hides state in its own
   database. SQLite stores Rookery's local admin metadata, not the workload
   definitions or systemd state.
2. **Validate with the host's generator**, never a vendored parser, so
   Rookery always agrees with the Podman version actually installed.
3. **Mutations go through systemd**, exactly as they would over SSH.
4. **Degrade gracefully**: if systemd or Podman is unreachable, files on disk
   are still listed and editable, with the error surfaced.

## API

| Method & path | Purpose |
|---|---|
| `GET /api/audit` | recent admin mutation audit events |
| `GET /api/backup` | tar.gz export of Rookery metadata and managed Quadlet files |
| `GET /api/units` | all units with live state |
| `GET /api/units/{scope}/{name}` | unit + file content |
| `PUT /api/units/{scope}/{name}` | validate → write → daemon-reload (`{"content", "restart"}`) |
| `DELETE /api/units/{scope}/{name}` | stop, remove file, daemon-reload |
| `POST /api/units/{scope}/{name}/action` | `{"action": "start\|stop\|restart\|enable\|disable"}` |
| `GET /api/units/{scope}/{name}/logs?follow=1` | journal stream (SSE) |
| `POST /api/validate` | dry-run a unit body without saving |
| `POST /api/convert` | `{"kind": "run\|compose\|container", "input": ...}` → draft units |
| `GET /api/import/containers` | existing containers eligible for import |
| `GET /api/units/{scope}/{name}/history` | git commits for the unit |
| `GET /api/units/{scope}/{name}/history/{commit}` | content at a commit |
| `POST /api/units/{scope}/{name}/rollback` | `{"commit": ...}` — validate + restore |
| `GET /api/updates` | digest drift for every container unit's image |
| `POST /api/units/{scope}/{name}/update` | pull new image + restart |
| `GET /api/gpus` | GPU inventory, local + every remote host |
| `GET /api/host` | metrics, Podman info, scopes |
| `GET /api/license` | edition, planned 3-node Enterprise Free allowance, managed-node count, remaining/over-limit nodes, unlimited user/SSO allowances |
| `GET /api/nodes` | managed-node inventory grouped from local and remote scopes |
| `GET /api/groups` | label-derived node groups for fleet organization |
| `PATCH /api/nodes/{id}/labels` | save Rookery-owned node labels for fleet organization |
| `GET /api/policies` | read-only fleet policy findings from Quadlet files |
| `POST /api/policies/waivers`, `DELETE /api/policies/waivers/{key}` | waive or unwaive policy findings with Rookery metadata |
| `GET/POST /api/secrets`, `DELETE /api/secrets/{name}` | podman secrets (write-only values) |
| `GET /api/images/stale` / `POST /api/images/prune` | dangling-image count/size, prune |
| `POST /api/share` | mint a 7-day read-only share token |
| `GET/POST /api/setup` | first-run wizard: create the initial admin (one-shot) |
| `GET/POST /api/users`, `DELETE /api/users/{name}`, `POST /api/users/{name}/password` | account management (admin) |
| `GET /api/oidc/login` / `GET /api/oidc/callback` | OIDC authorization-code login |
| `POST /api/login` / `POST /api/logout` / `GET /api/auth` | session auth (sliding idle timeout) |

`{scope}` is `system`, a username, or a remote-host alias from `-remotes`
(for grouped remotes, the full `node.scope` alias).

**Remote-scope limits:** SELinux hints and podman secrets are local-host
only. Everything else — list, edit, validate (remote generator), lifecycle,
logs, git history/rollback (when the remote dir is already a repo; Rookery
never git-inits another host), GPU panel, update checks and pulls — works
identically over ssh.

## Development

```sh
make check   # gofmt + go vet + go test
make build   # static binary with version stamp
make cross   # linux amd64 + arm64
```

Go ≥ 1.25, Node.js 22 for the embedded web build. Status: **pre-alpha** —
the PRD's v1 scope (lifecycle, importer, git, GPU, multi-host, update checks)
is implemented and under active dogfooding.

## Roadmap

Shipped through v1.x: full Quadlet lifecycle, importer, git history,
GPU panel, agentless multi-host with remote git/updates/GPU parity,
rootless auto-discovery, pod composition view, image-update checks with
stale-image pruning, failure alerts (ntfy/Telegram/webhook), read-only
share links, and podman-secrets management.

Deliberately not built: `podlet` integration (the native converter covers
the common cases and warns about the rest; a binary dependency for edge
cases isn't worth it — open an issue if you hit a real gap).

Multi-admin accounts with a viewer role shipped with the first-run wizard;
OIDC / external SSO is available for deployments that already have an
identity provider. Rookery's planned Enterprise Free model does not cap local
users or SSO identities; the commercial boundary is managed-node scale and
fleet governance.

- **v2**: systemd credentials alongside podman secrets, pod-level log
  interleaving.

## License

[Apache-2.0](LICENSE)
