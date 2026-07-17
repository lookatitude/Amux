#!/usr/bin/env bash
# Behavioral fixture proving the smoke harness cgo/static gate (T3 devops, F2).
#
# Exercises BOTH branches of amux_linkage_verdict with canned `file`/`ldd`
# strings, so the fail-closed behaviour is proven deterministically on any host
# (macOS included) without needing a real ELF binary. This is the guard that a
# dynamically linked / cgo-enabled artifact FAILS the smoke gate rather than
# passing with a note (the exact regression the round-1 review flagged).
set -euo pipefail
here="$(cd "$(dirname "$0")" && pwd)"
# shellcheck source=packaging/smoke/lib-linkage.sh
. "$here/lib-linkage.sh"

pass=0; failn=0
check() { # <desc> <expected-verdict> <file_out> <ldd_out>
  desc=$1; expected=$2; fo=$3; lo=$4
  got="$(amux_linkage_verdict "$fo" "$lo")" || true
  if [ "$got" = "$expected" ]; then
    echo "ok   $desc -> $got"; pass=$((pass + 1))
  else
    echo "FAIL $desc -> got '$got', want '$expected'"; failn=$((failn + 1))
  fi
}

# --- STATIC: cgo-free release binaries MUST pass the gate --------------------
check "cgo-free static ELF (file + ldd agree)" static \
  "amux: ELF 64-bit LSB executable, x86-64, statically linked, Go BuildID=abc, not stripped" \
  "	not a dynamic executable"
check "static-pie: old file says dynamic, ldd is authoritative" static \
  "amux: ELF 64-bit LSB pie executable, x86-64, dynamically linked, interpreter /lib/ld-linux-aarch64.so.1" \
  "	statically linked"
check "file-only host reports static-pie linked" static \
  "amux: ELF 64-bit LSB pie executable, x86-64, static-pie linked, stripped" \
  ""

# --- DYNAMIC: a cgo / libc-linked artifact MUST fail the gate ----------------
check "cgo binary linked against libc (ldd lists deps)" dynamic \
  "amuxd: ELF 64-bit LSB executable, x86-64, dynamically linked, interpreter /lib64/ld-linux-x86-64.so.2" \
  "	linux-vdso.so.1 (0x0000)
	libc.so.6 => /usr/lib/libc.so.6 (0x0000)
	/lib64/ld-linux-x86-64.so.2 (0x0000)"
check "dynamic per file, no ldd on host" dynamic \
  "amuxd: ELF 64-bit LSB executable, x86-64, dynamically linked, interpreter /lib64/ld-linux-x86-64.so.2, not stripped" \
  ""

# --- UNPROVABLE: no positive static proof -> fail closed --------------------
check "no probes available -> fail closed" unprovable "" ""
check "uninformative file, no ldd -> fail closed" unprovable "amux: data" ""

echo "---"
if [ "$failn" -eq 0 ]; then
  echo "linkage-fixture: PASS ($pass checks)"
else
  echo "linkage-fixture: FAILED ($failn of $((pass + failn)))"; exit 1
fi
