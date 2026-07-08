# Changelog

Rookery is currently pre-alpha. This file tracks human-written release notes;
GitHub release notes may include the full commit list.

## Unreleased

## v0.1.0-alpha - 2026-07-08

### Added

- Added dark/light/auto theme switching, grouped sidebar navigation, and a
  more professional operations-console visual polish pass.
- Polished the Import page with source-type cards, clearer target-scope
  selection, and one-click sample input for command and compose imports.
- Added status-count filters and a persisted compact-density option to the
  Units list for faster operational scanning.
- Added severity and waiver-status filters to the Policy page so critical
  active findings stay easy to isolate as fleets grow.
- Clarified the Enterprise Free model in the license API, About panel, and
  docs: local users and SSO identities are unlimited; node scale is the
  commercial boundary.
- Added remaining-node and over-limit counts to the license API and Fleet /
  About displays for clearer Enterprise Free allowance visibility.
- Improved Settings → Users with account summary tiles, role/search filters,
  and setup-required badges for local accounts.
- Improved Settings → Audit with event summary tiles, actor/action/search
  filters, refresh control, and wrapped JSON details.
- Improved Updates with summary tiles and scope/status/search filters for
  checked image drift rows.
- Added an initial read-only edition/license status API and Settings/About
  display for the planned 3-node Enterprise Free model. Enforcement remains
  disabled in alpha.
- Added a read-only Fleet page and `/api/nodes` inventory endpoint that groups
  local and remote scopes into managed nodes.
- Added persistent Rookery-owned node labels in `rookery.db`, editable from
  the Fleet page by admins.
- Added label-derived node groups on the Fleet page and `GET /api/groups`.
- Added a read-only Policy page and `/api/policies` endpoint for risky
  Quadlet patterns such as `latest` tags, privileged containers, and
  unlabeled bind mounts.
- Added policy waivers with reasons, stored in `rookery.db`, plus waive /
  unwaive controls on the Policy page.
- Added a SQLite-backed audit log for admin mutations with a Settings →
  Audit view and `GET /api/audit`.
- Extended audit logging to setup, login, logout, onboarding, OIDC login, and
  share-link creation events.
- Added a Settings → Backup export and `GET /api/backup` tar.gz download for
  Rookery metadata and managed Quadlet files.
- Documented the alpha-readiness path and clarified that Quadlet files remain
  the workload source of truth while `rookery.db` stores local admin metadata.
- Added a non-destructive host smoke script for homelab validation.
- Added release checklist and tag-driven binary release workflow.

### Fixed

- Fixed Quadlet enable/disable actions to mutate the source unit's
  `[Install]` section instead of calling `systemctl enable` on generated
  services.
- Fixed agentless remote commands to run under `sh -c` so targets with
  non-POSIX login shells still work.
- Fixed remote rootless log retrieval by querying journal fields for the
  user unit instead of relying on `journalctl --user -u`.

### Upgrade Notes

- Existing Quadlet files remain the source of truth. Enabling or disabling a
  unit from Rookery now edits the unit file and runs `daemon-reload`, which is
  the persistent Quadlet model.
- Container installs that manage remote hosts need SSH credentials available
  inside the container.

### Known Limitations

- Rookery is an alpha and should sit behind your own TLS/reverse-proxy layer
  when exposed beyond localhost.
- Remote hosts are SSH-only and agentless; the target still needs sshd,
  Podman, systemd, and compatible permissions.
- SELinux bind-mount hints and podman secret management are local-host only.
- Containerized Rookery depends on host namespace and socket mounts for full
  lifecycle control; missing mounts degrade specific features rather than
  changing the underlying Quadlet files.
- Importers cover common `podman run`, compose, and existing-container cases
  and emit warnings for guessed or unsupported fields. Review generated units
  before saving them.

## Release Note Template

Use this shape when cutting a tag:

```md
## vX.Y.Z - YYYY-MM-DD

### Added

### Changed

### Fixed

### Upgrade Notes

### Known Limitations
```
