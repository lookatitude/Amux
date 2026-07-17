#!/usr/bin/env bash
# Reproducible build-input record (T3 devops, D3; ADR-0007 "reproducible input
# records"). Captures the exact inputs a release build depended on so a third
# party can reproduce the binaries bit-for-bit. Writes dist/build-metadata.json.
#
# This is the DEFINITION of the record; QA (Q8) runs it against the integrated
# candidate. It intentionally records inputs only — it never claims the produced
# binaries have been runtime-verified (that is QA's integrated evidence).
set -euo pipefail
cd "$(dirname "$0")/../.."
dist="${1:-dist}"
mkdir -p "$dist"

# Environment/toolchain identity.
go_version="$(go version | awk '{print $3}')"
gotoolchain="$(go env GOTOOLCHAIN)"
git_sha="$(git rev-parse HEAD 2>/dev/null || echo unknown)"
git_desc="$(git describe --tags --always --dirty 2>/dev/null || echo unknown)"
source_epoch="$(git show -s --format=%ct HEAD 2>/dev/null || echo 0)"
gomod_sha="$(shasum -a 256 go.mod 2>/dev/null | awk '{print $1}' || sha256sum go.mod | awk '{print $1}')"
gosum_sha="$(shasum -a 256 go.sum 2>/dev/null | awk '{print $1}' || sha256sum go.sum | awk '{print $1}')"

cat >"$dist/build-metadata.json" <<JSON
{
  "schema": "amux.build_metadata.v1",
  "git_commit": "$git_sha",
  "git_describe": "$git_desc",
  "source_date_epoch": $source_epoch,
  "go_version": "$go_version",
  "gotoolchain": "$gotoolchain",
  "cgo_enabled": "0",
  "target_os": "linux",
  "target_arches": ["amd64", "arm64"],
  "build_flags": ["-trimpath", "-s", "-w"],
  "go_mod_sha256": "$gomod_sha",
  "go_sum_sha256": "$gosum_sha",
  "goreleaser_config": "packaging/goreleaser/goreleaser.yaml",
  "reproduce": "checkout git_commit, export GOTOOLCHAIN=$gotoolchain CGO_ENABLED=0, run 'make release-snapshot'; compare dist/checksums.txt"
}
JSON

echo "wrote $dist/build-metadata.json"
cat "$dist/build-metadata.json"
