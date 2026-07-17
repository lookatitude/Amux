#!/usr/bin/env bash
# Fuzz smoke gate (T3 devops, D1).
#
# Runs each Go fuzz target for a short, fixed budget so CI exercises the fuzz
# harnesses on every push WITHOUT turning into an open-ended campaign (QA owns
# long fuzz runs). Go only fuzzes one target per package per invocation, so we
# discover every `func Fuzz*` and run its package individually.
#
# Pre-backend there are no fuzz targets yet; this exits 0 with a clear note
# rather than a false "passed" — the harness is ready for T4/T6 to populate.
set -euo pipefail
cd "$(dirname "$0")/.."

FUZZTIME="${AMUX_FUZZTIME:-15s}"

# Collect files that define at least one fuzz target.
hits="$(grep -rEl '^func Fuzz[A-Z]' --include='*_test.go' . 2>/dev/null || true)"
if [ -z "$hits" ]; then
	echo "fuzz-smoke: no fuzz targets defined yet (pre-backend) — nothing to run"
	exit 0
fi

fail=0
seen=""
while IFS= read -r f; do
	[ -z "$f" ] && continue
	pkg="./$(dirname "$f")"
	while IFS= read -r fn; do
		[ -z "$fn" ] && continue
		key="$pkg::$fn"
		case " $seen " in *" $key "*) continue;; esac
		seen="$seen $key"
		echo "== fuzz $fn in $pkg (${FUZZTIME}) =="
		if ! go test "$pkg" -run '^$' -fuzz="^${fn}\$" -fuzztime="$FUZZTIME"; then
			fail=1
		fi
	done < <(grep -oE '^func (Fuzz[A-Za-z0-9_]+)' "$f" | awk '{print $2}')
done < <(printf '%s\n' "$hits")
exit $fail
