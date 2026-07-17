#!/usr/bin/env bash
# Owner-only TMPDIR setup + verify for Linux test/daemon/soak runs (T3 devops).
#
# WHY: the STR-3 local-transport hardening rejects any daemon control-socket path
# that has a group- or other-writable component anywhere in its directory chain.
# `/tmp` is mode 1777 (world-writable), so the default `TMPDIR=/tmp` makes every
# test that binds a socket under `$TMPDIR` fail closed — correctly. Production is
# unaffected (`$XDG_RUNTIME_DIR/amux` is 0700), but every Linux runner (CI,
# release, nightly) MUST export a 0700 owner-only TMPDIR before any `go test` or
# daemon command. This is the single reusable, fail-closed setup+verify step.
#
# FAIL-CLOSED: it never silently falls back to a world-writable tree. If it
# cannot create and prove an owner-safe TMPDIR it exits non-zero and the job
# fails, rather than running the suite under a chain STR-3 would reject.
#
# Usage:
#   scripts/ci/owner-tmpdir.sh          # exports TMPDIR to $GITHUB_ENV when set;
#                                       # otherwise prints `export TMPDIR=<dir>`.
#   eval "$(scripts/ci/owner-tmpdir.sh --print)"   # for a local shell
#
# Override the base with AMUX_TMPDIR_BASE (must itself be owner-safe).
set -euo pipefail

print_only=0
[ "${1:-}" = "--print" ] && print_only=1

# Numeric permission bits of a path, portable across GNU (Linux/CI) and BSD
# (macOS dev host) stat.
perm_bits() {
  stat -c '%a' "$1" 2>/dev/null || stat -f '%Lp' "$1"
}

# True if the path is group- or other-writable (mask 0022) — exactly what STR-3
# rejects in a socket-path ancestor chain.
is_group_or_other_writable() {
  local mode; mode="$(perm_bits "$1")"
  [ $(( 8#$mode & 0022 )) -ne 0 ]
}

# Walk from a directory up to `/`, failing closed if any ancestor is group/other
# writable — the same property STR-3 enforces on the live socket chain.
verify_chain() {
  local p; p="$(cd "$1" && pwd -P)"
  while :; do
    if is_group_or_other_writable "$p"; then
      echo "owner-tmpdir: FAIL '$p' is group/other-writable (mode $(perm_bits "$p")) — STR-3 would reject a socket under it" >&2
      return 1
    fi
    [ "$p" = "/" ] && break
    p="$(dirname "$p")"
  done
  return 0
}

# Base must be owned by us and not sit under a world/group-writable tree. Prefer
# an explicit override, else the runner temp, else HOME. Never /tmp.
base="${AMUX_TMPDIR_BASE:-${RUNNER_TEMP:-$HOME}}"
case "$base" in
  /tmp|/tmp/*|/var/tmp|/var/tmp/*)
    echo "owner-tmpdir: FAIL base '$base' is a world-writable tmp tree; refusing" >&2
    exit 1 ;;
esac
[ -d "$base" ] || { echo "owner-tmpdir: FAIL base '$base' does not exist" >&2; exit 1; }

# Create the owner-only dir with a restrictive umask, then pin it to 0700.
umask 077
dir="$(mktemp -d "${base%/}/amux-tmpdir.XXXXXX")" || {
  echo "owner-tmpdir: FAIL could not create a temp dir under '$base'" >&2; exit 1; }
chmod 700 "$dir"

mode="$(perm_bits "$dir")"
if [ "$mode" != "700" ]; then
  echo "owner-tmpdir: FAIL '$dir' is mode $mode, expected 700" >&2
  rmdir "$dir" 2>/dev/null || true
  exit 1
fi

if ! verify_chain "$dir"; then
  rmdir "$dir" 2>/dev/null || true
  exit 1
fi

# Prove a socket path under this TMPDIR would satisfy STR-3: bind a real unix
# socket in it. If even this cannot succeed, fail before running the suite.
probe="$dir/.owner-tmpdir-probe.sock"
if command -v python3 >/dev/null 2>&1; then
  if ! python3 - "$probe" <<'PY' 2>/dev/null
import socket, sys, os
p = sys.argv[1]
s = socket.socket(socket.AF_UNIX, socket.SOCK_STREAM)
try:
    s.bind(p)
finally:
    s.close()
    try: os.unlink(p)
    except OSError: pass
PY
  then
    echo "owner-tmpdir: FAIL could not bind a unix socket under '$dir'" >&2
    rmdir "$dir" 2>/dev/null || true
    exit 1
  fi
fi

echo "owner-tmpdir: OK TMPDIR=$dir (mode 700, ancestor chain g/o-non-writable, unix-socket bind proved)" >&2

if [ "$print_only" -eq 1 ]; then
  echo "export TMPDIR=$dir"
elif [ -n "${GITHUB_ENV:-}" ]; then
  # Every subsequent step in the job inherits the owner-safe TMPDIR.
  echo "TMPDIR=$dir" >> "$GITHUB_ENV"
else
  echo "owner-tmpdir: note — not in GitHub Actions and no --print; export TMPDIR=$dir yourself" >&2
fi
