#!/usr/bin/env bash
# T3 devops — run the installed-CLI smoke against THIS TREE's own release
# tarballs, in a clean glibc container.
#
# Usage: scripts/release/linux-install-smoke.sh <evidence-dir>
#
# Why this exists: the only install-smoke evidence in the repo was recorded
# against `amux_0.0.1-snapshot-none` tarballs BEFORE the fix commit, and was
# still being cited as if it described current artifacts. A smoke log is only
# evidence about the tarballs it actually extracted — so this builds the
# snapshot from the current tree and smokes THOSE artifacts, recording the
# tarball name and digest it tested.
#
# Two stages, deliberately in different images:
#   1. golang:<pin>  — build the frozen snapshot (needs the Go toolchain)
#   2. ubuntu:24.04  — extract + run the binaries in a CLEAN environment with no
#      Go toolchain present, which is what packaging/smoke/smoke-install.sh is
#      for. amd64 runs under --platform linux/amd64.
set -euo pipefail
cd "$(dirname "$0")/../.."

EVID="${1:?evidence dir}"
mkdir -p "$EVID"

# shellcheck disable=SC1091
. scripts/tools.env

BUILD_IMAGE="golang:${GO_VERSION}"
SMOKE_IMAGE="ubuntu:24.04"
LOG="$EVID/install-smoke.log"
STAGE="$(mktemp -d)"
trap 'rm -rf "$STAGE"' EXIT

sha256() { if command -v sha256sum >/dev/null 2>&1; then sha256sum "$@"; else shasum -a 256 "$@"; fi; }

{
  echo "== installed-CLI smoke on this tree's own tarballs =="
  echo "build_image=$BUILD_IMAGE smoke_image=$SMOKE_IMAGE"
  docker image inspect --format 'build_image_digest={{index .RepoDigests 0}}' "$BUILD_IMAGE" 2>/dev/null || true
  docker image inspect --format 'smoke_image_digest={{index .RepoDigests 0}}' "$SMOKE_IMAGE" 2>/dev/null || true
  echo "host=$(uname -srm) docker=$(docker version --format '{{.Server.Version}}')"
  echo "repo_head=$(git rev-parse HEAD) describe=$(git describe --tags --always --dirty)"
  echo "dirty_tracked_files=$(git status --porcelain --untracked-files=no | wc -l | tr -d ' ')"
  git status --porcelain --untracked-files=no | sed 's/^/  dirty: /'
  echo "harness=packaging/smoke/smoke-install.sh"
} | tee "$LOG"

echo "== STAGE 1: build the snapshot ==" | tee -a "$LOG"
docker run --rm --platform linux/arm64 \
  -v "$PWD:/src:ro" \
  -v "$STAGE:/out" \
  -v "amux-gomod-release-arm64:/go/pkg/mod" \
  -v "amux-gobuild-release-arm64:/root/.cache/go-build" \
  -e GOFLAGS=-buildvcs=false \
  "$BUILD_IMAGE" bash -c '
    set -euo pipefail
    cp -a /src /work && rm -rf /work/.tools /work/dist /work/build /work/.amux-artifacts
    cd /work && git config --global --add safe.directory /work
    . scripts/tools.env && export GOTOOLCHAIN CGO_ENABLED
    install -d -m 700 /reltmp && export TMPDIR=/reltmp
    export GOBIN=/reltools/bin PATH=/reltools/bin:$PATH
    mkdir -p "$GOBIN"
    go install "github.com/goreleaser/goreleaser/v2@$GORELEASER_VERSION" >/dev/null 2>&1
    go install "github.com/anchore/syft/cmd/syft@$SYFT_VERSION"          >/dev/null 2>&1
    make release-snapshot >/dev/null 2>&1
    cp dist/amux_*_linux_*.tar.gz /out/
    echo "built:"; sha256sum /out/*.tar.gz | sed "s#/out/#  #"
  ' 2>&1 | tee -a "$LOG"

rc_total=0
for arch in arm64 amd64; do
  # shellcheck disable=SC2012  # names are goreleaser-generated, no exotic chars
  tb="$(ls "$STAGE"/amux_*_linux_"$arch".tar.gz 2>/dev/null | head -1 || true)"
  if [ -z "$tb" ]; then echo "smoke: NO TARBALL for $arch" | tee -a "$LOG"; rc_total=1; continue; fi
  {
    echo "== STAGE 2: smoke $arch =="
    echo "tarball=$(basename "$tb")"
    echo "tarball_sha256=$(sha256 "$tb" | awk '{print $1}')"
  } | tee -a "$LOG"

  # Clean-room: repo mounted read-only ONLY for the harness scripts; the
  # container has no Go toolchain, so the binaries must stand on their own.
  docker run --rm --platform "linux/$arch" \
    -v "$PWD/packaging/smoke:/smoke:ro" \
    -v "$STAGE:/tarballs:ro" \
    "$SMOKE_IMAGE" bash -c "
      set -uo pipefail
      echo \"container: \$(uname -srm) \$(. /etc/os-release && echo \"\$PRETTY_NAME\")\"
      command -v go >/dev/null && echo 'WARNING: go present in smoke container' || echo 'clean env: no go toolchain'
      bash /smoke/smoke-install.sh /tarballs/$(basename "$tb")
      echo \"SMOKE_INSTALL_EXIT=\$?\"
    " 2>&1 | tee -a "$LOG"
  rc=${PIPESTATUS[0]}
  echo "stage2_${arch}_exit=$rc" | tee -a "$LOG"
  [ "$rc" -eq 0 ] || rc_total=1
done

echo "exit=$rc_total" | tee -a "$LOG"
exit "$rc_total"
