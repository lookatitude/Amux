Adopt `.guild/agents/devops.md` and reopen T3-devops for the release-pipeline
findings validated by the mandatory T6 G-lane review at
`.guild/runs/run-8c2926bb-2ebe-4166-8b77-84c0de3e12c0/review/G-lane:T6-qa/result-1.json`.
This is a contained tooling/CI repair suitable for Terra/Opus. Work
autonomously and do not publish, commit, tag, or push.

Close these findings:

1. Make the checked-in GoReleaser config coherent with one explicitly pinned
   GoReleaser version. The current v2.5.1 cannot parse `ids`/`formats`,
   `default_file_info` is invalid, and the before hook writes completions into
   `dist/` before a clean build. Select a currently compatible pinned v2
   release (the diagnostic already proved v2.12.7), update `scripts/tools.env`
   and dependency/toolchain documentation, use valid archive keys (`info`),
   stage completion inputs outside the cleaned output tree, and keep publishing
   disabled. `make release-check`, `make release-snapshot`, artifact
   verification, SBOM/checksum/provenance generation, and installed tarball
   smoke must use that exact pin and pass for Linux amd64+arm64.

2. Export an owner-only TMPDIR in every Linux CI/release/nightly job before any
   Go test or daemon command, because the socket hardening correctly rejects a
   chain below world-writable `/tmp`. Add a reusable fail-closed setup/verify
   step and cover Arch/Ubuntu amd64/arm64 workflows. Preserve honest labels:
   cross-build is not runtime evidence and locally linting YAML is not hosted
   CI evidence.

3. Add narrow repository hygiene rules for generated/local artifacts:
   `/amux`, `/.amux-artifacts/`, `/.tools/`, package-relative
   `**/.amux-artifacts/`, release output, and OS/editor residue. Do not ignore
   `.guild/` globally because it contains the canonical approved spec, plan,
   contexts, and receipts. Remove only generated nested QA residue or binaries;
   do not delete canonical Guild evidence. Update secrets-scan policy only for
   reviewed deliberate fixtures, never to hide real findings.

4. Correct QA/release scripts or documentation so a Darwin race run cannot be
   recorded as satisfying the frozen Linux-CI `race-full-suite` prerequisite.
   If practical, execute a real Linux race suite in the Arch or Ubuntu
   container harness and emit a truthful receipt; otherwise mark it unproven.

Run YAML/config lint, pinned release-check/snapshot/verify, tarball install smoke
in Arch amd64 + Ubuntu amd64 + Ubuntu arm64, shell checks, full verify/tests,
and scope audit. Do not change backend replay, trust identity, TUI, product
spec, AUR placeholder publication data, or claim hosted CI/reference-profile
evidence. Replace
`.guild/runs/run-8c2926bb-2ebe-4166-8b77-84c0de3e12c0/handoffs/devops-T3-devops.md`
with an exact receipt and emit exactly one valid `guild.handoff.v2` object.
