#!/usr/bin/env bash
# T3 devops — run the FROZEN release path inside a real Linux container.
#
# Usage: scripts/release/linux-release-gates.sh <amd64|arm64> <evidence-dir>
#
# This is the release-pipeline sibling of scripts/qa/linux-gates.sh and keeps the
# same evidence discipline: pinned toolchain from scripts/tools.env, recorded
# image digest, recorded kernel/OS, owner-only TMPDIR, tee'd log, recorded exit
# code. It exists because `goreleaser`/`syft` are Linux-release tools that must
# be proven on Linux — a darwin run proves the config parses, not that the
# release builds.
#
# AS-FROZEN means: the checked-in packaging/goreleaser/goreleaser.yaml and the
# pins in scripts/tools.env, with NO ad-hoc edits. The repo is bind-mounted
# READ-ONLY and copied to a container-local worktree, so a run can neither
# mutate the host tree nor be rescued by a host-side patch. ./.tools is dropped
# from the copy so the host's (darwin) binaries can never satisfy the toolcheck.
#
# The command sequence is exactly the release.yml `build` job's gate steps:
#   make release-check            (goreleaser check, fails closed off-pin)
#   make release-snapshot         (tarballs + checksums + SBOMs)
#   scripts/release/record-provenance.sh dist
#   make release-verify           (recompute checksums, SBOM/metadata presence)
set -euo pipefail
cd "$(dirname "$0")/../.."

ARCH="${1:?amd64|arm64}"
EVID="${2:?evidence dir}"
mkdir -p "$EVID"

# shellcheck disable=SC1091
. scripts/tools.env

case "$ARCH" in
  amd64|arm64) PLATFORM="--platform linux/$ARCH" ;;
  *) echo "unknown arch: $ARCH (want amd64|arm64)" >&2; exit 2 ;;
esac

IMAGE="golang:${GO_VERSION}"
CFG=packaging/goreleaser/goreleaser.yaml
LOG="$EVID/release-gates-$ARCH.log"

sha256() { if command -v sha256sum >/dev/null 2>&1; then sha256sum "$@"; else shasum -a 256 "$@"; fi; }

{
  echo "== linux release gates $ARCH =="
  echo "image=$IMAGE"
  docker image inspect --format 'image_digest={{index .RepoDigests 0}}' "$IMAGE" 2>/dev/null || true
  echo "host=$(uname -srm) docker=$(docker version --format '{{.Server.Version}}')"
  echo "repo_head=$(git rev-parse HEAD) describe=$(git describe --tags --always --dirty)"
  echo "pins: GO_VERSION=$GO_VERSION GOTOOLCHAIN=$GOTOOLCHAIN GORELEASER_VERSION=$GORELEASER_VERSION SYFT_VERSION=$SYFT_VERSION CGO_ENABLED=$CGO_ENABLED"
  # Bind the log to the exact inputs under test: a later edit to any of these
  # invalidates this evidence, and the digest is how a reader can tell.
  echo "config=$CFG config_sha256=$(sha256 "$CFG" | awk '{print $1}')"
  echo "toolsenv_sha256=$(sha256 scripts/tools.env | awk '{print $1}')"
  echo "makefile_sha256=$(sha256 Makefile | awk '{print $1}')"
  echo "dirty_tracked_files=$(git status --porcelain --untracked-files=no | wc -l | tr -d ' ')"
  git status --porcelain --untracked-files=no | sed 's/^/  dirty: /'
  echo "cmd=make release-check && make release-snapshot && scripts/release/record-provenance.sh dist && make release-verify"
} | tee "$LOG"

# shellcheck disable=SC2086  # $PLATFORM must word-split into two argv entries.
docker run --rm $PLATFORM \
  -v "$PWD:/src:ro" \
  -v "amux-gomod-release-$ARCH:/go/pkg/mod" \
  -v "amux-gobuild-release-$ARCH:/root/.cache/go-build" \
  -e GOFLAGS=-buildvcs=false \
  "$IMAGE" bash -c '
    set -euo pipefail
    echo "container: $(uname -srm) $(. /etc/os-release && echo "$PRETTY_NAME")"

    # Container-local worktree: the host tree is read-only, so nothing here can
    # write back into the repo. Drop ./.tools so the host darwin binaries can
    # never satisfy release-toolcheck, and drop stale dist/build.
    cp -a /src /work
    rm -rf /work/.tools /work/dist /work/build /work/.amux-artifacts
    cd /work
    git config --global --add safe.directory /work

    . scripts/tools.env
    export GOTOOLCHAIN CGO_ENABLED

    # STR-3 owner-only TMPDIR, same rule the QA linux gates apply.
    install -d -m 700 /reltmp
    export TMPDIR=/reltmp

    go version
    echo "go_env_GOTOOLCHAIN=$(go env GOTOOLCHAIN)"

    # Pinned release tooling, installed into a container-local GOBIN (NOT the
    # bind mount). This is `make release-tools` with GOBIN redirected.
    export GOBIN=/reltools/bin
    mkdir -p "$GOBIN"
    export PATH="$GOBIN:$PATH"
    echo "== installing pinned release tools =="
    go install "github.com/goreleaser/goreleaser/v2@$GORELEASER_VERSION"
    go install "github.com/anchore/syft/cmd/syft@$SYFT_VERSION"
    goreleaser --version | sed -n "1,20p"
    syft version | sed -n "1,10p"

    echo "== STEP 1: make release-check =="
    make release-check; echo "release-check exit=$?"

    echo "== STEP 2: make release-snapshot =="
    make release-snapshot; echo "release-snapshot exit=$?"

    echo "== STEP 3: scripts/release/record-provenance.sh dist =="
    scripts/release/record-provenance.sh dist; echo "record-provenance exit=$?"

    echo "== STEP 4: make release-verify =="
    make release-verify; echo "release-verify exit=$?"

    echo "== dist listing =="
    ls -l dist
    echo "== checksums.txt =="
    cat dist/checksums.txt
    echo "== archive contents (amd64) =="
    tar -tzf dist/amux_*_linux_amd64.tar.gz
  ' 2>&1 | tee -a "$LOG"
rc=${PIPESTATUS[0]}
echo "exit=$rc" | tee -a "$LOG"
exit "$rc"
