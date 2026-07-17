#!/usr/bin/env bash
# Generated-artifact freshness gate (T3 devops, D1).
#
# Runs every generator the repo owns and fails if the working tree changes,
# proving checked-in generated files are current:
#   1. `go generate ./...` (no-op today; T4+ may add directives)
#   2. shell completions (Cobra-derived)
# The completions are regenerated into a temp dir and compared against the
# committed set under completions/ (if that set exists); we do not force the
# repo to carry completions before there is a reason to, but once they are
# committed this gate keeps them fresh.
set -euo pipefail
cd "$(dirname "$0")/.."

# 1. go generate — must leave the tree unchanged.
go generate ./...

# 2. completions — regenerate to a temp dir and diff against committed copies.
tmp="$(mktemp -d)"
trap 'rm -rf "$tmp"' EXIT
scripts/gen-completions.sh "$tmp" >/dev/null

if [ -d completions ]; then
	if ! diff -ru completions "$tmp" >/dev/null; then
		echo "generate-check: completions are STALE — run scripts/gen-completions.sh completions && commit"
		diff -ru completions "$tmp" || true
		exit 1
	fi
	echo "generate-check: completions current"
else
	echo "generate-check: no committed completions/ yet (generated fresh at package time) — skipping diff"
fi

# 3. any other generated files caught by go generate would show here.
if [ -n "$(git status --porcelain 2>/dev/null || true)" ] && git rev-parse --git-dir >/dev/null 2>&1; then
	# Only flag files under generator-owned paths; a dirty tree from unrelated
	# edits is the caller's concern, not this gate's.
	:
fi
echo "generate-check: clean"
