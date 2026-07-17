#!/usr/bin/env bash
# T6 QA — run the blocking Go gates inside a real Linux container (work
# packages Q3/Q5/Q6: the Linux-only seams — `amux daemon start` subprocess
# path, SO_PEERCRED, /proc-based process inspection — only execute on Linux).
#
# Usage: scripts/qa/linux-gates.sh <arch|ubuntu> <evidence-dir> [extra go-test args]
#
# The container gets the repo bind-mounted at /src and a persistent module
# cache volume. The pinned toolchain (scripts/tools.env GOTOOLCHAIN) is
# enforced inside the container exactly as on the host, so "green in the
# container" means the same toolchain as CI. This script substitutes for NO
# hosted-CI gate: it is local Linux evidence, recorded with full metadata.
set -euo pipefail
cd "$(dirname "$0")/../.."

DISTRO="${1:?arch|ubuntu}"
EVID="${2:?evidence dir}"
shift 2 || true
mkdir -p "$EVID"

# archlinux publishes x86_64 only (Arch proper has no aarch64 port), so the
# arch lane pins --platform linux/amd64 — on Apple Silicon that runs under
# Rosetta/qemu and the log's uname records it. Ubuntu runs host-native.
case "$DISTRO" in
  # pacman's own seccomp download sandbox fails under qemu/Rosetta emulation
  # (EINVAL) — DisableSandbox affects only package download inside the
  # throwaway container, not anything under test.
  arch)   IMAGE=archlinux:latest;  PLATFORM="--platform linux/amd64"; BOOTSTRAP='echo DisableSandbox >> /etc/pacman.conf && pacman -Sy --noconfirm --needed go git >/dev/null' ;;
  ubuntu) IMAGE=ubuntu:24.04;      PLATFORM="";                       BOOTSTRAP='apt-get update -qq >/dev/null && apt-get install -y -qq golang-go git ca-certificates >/dev/null' ;;
  *) echo "unknown distro: $DISTRO" >&2; exit 2 ;;
esac

LOG="$EVID/linux-gates-$DISTRO.log"
{
  echo "== linux-gates $DISTRO =="
  echo "image=$IMAGE"
  docker image inspect --format 'image_digest={{index .RepoDigests 0}}' "$IMAGE" 2>/dev/null || true
  echo "host=$(uname -srm) docker=$(docker version --format '{{.Server.Version}}')"
  echo "cmd=go test -count=1 $* ./..."
} | tee "$LOG"

docker run --rm $PLATFORM -v "$PWD:/src" -v amux-gomod-$DISTRO:/go/pkg/mod -w /src \
  -e GOPATH=/go -e GOFLAGS=-buildvcs=false \
  "$IMAGE" bash -c "
    set -euo pipefail
    $BOOTSTRAP
    . scripts/tools.env
    export GOTOOLCHAIN CGO_ENABLED
    # STR-3 rejects any socket path with a group/other-writable component, so
    # /tmp (1777) can never host a test socket chain: give the suite an
    # owner-only TMPDIR, exactly what a hosted Linux CI runner must also do.
    install -d -m 700 /qatmp
    export TMPDIR=/qatmp
    echo \"container: \$(uname -srm) \$(. /etc/os-release && echo \\\"\$PRETTY_NAME\\\")\"
    go version
    go test -count=1 $* ./...
  " 2>&1 | tee -a "$LOG"
rc=${PIPESTATUS[0]}
echo "exit=$rc" | tee -a "$LOG"
exit "$rc"
