#!/usr/bin/env bash
# Soak / performance evidence harness (T3 devops, work package D5).
#
# Standardises HOW a soak run is executed and WHAT evidence it retains, so the
# 30-minute blocking soak and the 8-hour nightly soak differ only by duration.
# For EVERY run it captures the metadata the spec requires: structured logs,
# pprof snapshots, metrics, fixture seed, version, kernel/distro, and a pass/fail
# summary — into a timestamped evidence dir under .amux-artifacts/soak/.
#
# The soak WORKLOAD itself (20 concurrent PTYs against a real daemon) is owned by
# backend/QA: this harness invokes it via the documented target
#   go test -tags soak -run TestSoak ./... -soak.duration=<D> -soak.seed=<S>
# When that target does not exist yet (pre-backend), the harness FAILS CLOSED
# (exit 1) unless AMUX_SOAK_ALLOW_SCAFFOLD=1 is set for pipeline wiring tests.
# This guarantees a release can never be cut on a phantom soak (autonomy policy:
# forbidden to treat missing evidence as a pass).
set -euo pipefail
cd "$(dirname "$0")/../.."

DURATION="${AMUX_SOAK_DURATION:-30m}"
SEED="${AMUX_SOAK_SEED:-1}"
PTYS="${AMUX_SOAK_PTYS:-20}"
STAMP="$(date -u +%Y%m%dT%H%M%SZ 2>/dev/null || echo run)"
EVID=".amux-artifacts/soak/$STAMP"
mkdir -p "$EVID/pprof"

# --- Reproducible run metadata (retained for every soak/benchmark) ----------
{
  echo "run_utc=$STAMP"
  echo "duration=$DURATION"
  echo "seed=$SEED"
  echo "concurrent_ptys=$PTYS"
  echo "git_commit=$(git rev-parse HEAD 2>/dev/null || echo unknown)"
  echo "git_describe=$(git describe --tags --always --dirty 2>/dev/null || echo unknown)"
  echo "go_version=$(go version | awk '{print $3}')"
  echo "kernel=$(uname -sr)"
  echo "arch=$(uname -m)"
  echo "distro=$( (. /etc/os-release 2>/dev/null && echo "$PRETTY_NAME") || echo unknown)"
  echo "nproc=$( (nproc 2>/dev/null) || sysctl -n hw.ncpu 2>/dev/null || echo unknown)"
} >"$EVID/metadata.env"
echo "soak: metadata ->"; cat "$EVID/metadata.env"

# --- Discover the soak workload target --------------------------------------
if ! grep -rEl 'func TestSoak' --include='*_test.go' . >/dev/null 2>&1; then
  msg="soak: the soak workload target (TestSoak, -tags soak) does not exist yet (pre-backend)."
  {
    echo "result=SKIPPED_NO_WORKLOAD"
    echo "reason=backend/QA supply the 20-PTY soak workload; harness+evidence layout are ready"
  } >>"$EVID/summary.env"
  if [ "${AMUX_SOAK_ALLOW_SCAFFOLD:-0}" = "1" ]; then
    echo "$msg (AMUX_SOAK_ALLOW_SCAFFOLD=1 → wiring test, exit 0)"; exit 0
  fi
  echo "$msg"
  echo "soak: FAILING CLOSED — a release requires real soak evidence. Set AMUX_SOAK_ALLOW_SCAFFOLD=1 only to test pipeline wiring." >&2
  exit 1
fi

# --- Execute the soak, streaming structured logs ----------------------------
# T6 QA harness note: the -soak.* flags are registered only by the package(s)
# defining TestSoak, so the invocation targets exactly those packages (passing
# them to ./... would abort every other test binary with an unknown flag). The
# go-test timeout must exceed the soak duration (default 10m would kill the
# 30m/8h profiles); AMUX_SOAK_TIMEOUT bounds a hung run without weakening it.
TIMEOUT="${AMUX_SOAK_TIMEOUT:-10h}"
PKGS="$(grep -rEl 'func TestSoak' --include='*_test.go' --exclude-dir=.git . | xargs -n1 dirname | sort -u | sed 's|^\.|.|')"
echo "soak: running $DURATION with $PTYS PTYs, seed=$SEED (packages: $PKGS)"
set +e
# Evidence paths must be absolute: `go test` runs each test binary with the
# package directory as cwd, so a relative -soak.* path would land under the
# package, not the repo evidence tree.
AMUX_PPROF_DIR="$PWD/$EVID/pprof" \
go test -tags soak -run 'TestSoak$' -count=1 -timeout "$TIMEOUT" $PKGS \
  -soak.duration="$DURATION" -soak.seed="$SEED" -soak.ptys="$PTYS" \
  -soak.pprof="$PWD/$EVID/pprof" -soak.metrics="$PWD/$EVID/metrics.jsonl" \
  2>&1 | tee "$EVID/soak.log"
rc=${PIPESTATUS[0]}
set -e

# --- Pass/fail summary (the harness records the verdict; it does not soften it)
if [ "$rc" -eq 0 ]; then verdict=PASS; else verdict=FAIL; fi
{
  echo "result=$verdict"
  echo "exit_code=$rc"
  echo "log=$EVID/soak.log"
  echo "pprof_dir=$EVID/pprof"
  echo "metrics=$EVID/metrics.jsonl"
} >>"$EVID/summary.env"
echo "soak: $verdict — evidence in $EVID"
exit "$rc"
