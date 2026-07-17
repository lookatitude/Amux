# shellcheck shell=bash
# Linkage classifier for the package smoke harness (T3 devops, D4).
#
# Single responsibility: decide, from the output of `file` and/or `ldd`, whether
# a release binary is statically linked (cgo-free, ADR-0007 Decision 4) or has
# drifted into forbidden dynamic/libc linkage. It is FAIL-CLOSED: if neither
# probe can POSITIVELY prove static linkage, the verdict is `unprovable` and the
# caller must fail — a binary is never assumed cgo-free by default.
#
# Kept as a pure, side-effect-free function of two strings so the failure branch
# is provable with canned fixtures on any host (see linkage-fixture.test.sh),
# not only against a real ELF on Linux.

# amux_linkage_verdict <file_output> <ldd_output>
#   echoes exactly one of: static | dynamic | unprovable
#   returns 0 only for `static`; non-zero for `dynamic` and `unprovable`.
amux_linkage_verdict() {
  file_out=$1
  ldd_out=$2

  # 1) `ldd` is authoritative for libc / shared-object dependencies. It settles
  #    the static-PIE ambiguity that `file` alone gets wrong (an old `file`
  #    prints "dynamically linked" for a static-pie binary whose `ldd` still
  #    reports "not a dynamic executable").
  if [ -n "$ldd_out" ]; then
    if printf '%s' "$ldd_out" | grep -Eqi 'not a dynamic executable|statically linked'; then
      echo static; return 0
    fi
    if printf '%s' "$ldd_out" | grep -Eqi '=>|\.so($|[.[:space:]])|ld-linux|ld-musl'; then
      echo dynamic; return 1
    fi
  fi

  # 2) `file` as the secondary probe (present on hosts without `ldd`).
  if [ -n "$file_out" ]; then
    if printf '%s' "$file_out" | grep -Eqi 'static-pie linked|statically linked'; then
      echo static; return 0
    fi
    if printf '%s' "$file_out" | grep -Eqi 'dynamically linked'; then
      echo dynamic; return 1
    fi
  fi

  # 3) Neither probe positively proved static linkage — fail closed.
  echo unprovable; return 1
}
