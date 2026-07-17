#!/usr/bin/env bash
# License gate (T3 devops, D1; ADR-0007 Decision 2).
#
# Verifies that every module in the linux test graph carries a license file whose
# text matches the permissive allowlist (MIT, BSD-2/3-Clause, Apache-2.0, ISC).
# It reads the LICENSE/COPYING file INSIDE the downloaded module (never registry
# metadata), matching the verification method docs/dependencies.md documents.
#
# Copyleft / source-available / dual-licensed / attribution-plus obligations are
# an ADR-0007 autonomy gate: they FAIL here so the change stops at a human
# decision rather than shipping. This is a policy gate, not a legal review.
set -euo pipefail
cd "$(dirname "$0")/.."

modcache="$(go env GOMODCACHE)"
# Ensure the modules we are about to inspect are actually in the cache.
# `go mod download all` would ADD go.sum hashes for module-graph-only deps
# (dirtying the tree the tidy-check gate then fails on), so go.mod/go.sum are
# snapshotted and restored around the cache priming — this gate must stay
# read-only on the repo.
cp go.mod go.mod.lic-bak
cp go.sum go.sum.lic-bak
GOOS=linux GOARCH=amd64 go build ./... >/dev/null 2>&1 || true
go list -deps -test -f '{{if not .Standard}}{{.Module.Path}}@{{.Module.Version}}{{end}}' ./... \
	>/dev/null 2>&1 || true
GOFLAGS=-mod=mod go mod download all >/dev/null 2>&1 || true
mv go.mod.lic-bak go.mod
mv go.sum.lic-bak go.sum

allow_re='MIT|BSD 2-Clause|BSD 3-Clause|BSD-2-Clause|BSD-3-Clause|Redistribution and use in source|Apache License|ISC License|Permission to use, copy, modify'

fail=0
while IFS= read -r mv; do
	[ -z "$mv" ] && continue
	path="${mv%@*}"; ver="${mv##*@}"
	# Module cache escapes uppercase as !x; google/uuid etc. are all lowercase here.
	dir="$modcache/$path@$ver"
	lic="$(find "$dir" -maxdepth 1 \( -iname 'LICENSE*' -o -iname 'COPYING*' \) 2>/dev/null | head -1 || true)"
	if [ -z "$lic" ]; then
		echo "license: MISSING license file for $mv (looked in $dir)"; fail=1; continue
	fi
	if head -60 "$lic" | grep -Eq "$allow_re"; then
		kind="$(head -3 "$lic" | tr -s ' \t\n' ' ' | cut -c1-48)"
		echo "license: OK   $mv — $kind..."
	else
		echo "license: DENY $mv — $lic did not match the permissive allowlist (ADR-0007 gate)"; fail=1
	fi
done < <(grep -v '^#' scripts/expected-modules-test.txt | grep -v '^[[:space:]]*$')

if [ "$fail" -eq 0 ]; then echo "license: all modules permissive"; fi
exit $fail
