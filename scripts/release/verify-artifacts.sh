#!/usr/bin/env bash
# Release-artifact verification (T3 devops, D3). Verifies the integrity of a
# built dist/ WITHOUT trusting the builder: checksums recompute, every archive
# has an SBOM, and the build-input record is present. QA (Q8) runs this against
# the integrated candidate and additionally verifies the provenance attestation
# with `gh attestation verify` (see docs/release/artifact-verification.md).
#
# Exit non-zero on any missing/mismatched artifact — never a soft pass.
set -euo pipefail
dist="${1:-dist}"
[ -d "$dist" ] || { echo "verify: no dist dir at '$dist'"; exit 1; }
cd "$dist"

sha() { if command -v sha256sum >/dev/null; then sha256sum "$@"; else shasum -a 256 "$@"; fi; }

fail=0

# 1. Checksums recompute for every listed archive.
if [ -f checksums.txt ]; then
	if sha -c checksums.txt; then
		echo "verify: checksums OK"
	else
		echo "verify: CHECKSUM MISMATCH"; fail=1
	fi
else
	echo "verify: missing checksums.txt"; fail=1
fi

# 2. Every tarball has a matching SBOM.
shopt -s nullglob
archives=( amux_*_linux_*.tar.gz )
if [ ${#archives[@]} -eq 0 ]; then echo "verify: no archives found"; fail=1; fi
for a in "${archives[@]}"; do
	if [ -f "$a.sbom.cdx.json" ] || ls "$a".sbom.* >/dev/null 2>&1; then
		echo "verify: SBOM present for $a"
	else
		echo "verify: MISSING SBOM for $a"; fail=1
	fi
done

# 3. Reproducible build-input record present.
if [ -f build-metadata.json ]; then
	echo "verify: build-metadata.json present"
else
	echo "verify: MISSING build-metadata.json (run scripts/release/record-provenance.sh)"; fail=1
fi

# 4. Provenance attestation is verified with `gh attestation verify` by QA
#    against the integrated candidate; this script flags whether one is expected.
[ -f provenance.intoto.jsonl ] && echo "verify: provenance attestation bundle present" \
	|| echo "verify: note — provenance is attached out-of-band by release.yml; verify with 'gh attestation verify'"

exit $fail
