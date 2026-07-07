# Changelog

Rookery is currently pre-alpha. This file tracks human-written release notes;
GitHub release notes may include the full commit list.

## Unreleased

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
