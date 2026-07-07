#!/usr/bin/env bash
set -euo pipefail

usage() {
  cat <<'EOF'
Usage: scripts/smoke-host.sh [--url http://127.0.0.1:7665] [--scope system] [--mutating]

Runs homelab-safe smoke checks for a Rookery host. By default this script only
inspects the local machine and optionally the Rookery HTTP API. It does not
start, stop, restart, edit, delete, pull, prune, or import workloads.

Options:
  --url URL       Probe a running Rookery instance. Authenticated installs may
                  return 401; that still proves the server is reachable.
  --scope SCOPE   Scope name to look for in /api/units output when --url is set.
                  Default: system.
  --mutating      Reserved for future destructive lifecycle checks. Currently
                  exits with an explanation instead of touching workloads.
  -h, --help      Show this help.
EOF
}

url=""
scope="system"
mutating=0

while [ "$#" -gt 0 ]; do
  case "$1" in
    --url)
      url="${2:-}"
      shift 2
      ;;
    --scope)
      scope="${2:-}"
      shift 2
      ;;
    --mutating)
      mutating=1
      shift
      ;;
    -h|--help)
      usage
      exit 0
      ;;
    *)
      echo "unknown argument: $1" >&2
      usage >&2
      exit 2
      ;;
  esac
done

if [ "$mutating" -eq 1 ]; then
  cat >&2 <<'EOF'
--mutating is intentionally not implemented yet.

Rookery may already be managing real homelab workloads on this host, so this
script currently refuses to perform lifecycle or write-path checks. Use the
dogfood checklist in docs/DOGFOOD.md for explicit manual mutation tests.
EOF
  exit 2
fi

pass() { printf 'ok - %s\n' "$1"; }
warn() { printf 'warn - %s\n' "$1"; }
fail() { printf 'fail - %s\n' "$1" >&2; exit 1; }

tmpdir="$(mktemp -d)"
trap 'rm -rf "$tmpdir"' EXIT

have() {
  command -v "$1" >/dev/null 2>&1
}

check_command() {
  if have "$1"; then
    pass "$1 is installed"
  else
    warn "$1 is not installed"
  fi
}

check_command systemctl
check_command journalctl
check_command podman
check_command git
check_command ssh

if have systemctl; then
  if systemctl is-system-running >/dev/null 2>&1; then
    pass "systemd reports system running"
  else
    state="$(systemctl is-system-running 2>/dev/null || true)"
    warn "systemd state is ${state:-unknown}"
  fi
fi

system_dir="/etc/containers/systemd"
user_dir="${XDG_CONFIG_HOME:-$HOME/.config}/containers/systemd"

if [ -d "$system_dir" ]; then
  count="$(find "$system_dir" -maxdepth 1 -type f \( -name '*.container' -o -name '*.pod' -o -name '*.network' -o -name '*.volume' -o -name '*.kube' -o -name '*.image' -o -name '*.build' \) 2>/dev/null | wc -l | tr -d ' ')"
  pass "$system_dir exists with $count Quadlet files"
else
  warn "$system_dir does not exist or is not readable"
fi

if [ -d "$user_dir" ]; then
  count="$(find "$user_dir" -maxdepth 1 -type f \( -name '*.container' -o -name '*.pod' -o -name '*.network' -o -name '*.volume' -o -name '*.kube' -o -name '*.image' -o -name '*.build' \) 2>/dev/null | wc -l | tr -d ' ')"
  pass "$user_dir exists with $count Quadlet files"
else
  warn "$user_dir does not exist or is not readable"
fi

if have podman; then
  if podman info >/dev/null 2>&1; then
    pass "podman info works"
  else
    warn "podman info failed for this user"
  fi
fi

quadlet_generator=""
for candidate in /usr/libexec/podman/quadlet /usr/lib/podman/quadlet; do
  if [ -x "$candidate" ]; then
    quadlet_generator="$candidate"
    break
  fi
done

if [ -n "$quadlet_generator" ]; then
  pass "quadlet generator found at $quadlet_generator"
else
  warn "quadlet generator not found at common Fedora/Debian paths"
fi

if [ -n "$url" ]; then
  if ! have curl; then
    warn "curl is not installed; skipping HTTP checks"
    exit 0
  fi

  auth_body="$tmpdir/auth.json"
  auth_status="$(curl -ksS -o "$auth_body" -w '%{http_code}' "$url/api/auth" || true)"
  case "$auth_status" in
    200)
      pass "$url/api/auth returned 200"
      ;;
    401|403)
      pass "$url/api/auth returned $auth_status; server is reachable and protected"
      ;;
    000)
      warn "$url/api/auth was not reachable"
      ;;
    *)
      warn "$url/api/auth returned HTTP $auth_status"
      ;;
  esac

  units_body="$tmpdir/units.json"
  units_status="$(curl -ksS -o "$units_body" -w '%{http_code}' "$url/api/units" || true)"
  case "$units_status" in
    200)
      pass "$url/api/units returned 200"
      if grep -q "\"scope\"[[:space:]]*:[[:space:]]*\"$scope\"" "$units_body"; then
        pass "units response includes scope '$scope'"
      else
        warn "units response did not include scope '$scope'"
      fi
      ;;
    401|403)
      pass "$url/api/units returned $units_status; login is required"
      ;;
    000)
      warn "$url/api/units was not reachable"
      ;;
    *)
      warn "$url/api/units returned HTTP $units_status"
      ;;
  esac
fi

pass "non-destructive smoke checks complete"
