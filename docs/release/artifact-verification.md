# Artifact verification (D6)

T3 devops · work package D6. How to verify a release candidate **without
trusting the builder**. Every check exits non-zero on mismatch — there is no
soft pass. `scripts/release/verify-artifacts.sh` bundles checks 1–3; QA (Q8)
additionally runs check 4 against the integrated candidate.

## The artifact set

For each `linux/{amd64,arm64}` archive (`CGO_ENABLED=0`, glibc):

| File | Produced by | Proves |
|---|---|---|
| `amux_<ver>_linux_<arch>.tar.gz` | GoReleaser | the release payload |
| `checksums.txt` | GoReleaser (`sha256`) | archive integrity |
| `..._<arch>.tar.gz.sbom.cdx.json` | syft (CycloneDX) | dependency inventory |
| `build-metadata.json` | `record-provenance.sh` | reproducible build inputs |
| provenance attestation *(opt-in)* | `actions/attest-build-provenance` | build origin |

## 1 · Checksums recompute

```bash
cd dist
sha256sum -c checksums.txt        # or: shasum -a 256 -c checksums.txt (macOS)
```

Every archive listed must recompute to its recorded digest. Any mismatch = a
tampered or corrupt artifact; stop.

## 2 · Every archive has an SBOM

```bash
for a in amux_*_linux_*.tar.gz; do
  test -f "$a.sbom.cdx.json" && echo "SBOM ok: $a" || { echo "MISSING SBOM: $a"; exit 1; }
done
```

Inspect an SBOM's component list (jq optional):

```bash
jq '.components[].name' amux_<ver>_linux_amd64.tar.gz.sbom.cdx.json
```

The component set must match the frozen module manifest (`docs/dependencies.md`,
enforced in CI by `scripts/check-deps-manifest.sh`). A component that is not in
the manifest is an un-evidenced dependency edge — investigate before trusting.

## 3 · Reproducible build-input record

`build-metadata.json` records the exact inputs (git commit + describe,
`source_date_epoch`, Go version, `GOTOOLCHAIN`, `CGO_ENABLED=0`, target arches,
build flags, `go.mod`/`go.sum` SHA-256, GoReleaser config path). To reproduce
the binaries bit-for-bit:

```bash
git checkout <git_commit-from-build-metadata.json>
export GOTOOLCHAIN=<gotoolchain>  CGO_ENABLED=0
make release-snapshot
diff <(sort dist/checksums.txt) <(sort <original>/checksums.txt)
```

Reproducibility rests on three things pinned in `goreleaser.yaml`: `-trimpath`,
`mod_timestamp={{ .CommitTimestamp }}` (binary mtimes), and an explicit
`info.mtime={{ .CommitDate }}` on **every non-binary archive member**. The third
is load-bearing and was added only after being measured: `-trimpath` +
`mod_timestamp` alone make the four binaries byte-identical but leave the tar
headers carrying wall-clock mtimes, because the before-hook regenerates the
shell completions on every run. Two builds of the same commit therefore produced
different tarball digests. See
`.amux-artifacts/devops-t6/release-frozen-20260722/repro-double-build-arm64.BEFORE-mtime-fix.log`
for the divergence and `repro-double-build-arm64.log` for the fixed run.

This is a claim with a gate behind it, not an assertion — run it yourself:

```bash
scripts/release/linux-repro-check.sh arm64 .amux-artifacts/devops-t6/<stamp>
# builds the frozen snapshot twice in one Linux container and diffs the
# artifact digests; non-zero exit on any divergence
```

A checksum divergence means an input drifted — chase it before release.

## 4 · Provenance attestation (QA, integrated candidate)

Only generated when the operator opts in (`publish_attestation: true`). Verify
with the GitHub CLI:

```bash
gh attestation verify amux_<ver>_linux_amd64.tar.gz \
  --repo amux-run/amux
```

This confirms the archive was built by the expected workflow from the expected
source. `verify-artifacts.sh` notes whether a bundle is expected but does not
itself run `gh` (that is QA's integrated step, needing the repo/network context).

## One-shot local verification

```bash
make release-verify        # -> scripts/release/verify-artifacts.sh dist
# checks 1–3; prints a note for check 4 (verified by QA with gh attestation verify)
```

## Deferrals (honest)

- Checks 1–3 run anywhere `sha256sum`/`shasum` exist (macOS or Linux).
- The **reproducible rebuild** (§3) needs the pinned GoReleaser binary + `syft`
  (`make release-tools`); the linux tarballs cross-compile (`CGO_ENABLED=0`) so a
  dev macOS host with the pin on PATH reproduces the full artifact set. The
  release workflow remains the authority for hosted-CI evidence.
- The **attestation** (§4) exists only for opt-in, workflow-built candidates and
  is verified by QA against the integrated release.
