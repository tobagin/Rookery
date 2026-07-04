# 🦭 Rookery

**Quadlet-native web UI for Podman** — manage systemd containers, pods, and
GPUs from your browser, with unit files on disk as the single source of truth.

> A rookery is where a pod of seals gathers.

Podman + Quadlets is the canonical way to run containers on Fedora/RHEL, but
every polished management UI speaks the Docker API and can't see them. Rookery
is a thin, honest layer over systemd and Podman: it reads and writes the
`.container`/`.pod`/`.network`/`.volume` files that already define your
system, validates them with the host's own Quadlet generator, and drives them
through `systemctl`. **No daemon, no database, no agents.** You can always
fall back to SSH + `systemctl` with zero loss.

See [docs/PRD.md](docs/PRD.md) for the full product rationale, competitive
landscape, and roadmap.

## Status: early spike (pre-alpha)

What works today:

- **Dashboard** — all Quadlet units grouped by state (failed / running /
  stopped), host metrics strip, Podman version and container counts,
  restart-loop (`↻N`) and exit-code surfacing.
- **Editor** — syntax-highlighted unit-file editing in the browser with a
  diff preview before every save; the confirmed content is validated with
  the host's `podman-system-generator --dryrun`, written atomically, and
  followed by `daemon-reload` (+ optional restart). On SELinux-enforcing
  hosts, unlabeled bind mounts get a `:Z`/`:z` hint before they bite.
- **Importer** — convert `podman run` commands and compose files into
  Quadlet units, or import an existing container's configuration via the
  Podman API; everything the converter has to guess about becomes an
  explicit warning on the draft.
- **Create / delete** units for every Quadlet kind (`.container`, `.pod`,
  `.network`, `.volume`, `.kube`, `.image`, `.build`), with starter templates.
- **Lifecycle** — start / stop / restart / enable / disable via systemd.
- **Live logs** — `journalctl` streamed over Server-Sent Events, follow mode.
- **Rootless-aware** — run rootless to manage your own
  `~/.config/containers/systemd/`; run rootful with `-users alice,bob` to
  additionally manage those users' sessions (via `systemctl --user --machine`).
- **Git history & rollback** — with `-git` (or when the unit directory is
  already a repository), every save/delete/rollback becomes a commit; the
  unit page lists history with per-revision diffs and one-click restore,
  which re-validates the old content before writing it. The repo is plain
  git in the unit directory — fully usable without Rookery.
- **GPU panel** — host GPU inventory on the dashboard (NVIDIA via
  `nvidia-smi`, AMD VRAM/busy via amdgpu sysfs, Intel presence), per-unit
  attachment badges (`AddDevice=nvidia.com/gpu=…`, `/dev/dri`, `--gpus`),
  and an editor helper that inserts CDI / VAAPI / ROCm device lines.
- **Admin login** — single-password auth (`ROOKERY_PASSWORD` or
  `-password-file`) with in-memory sessions; without a password Rookery is
  open and warns loudly unless bound to loopback.
- **Mobile-responsive UI** — passes the "restart it from the couch" test.
- **Single static binary**, zero Go dependencies outside the standard library.

Not yet: multi-host over SSH, image-update checks. See the
[roadmap](#roadmap).

> ⚠️ Set a password (`ROOKERY_PASSWORD` or `-password-file`) before exposing
> Rookery beyond `127.0.0.1`, and put TLS in front of it (reverse proxy) —
> the built-in server speaks plain HTTP.

## Quick start

```sh
make build          # or: go build ./cmd/rookery
./rookery           # http://127.0.0.1:7878
```

- **Rootless**: manages your own `~/.config/containers/systemd/`.
- **Rootful**: manages `/etc/containers/systemd/`; add `-users alice` to also
  manage alice's rootless units.

| Flag | Env | Default | Meaning |
|---|---|---|---|
| `-listen` | `ROOKERY_LISTEN` | `127.0.0.1:7878` | listen address |
| `-users` | `ROOKERY_USERS` | — | extra users to manage (rootful only) |
| `-password-file` | `ROOKERY_PASSWORD_FILE` | — | admin password file (or set `ROOKERY_PASSWORD`) |
| `-git` | `ROOKERY_GIT=1` | auto-detect | track unit dirs in git: commit on save, history, rollback |

Dogfooding: [packaging/rookery.container](packaging/rookery.container) runs
Rookery itself as a Quadlet.

## Architecture

```
browser ──HTTP/SSE──▶ rookery (one static Go binary)
                        ├─ unit files   ~/.config/containers/systemd/, /etc/containers/systemd/
                        ├─ validation   podman-system-generator --dryrun (the host's own)
                        ├─ lifecycle    systemctl [--user [--machine user@.host]]
                        ├─ logs         journalctl -o json (-f)
                        └─ host info    Podman native REST socket (read-only) + /proc
```

Design rules (from the PRD):

1. **Files on disk are authoritative.** Rookery never hides state in its own
   database — there is none.
2. **Validate with the host's generator**, never a vendored parser, so
   Rookery always agrees with the Podman version actually installed.
3. **Mutations go through systemd**, exactly as they would over SSH.
4. Degrade gracefully: if systemd or Podman is unreachable, files on disk are
   still listed and editable, with the error surfaced.

`internal/systemd` currently shells out to `systemctl` (which makes cross-user
session management trivial via `--machine user@.host`); it is a small
interface, so a native D-Bus client can replace it without touching handlers.

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
| `GET /api/gpus` | host GPU inventory |
| `GET /api/host` | metrics, Podman info, scopes |
| `POST /api/login` / `POST /api/logout` / `GET /api/auth` | session auth |

`{scope}` is `system` or a username.

## Development

```sh
make check   # gofmt + go vet + go test
make build   # static binary with version stamp
make run
```

Go ≥ 1.24. The web UI is dependency-free vanilla JS embedded via `go:embed` —
no Node toolchain required.

## Roadmap

- **MVP polish**: optional `podlet` integration for edge-case conversions
  (the built-in converter covers the common flags/keys and warns about the
  rest), pod-level composition view.
- **v1 (remaining)**: agentless multi-host over SSH, image-update checks
  (digest drift per unit, one-click pull + restart). Done: Git integration,
  GPU panel.
- **v2**: OIDC, read-only share links, alerting hooks, secrets integration.

## License

[Apache-2.0](LICENSE)
