#!/usr/bin/env bash
# Emit the `race-full-suite` check receipt — FAIL-CLOSED on host OS (T3 devops).
#
# The readiness manifest freezes `race-full-suite` to the Linux CI matrix
# (checks[].prerequisites: "Linux CI runners (T3 lane)"; description: "under the
# Go race detector on the Linux CI matrix"). A darwin/arm64 race run is NOT that
# evidence, yet a hand-authored receipt previously recorded `outcome: pass` from
# a darwin host. This script makes that impossible: it refuses to write a `pass`
# receipt unless it is actually running on Linux, and it records the real host.
#
# On a non-Linux host it writes `outcome: deferred_prerequisite` (the manifest's
# vocabulary for "the environment cannot produce this evidence") and exits 3 —
# never `pass`, never silent. Run it inside the Arch/Ubuntu container harness
# (scripts/qa/linux-gates.sh style) or on a Linux CI runner.
#
# Usage: scripts/ci/race-full-suite-receipt.sh
set -uo pipefail
cd "$(dirname "$0")/../.." || { echo "race-full-suite-receipt: cannot cd to repo root" >&2; exit 1; }

CHECK_ID="race-full-suite"
COMMAND="go test -race -count=1 ./..."
OUT_DIR=".amux-artifacts/security"
EVIDENCE="$OUT_DIR/race-full-suite.txt"
RECEIPT="$OUT_DIR/race-full-suite.receipt.json"
mkdir -p "$OUT_DIR"

host_os="$(uname -s | tr '[:upper:]' '[:lower:]')"
host_arch="$(uname -m)"
case "$host_arch" in x86_64) host_arch=amd64 ;; aarch64) host_arch=arm64 ;; esac
started_at="$(date -u +%Y-%m-%dT%H:%M:%SZ)"
tool_version="$(go version 2>/dev/null | awk '{print $3}')"

emit_receipt() { # outcome exit_code notes
  cat >"$RECEIPT" <<JSON
{
 "schema": "amux.security.check-receipt.v1",
 "check_id": "$CHECK_ID",
 "command": "$COMMAND",
 "exit_code": $2,
 "outcome": "$1",
 "started_at": "$started_at",
 "host_os": "$host_os",
 "host_arch": "$host_arch",
 "tool_version": "$tool_version",
 "evidence_path": "$EVIDENCE",
 "notes": "$3"
}
JSON
  echo "race-full-suite-receipt: wrote $RECEIPT (outcome=$1, host_os=$host_os/$host_arch)"
}

# FAIL-CLOSED host gate: the frozen prerequisite is a Linux CI runner. A darwin
# (or any non-linux) host can never satisfy it, so we never record `pass` here.
if [ "$host_os" != "linux" ]; then
  {
    echo "race-full-suite: NOT RUN as satisfying evidence."
    echo "host_os=$host_os host_arch=$host_arch — the frozen prerequisite is a Linux CI runner."
    echo "A non-Linux race run is not the frozen race-full-suite evidence; recording deferred_prerequisite."
  } | tee "$EVIDENCE"
  emit_receipt "deferred_prerequisite" 0 \
    "Refused to record pass on host_os=$host_os: race-full-suite is frozen to the Linux CI matrix (readiness-manifest prerequisites). Re-run inside the Arch/Ubuntu container harness or on a Linux CI runner. A darwin race run is a local cross-check only, never this gate."
  exit 3
fi

# On Linux: run the real race suite and record the true outcome. The Go race
# detector links the C runtime, so it needs cgo + a C compiler even though every
# PRODUCT build is CGO_ENABLED=0 (ADR-0007 D4); `-race` is a test-only tool, not
# a shipped artifact, so enabling cgo here does not weaken the cgo prohibition.
export CGO_ENABLED=1
echo "== race-full-suite (Linux) ==" | tee "$EVIDENCE"
echo "host=$(uname -srm) tmpdir=${TMPDIR:-<unset>} tool=$tool_version cgo=1" | tee -a "$EVIDENCE"
set -o pipefail
go test -race -count=1 ./... 2>&1 | tee -a "$EVIDENCE"
rc=${PIPESTATUS[0]}
echo "exit=$rc" | tee -a "$EVIDENCE"

if [ "$rc" -eq 0 ]; then
  emit_receipt "pass" 0 "Full suite under the Go race detector on Linux $host_arch (container harness / CI matrix). TMPDIR=${TMPDIR:-<unset>} (STR-3 owner-safe)."
else
  emit_receipt "fail" "$rc" "Race suite failed on Linux $host_arch (exit $rc); see evidence_path."
fi
exit "$rc"
