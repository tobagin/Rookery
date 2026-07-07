# Changelog

Rookery is currently pre-alpha. This file tracks human-written release notes;
GitHub release notes may include the full commit list.

## Unreleased

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
