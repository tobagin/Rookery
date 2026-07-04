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
- **Live logs** — `journalctl` streamed to the browser, follow mode.
- **Rootless multi-user** — rootless Rookery manages your own
  `~/.config/containers/systemd/`; rootful with `-users alice,bob` also
  manages those users' sessions via `systemctl --user --machine`.
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
  pull + restart. Digest-pinned images are correctly reported as unable to
  drift.
- **Mobile-responsive** — passes the "restart it from the couch" test.
- **One static binary** — amd64 + arm64, zero dependencies outside the Go
  standard library; the web UI is embedded vanilla JS, no Node toolchain.

## Quick start

```sh
git clone https://github.com/tobagin/rookery && cd rookery
make build          # or: go build ./cmd/rookery
./rookery           # → http://127.0.0.1:7878
```

Run it rootless to manage your own `~/.config/containers/systemd/`, or
rootful to manage `/etc/containers/systemd/` (add `-users alice` to also
manage alice's rootless units).

> ⚠️ Set a password (`ROOKERY_PASSWORD` or `-password-file`) before exposing
> Rookery beyond `127.0.0.1`, and put TLS in front of it (reverse proxy) —
> the built-in server speaks plain HTTP.

### Configuration

| Flag | Env | Default | Meaning |
|---|---|---|---|
| `-listen` | `ROOKERY_LISTEN` | `127.0.0.1:7878` | listen address |
| `-users` | `ROOKERY_USERS` | — | extra users to manage (rootful only) |
| `-password-file` | `ROOKERY_PASSWORD_FILE` | — | admin password file (or set `ROOKERY_PASSWORD`) |
| `-git` | `ROOKERY_GIT=1` | auto-detect | track unit dirs in git: commit on save, history, rollback |
| `-remotes` | `ROOKERY_REMOTES` | — | remote hosts over ssh, `alias=user@host,...` |

### Install as a service

[packaging/rookery.service](packaging/rookery.service) is a plain systemd
unit for the binary. Or dogfood it:
[packaging/rookery.container](packaging/rookery.container) runs Rookery
itself as a Quadlet.

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
| `GET /api/gpus` | host GPU inventory |
| `GET /api/host` | metrics, Podman info, scopes |
| `POST /api/login` / `POST /api/logout` / `GET /api/auth` | session auth |

`{scope}` is `system`, a username, or a remote-host alias from `-remotes`.

**Remote-scope limits (v1):** git history, SELinux hints, the GPU panel, and
image updates apply to the local host only — the remote host's own Rookery
(or SSH) covers those. Everything else — list, edit, validate, lifecycle,
logs — works identically over ssh.

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

- **Polish**: optional `podlet` integration for edge-case conversions,
  pod-level composition view, remote-host git/updates/GPU parity, automatic
  rootless-user discovery.
- **v2**: OIDC / multi-admin auth, read-only share links, alerting hooks
  (ntfy/Telegram), secrets integration (`podman secret` + systemd
  credentials).

## License

[Apache-2.0](LICENSE)
