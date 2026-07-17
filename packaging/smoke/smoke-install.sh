#!/usr/bin/env bash
# Package-install smoke harness (T3 devops, work package D4).
#
# Proves a RELEASED tarball installs and its binaries run in a CLEAN glibc
# environment (Arch or Ubuntu 24.04). It is intentionally distro-agnostic so the
# same harness runs in an `archlinux:latest` and an `ubuntu:24.04` container.
# QA (Q8) executes it against the integrated candidate; pre-backend it already
# validates the skeleton binaries' identity, version stamping, and completions.
#
# Usage:
#   packaging/smoke/smoke-install.sh path/to/amux_<ver>_linux_<arch>.tar.gz
#   packaging/smoke/smoke-install.sh --dist dist    # newest tarball in dist/
#
# Exits non-zero on any failed check — never a soft pass.
set -euo pipefail

tarball="${1:-}"
if [ "$tarball" = "--dist" ]; then
  dir="${2:-dist}"
  tarball="$(ls -t "$dir"/amux_*_linux_*.tar.gz 2>/dev/null | head -1 || true)"
fi
[ -n "$tarball" ] && [ -f "$tarball" ] || { echo "smoke: no tarball (got '$tarball')"; exit 1; }

here="$(cd "$(dirname "$0")" && pwd)"
# shellcheck source=packaging/smoke/lib-linkage.sh
. "$here/lib-linkage.sh"

work="$(mktemp -d)"
trap 'rm -rf "$work"' EXIT
prefix="$work/opt/amux"
mkdir -p "$prefix"
tar -xzf "$tarball" -C "$prefix"
echo "smoke: extracted $(basename "$tarball") -> $prefix"

fail=0
must() { if "$@"; then echo "smoke: OK   $*"; else echo "smoke: FAIL $*"; fail=1; fi; }

# 1. Both binaries exist and are executable.
must test -x "$prefix/amux"
must test -x "$prefix/amuxd"

# 2. They run and report a stamped version (not the un-stamped dev default when
#    installed from a real release; skeleton snapshots may still read dev).
"$prefix/amux" --version || { echo "smoke: FAIL amux --version"; fail=1; }
"$prefix/amuxd" --version || { echo "smoke: FAIL amuxd --version"; fail=1; }

# 3. Static / cgo-free identity (FAIL-CLOSED): a CGO_ENABLED=0 release build MUST
#    be statically linked. A dynamically linked / libc-dependent binary is a
#    forbidden cgo or runtime-linkage regression (ADR-0007 Decision 4) and FAILS
#    the gate — never a soft note. If neither `file` nor `ldd` is available we
#    cannot prove static linkage, so we fail rather than silently pass.
#    Verdict logic + its behavioral proof: lib-linkage.sh + linkage-fixture.test.sh.
for bin in amux amuxd; do
  file_out=""; ldd_out=""
  command -v file >/dev/null && file_out="$(file "$prefix/$bin" 2>&1 || true)"
  command -v ldd  >/dev/null && ldd_out="$(ldd  "$prefix/$bin" 2>&1 || true)"
  if [ -z "$file_out" ] && [ -z "$ldd_out" ]; then
    echo "smoke: FAIL $bin — no linkage probe (file/ldd) present; cannot prove cgo-free (fail-closed)"
    fail=1; continue
  fi
  verdict="$(amux_linkage_verdict "$file_out" "$ldd_out")" || true
  if [ "$verdict" = "static" ]; then
    echo "smoke: OK   $bin is statically linked (cgo-free)"
  else
    echo "smoke: FAIL $bin is NOT provably static ($verdict) — cgo/dynamic-link regression"
    echo "smoke:      file: ${file_out:-<none>}"
    echo "smoke:      ldd:  ${ldd_out:-<none>}"
    fail=1
  fi
done

# 4. Completions shipped for all three shells.
for sh in bash zsh fish; do must test -s "$prefix/completions/amux.$sh"; done

# 5. Install-time safety: nothing in the tarball should be a service that could
#    auto-start. The systemd unit ships as an example only (WantedBy is opt-in).
if [ -f "$prefix/systemd/amuxd.user.service" ]; then
  echo "smoke: OK   example user unit present (opt-in; not auto-started)"
fi

[ "$fail" -eq 0 ] && echo "smoke: PASS" || echo "smoke: FAILED"
exit $fail
