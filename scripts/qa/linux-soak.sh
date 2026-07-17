#!/usr/bin/env bash
# T6 QA — execute the blocking soak (scripts/soak/run-soak.sh, work package
# Q7) inside a real Linux container so the 20-PTY workload runs against a
# Linux kernel. Evidence lands in the repo's .amux-artifacts/soak/<stamp>/
# exactly as on any other host; metadata.env records the container kernel and
# distro so this is never mistaken for the Arch x86_64 reference profile.
#
# Usage: scripts/qa/linux-soak.sh <duration> [seed] [ptys]
set -euo pipefail
cd "$(dirname "$0")/../.."

DURATION="${1:?duration, e.g. 30m}"
SEED="${2:-1}"
PTYS="${3:-20}"

# archlinux publishes x86_64 only; on non-x86 hosts this runs emulated and the
# recorded kernel/arch metadata says so.
docker run --rm --platform linux/amd64 -v "$PWD:/src" -v amux-gomod-arch:/go/pkg/mod -w /src \
  -e GOPATH=/go -e GOFLAGS=-buildvcs=false \
  -e AMUX_SOAK_DURATION="$DURATION" -e AMUX_SOAK_SEED="$SEED" -e AMUX_SOAK_PTYS="$PTYS" \
  archlinux:latest bash -c '
    set -euo pipefail
    # pacman seccomp download sandbox fails under emulation (EINVAL) — disable
    # it for the throwaway container bootstrap only.
    echo DisableSandbox >> /etc/pacman.conf
    pacman -Sy --noconfirm --needed go git >/dev/null
    . scripts/tools.env
    export GOTOOLCHAIN CGO_ENABLED
    # STR-3: /tmp (1777) cannot host a socket chain — use an owner-only TMPDIR.
    install -d -m 700 /qatmp
    export TMPDIR=/qatmp
    echo "container: $(uname -srm) $(. /etc/os-release && echo "$PRETTY_NAME")"
    go version
    scripts/soak/run-soak.sh
  '
