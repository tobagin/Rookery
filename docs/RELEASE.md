# Release Checklist

Rookery is pre-alpha. Releases should be boring, reproducible, and clear about
what was tested on real hosts.

## Before Tagging

- Confirm the worktree is clean.
- Run `make check`.
- Run `scripts/smoke-host.sh` on at least one development host.
- On a homelab host, follow [DOGFOOD.md](DOGFOOD.md) through the read-only UI
  pass and the disposable-unit write-path pass.
- Test either bare-binary install with [packaging/rookery.service](../packaging/rookery.service)
  or container install with [packaging/rookery.container](../packaging/rookery.container).
- If upgrading an existing install, confirm `users.json` migration into
  `rookery.db` on a copied data directory or a non-critical host.
- Update [CHANGELOG.md](../CHANGELOG.md) with user-facing changes, upgrade
  notes, and known limitations.
- Confirm README install, backup, TLS, and known-limitations sections still
  match the build.

## Tagging

Use annotated tags:

```sh
git tag -a v0.1.0 -m "v0.1.0"
git push origin v0.1.0
```

Pushing a `v*` tag runs:

- `.github/workflows/release.yml` to build amd64/arm64 binaries and checksums.
- `.github/workflows/image.yml` to publish the multi-arch container image.

The GitHub release is created as a draft. Review the generated notes before
publishing.

## After Tagging

- Download both binaries from the draft release.
- Verify checksums:

```sh
sha256sum -c SHA256SUMS
```

- Run `./rookery-linux-amd64 -version` or the relevant architecture binary.
- Pull the tagged container image and check it starts:

```sh
podman pull ghcr.io/tobagin/rookery:v0.1.0
```

- Publish the GitHub release only after binaries, checksums, and image tags
  are present.
- For public alpha, include a short warning that Rookery should be placed
  behind TLS when exposed beyond localhost and that write-path testing should
  start with disposable units.

## Rollback

- Workloads are defined by Quadlet files, not the Rookery binary. Downgrading
  or stopping Rookery should not stop containers already managed by systemd.
- Back up `ROOKERY_DATA_DIR` before upgrading. Restore it to recover local
  accounts and UI-managed settings.
- If a release has a serious issue, unpublish only the GitHub release text if
  needed; leave immutable tags and container digests intact and cut a fixed
  patch release.
