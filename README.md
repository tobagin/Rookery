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
  stopped), host metrics strip, Podman version and container counts.
- **Editor** — view and edit unit files in the browser; every save is
  validated with the host's `podman-system-generator --dryrun` first, then
  written atomically and followed by `daemon-reload` (+ optional restart).
- **Create / delete** units for every Quadlet kind (`.container`, `.pod`,
  `.network`, `.volume`, `.kube`, `.image`, `.build`), with starter templates.
- **Lifecycle** — start / stop / restart / enable / disable via systemd.
- **Live logs** — `journalctl` streamed over Server-Sent Events, follow mode.
- **Rootless-aware** — run rootless to manage your own
  `~/.config/containers/systemd/`; run rootful with `-users alice,bob` to
  additionally manage those users' sessions (via `systemctl --user --machine`).
- **Mobile-responsive UI** — passes the "restart it from the couch" test.
- **Single static binary**, zero Go dependencies outside the standard library.

Not yet: import/convert (`podlet` wrapper), Git history, GPU panel,
multi-host, auth. See the [roadmap](#roadmap).

> ⚠️ There is **no authentication yet**. Rookery binds to `127.0.0.1` by
> default; only expose it via a reverse proxy that adds auth, or an SSH
> tunnel.

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
| `GET /api/host` | metrics, Podman info, scopes |

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

- **MVP**: importer/generator (`podman run` + compose → Quadlet, wraps
  `podlet`), import of running containers, restart-loop surfacing, syntax
  highlighting, admin login.
- **v1**: agentless multi-host over SSH, Git integration (commit on save,
  history, rollback), GPU panel (attachments, utilization, CDI helper),
  image-update checks.
- **v2**: OIDC, read-only share links, alerting hooks, secrets integration.

## License

[Apache-2.0](LICENSE)
