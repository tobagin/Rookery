# 🦭 Rookery

**Quadlet-native web UI for Podman** — manage systemd containers, pods, and
GPUs from your browser, with unit files on disk as the single source of truth.

[![CI](https://github.com/tobagin/rookery/actions/workflows/ci.yml/badge.svg)](https://github.com/tobagin/rookery/actions/workflows/ci.yml)
[![License](https://img.shields.io/badge/license-Apache--2.0-blue.svg)](LICENSE)
![Go](https://img.shields.io/badge/go-%E2%89%A51.24-00ADD8?logo=go)
![Dependencies](https://img.shields.io/badge/dependencies-0-brightgreen)

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
**No daemon, no database, no agents.** Stop using Rookery tomorrow and you
lose nothing — the files on disk *are* the state.

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
  host's generator), lifecycle, and logs over plain ssh. Nothing to install
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
  (PBKDF2-hashed in a plain JSON file, no database); admins can add more
  admins or **viewer** accounts that get a read-only dashboard. Sessions are
  HttpOnly cookies with a sliding idle timeout (`-session-ttl`, default 24h).
- **Read-only share links** — one click mints a 7-day link for a dashboard
  view without a login: enforced GET-only on the server, no secrets, no
  actions. Changing any password revokes all links.
- **Secrets** — list `podman secret`s with the units that reference them,
  create and delete from the browser (delete refuses while referenced);
  the editor inserts `Secret=` lines from a picker. Values are write-only.
- **Mobile-responsive** — passes the "restart it from the couch" test.
- **One static binary** — amd64 + arm64, zero dependencies outside the Go
  standard library; the web UI is embedded vanilla JS, no Node toolchain.

## Quick start

```sh
git clone https://github.com/tobagin/rookery && cd rookery
make build          # or: go build ./cmd/rookery
./rookery           # → http://127.0.0.1:7665
```

Run it rootless to manage your own `~/.config/containers/systemd/`, or
rootful to manage `/etc/containers/systemd/` (add `-users alice` to also
manage alice's rootless units).

> The first visit runs a setup wizard that creates the admin account —
> complete it **before** exposing Rookery beyond `127.0.0.1`, and put TLS in
> front of it (reverse proxy) — the built-in server speaks plain HTTP.
> (`ROOKERY_PASSWORD` / `-password-file` still work as a wizard-free legacy
> single-admin mode.)

### Configuration

| Flag | Env | Default | Meaning |
|---|---|---|---|
| `-listen` | `ROOKERY_LISTEN` | `127.0.0.1:7665` | listen address ("ROOK" on a phone keypad) |
| `-users` | `ROOKERY_USERS` | auto-discover | rootless users to manage (rootful only); `none` disables |
| `-password-file` | `ROOKERY_PASSWORD_FILE` | — | legacy single-admin password (or `ROOKERY_PASSWORD`); the wizard is nicer |
| `-data-dir` | `ROOKERY_DATA_DIR` | `/etc/rookery` (rootful) | where `users.json` lives |
| `-session-ttl` | `ROOKERY_SESSION_TTL` | `24h` | idle timeout for login sessions (sliding) |
| `-git` | `ROOKERY_GIT=1` | auto-detect | track unit dirs in git: commit on save, history, rollback |
| `-remotes` | `ROOKERY_REMOTES` | — | remote hosts over ssh, `alias=user@host,...` |
| `-alerts` | `ROOKERY_ALERTS` | — | failure alerts: `ntfy://host/topic`, `telegram://TOKEN@CHAT`, webhook URL |

### Install as a service

[packaging/rookery.service](packaging/rookery.service) is a plain systemd
unit for the binary — the simplest install.

Or run the image (amd64 + arm64, published from CI):
[packaging/rookery.container](packaging/rookery.container) runs
`ghcr.io/tobagin/rookery` as a Quadlet, dogfooding Rookery on itself. The
container needs the host mounts listed there (unit dirs, `/run/systemd`,
journal, Podman socket, and the host's quadlet generator for validation).

## Architecture

```
browser ──HTTP/SSE──▶ rookery (one static Go binary)
                        ├─ unit files   ~/.config/containers/systemd/, /etc/containers/systemd/
                        ├─ validation   podman-system-generator --dryrun (the host's own)
                        ├─ lifecycle    systemctl [--user [--machine user@.host]]
                        ├─ logs         journalctl -o json (-f)
                        ├─ host info    Podman native REST socket (read-only) + /proc
                        ├─ updates      registry v2 digest HEAD + podman pull
                        └─ remote hosts ssh user@host -- <the same commands over there>
```

Design rules (from the [PRD](docs/PRD.md)):

1. **Files on disk are authoritative.** Rookery never hides state in its own
   database — there is none.
2. **Validate with the host's generator**, never a vendored parser, so
   Rookery always agrees with the Podman version actually installed.
3. **Mutations go through systemd**, exactly as they would over SSH.
4. **Degrade gracefully**: if systemd or Podman is unreachable, files on disk
   are still listed and editable, with the error surfaced.

## API

| Method & path | Purpose |
|---|---|
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
| `GET/POST /api/secrets`, `DELETE /api/secrets/{name}` | podman secrets (write-only values) |
| `GET /api/images/stale` / `POST /api/images/prune` | dangling-image count/size, prune |
| `POST /api/share` | mint a 7-day read-only share token |
| `GET/POST /api/setup` | first-run wizard: create the initial admin (one-shot) |
| `GET/POST /api/users`, `DELETE /api/users/{name}`, `POST /api/users/{name}/password` | account management (admin) |
| `POST /api/login` / `POST /api/logout` / `GET /api/auth` | session auth (sliding idle timeout) |

`{scope}` is `system`, a username, or a remote-host alias from `-remotes`.

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

Go ≥ 1.24, no other toolchain. Status: **pre-alpha** — the PRD's v1 scope
(lifecycle, importer, git, GPU, multi-host, update checks) is implemented and
under active dogfooding.

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
what remains of the auth story is external identity.

- **v2**: OIDC / external SSO, systemd credentials alongside podman
  secrets, pod-level log interleaving.

## License

[Apache-2.0](LICENSE)
