#!/usr/bin/env bash
# Generate shell completions from the amux CLI (T3 devops, D1/D4).
#
# Completions are a GENERATED artifact: the Cobra command tree is authoritative,
# so we build the CLI and let it emit its own completions rather than hand-
# maintaining them. Both the generate-check gate and the release/packaging steps
# call this, so the completions shipped in a package are byte-identical to what
# the current binary produces.
#
# Usage: scripts/gen-completions.sh [OUT_DIR]   (default: ./completions)
set -euo pipefail
cd "$(dirname "$0")/.."

out="${1:-completions}"
mkdir -p "$out"

# Build once into a temp binary so we do not depend on it being on PATH.
bin="$(mktemp -d)/amux"
CGO_ENABLED=0 go build -o "$bin" ./cmd/amux

for sh in bash zsh fish; do
	"$bin" completion "$sh" >"$out/amux.$sh"
done
echo "completions written to $out/ (bash, zsh, fish)"
