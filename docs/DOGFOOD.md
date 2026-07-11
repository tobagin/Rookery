# Dogfood Checklist

This checklist is for validating Rookery on real homelab hosts without
surprising existing workloads. Start with read-only checks, then do write-path
checks only against a disposable test unit or during a maintenance window.

## 1. Preflight

- Back up `ROOKERY_DATA_DIR` (`/etc/rookery` by default for rootful installs).
- Back up the managed Quadlet directories:
  `/etc/containers/systemd/` and each managed user's
  `~/.config/containers/systemd/`.
- Confirm how Rookery is installed: bare binary service, container Quadlet, or
  temporary development binary.
- Confirm whether access is local-only, reverse-proxied with TLS, or exposed
  some other way.
- Run the non-destructive smoke script:

```sh
scripts/smoke-host.sh --url http://127.0.0.1:7665
```

Authenticated installs may return `401` or `403` for API endpoints; that is
acceptable for reachability checks.

## 2. Read-Only UI Pass

- Sign in and confirm the dashboard loads without browser console errors.
- Confirm system and rootless scopes match the hosts/users you expect Rookery
  to manage.
- Open a running unit, stopped unit, and failed unit if available.
- Confirm logs render and follow mode can be started and stopped.
- Confirm host metrics load.
- Confirm GPU panel behavior on hosts with and without GPUs.
- Confirm update checks report digest-pinned images as unable to drift.
- Confirm viewer accounts and share links cannot see secrets, users, or
  settings, and cannot run non-GET actions.

## 3. Import/Migration Pass

Use a disposable workload first, not a production service.

- Convert a representative `podman run` command and review warnings.
- Convert a representative compose file and review generated service ordering.
- Import one existing non-critical container and compare the generated Quadlet
  with `podman inspect`.
- Save the generated unit only after reviewing paths, ports, environment,
  secrets, SELinux labels, restart policy, and `WantedBy=`.
- Reboot or manually run `systemctl daemon-reload` plus the relevant start
  command to confirm the generated unit survives outside Rookery.

## 4. Write-Path Pass

Run this only against a disposable unit such as `rookery-smoke.container`.

- Create a new `.container` unit from the template.
- Validate without saving, then save without restart.
- Inspect the written file on disk.
- Start, stop, restart, enable, and disable the disposable unit from Rookery.
- Confirm the same lifecycle operations work from `systemctl`.
- Edit the unit, preview the diff, save with restart, and confirm logs update.
- Delete the disposable unit and confirm the file is removed after
  `daemon-reload`.

## 5. Recovery Pass

- If `-git` is enabled, confirm save/delete operations create commits.
- View per-unit history, inspect a revision, and roll back the disposable unit.
- Confirm manual git commands still work in the Quadlet directory.
- Restart Rookery and confirm existing login sessions survive (they persist
  in `rookery.db`) while workloads keep running.
- Restore `ROOKERY_DATA_DIR` from backup on a test host and confirm accounts
  and settings return.

## 6. Remote Host Pass

- Configure one remote with `ROOKERY_REMOTES=alias=user@host`.
- Confirm the remote dashboard scope loads.
- Confirm remote validation uses the remote host's generator.
- Confirm remote logs and lifecycle work against a disposable unit.
- Confirm remote update checks and pulls work only when the remote user's
  Podman permissions allow them.
- For agent-backed hosts (`ROOKERY_AGENTS=alias=url` + `ROOKERY_AGENT_TOKEN`):
  confirm every scope on the host appears (system + rootless users), units
  list with live state, lifecycle/logs/edit work, and the Fleet row shows the
  host's metrics and GPUs.
- Use the topbar node picker to select each node in turn and confirm the
  dashboard, containers, images, volumes, and networks show only that node's
  objects; confirm "Prune unused" with a node selected touches only that node.

## 7. Exit Criteria For Public Alpha

- A real migration can be completed without SSH except for install and backup.
- A disposable write-path unit passes create, edit, validate, lifecycle, logs,
  rollback, and delete.
- A fresh install and an upgrade from `users.json` to `rookery.db` are both
  documented and tested.
- The known limitations in the README match observed behavior.
- Any production-impacting issues found during dogfooding are either fixed or
  explicitly listed before announcement.
