#!/usr/bin/env bash
# Dependency-integrity gate (T3 devops, D1; ADR-0007 Decision 3).
#
# Freezes the third-party module graph so a stray `go get` cannot silently
# widen the supply chain. It regenerates the linux release-build and test module
# graphs with the EXACT commands frozen in docs/dependencies.md and diffs them
# against the checked-in expected lists. Any drift fails the build and is a
# reminder that docs/dependencies.md must be regenerated in the same change.
#
# It is host-agnostic: both graphs are computed under GOOS=linux GOARCH=amd64 so
# the result does not depend on the developer's OS (macOS would otherwise omit
# the golang.org/x/sys linux files).
set -euo pipefail
cd "$(dirname "$0")/.."

fail=0
tmp="$(mktemp -d)"
trap 'rm -rf "$tmp"' EXIT

gen() { # $1=extra-flags -> sorted module@version list on stdout
	GOOS=linux GOARCH=amd64 go list -deps $1 \
		-f '{{if not .Standard}}{{.Module.Path}}@{{.Module.Version}}{{end}}' ./... \
		| grep -v '^$' | grep -v '^github.com/amux-run' | sort -u
}

check() { # $1=label $2=expected-file $3=extra-flags
	gen "$3" >"$tmp/actual"
	if ! diff -u "$2" <(grep -v '^#' "$2" | grep -v '^$' | sort -u) >/dev/null 2>&1; then
		: # expected file may carry comments; normalise below
	fi
	grep -v '^#' "$2" | grep -v '^[[:space:]]*$' | sort -u >"$tmp/expected"
	if diff -u "$tmp/expected" "$tmp/actual"; then
		echo "deps-manifest: $1 graph matches frozen manifest"
	else
		echo "deps-manifest: DRIFT in the $1 graph (see diff above)."
		echo "  If this change is intended: update $2 AND regenerate docs/dependencies.md"
		echo "  (regeneration commands are frozen in that file's header)."
		fail=1
	fi
}

check "release-build" scripts/expected-modules-build.txt ""
check "test"          scripts/expected-modules-test.txt "-test"

# go.sum hash integrity is a separate, cheap belt-and-braces check.
go mod verify >/dev/null && echo "deps-manifest: go.sum hashes verified"

exit $fail
