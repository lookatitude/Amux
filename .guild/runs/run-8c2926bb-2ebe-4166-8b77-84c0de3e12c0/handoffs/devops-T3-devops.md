---
schema_version: guild.handoff_receipt.v1
task_id: T3-devops
run_id: run-8c2926bb-2ebe-4166-8b77-84c0de3e12c0
task_run_id: trun-008
specialist: devops
tier: mid
status: done
completed_at: 2026-07-17
resume: true
rework_round: 2
retry_attempt: 1
host:
  requested: claude
  selected: claude-code-cli
  degraded: false
  independence: weak
---

# T3-devops handoff receipt — G-lane (T6-qa) release-pipeline rework (2026-07-17)

This receipt replaces the prior T3-devops receipt after the mandatory T6 G-lane
review (`review/G-lane:T6-qa/result-1.json`, verdict `issues`) reopened T3 for
the **release-pipeline** findings it owns. Scope was exactly the four devops
findings in the fix brief — nothing else. All four are closed with executable
config and real Linux-container evidence; publishing stayed disabled and nothing
was committed, tagged, or pushed.

The review's other blockers are explicitly **not** T3's and were not touched:
F1 ReplayRead/MaxBytes and F2 overlayfs identity (backend), F5 trust-matrix
`-run` binding (security), F4 `amux --version` (already wired by the CLI lane —
verified `amux --version` prints the stamped identity). Concurrent backend/
security/QA lanes wrote `cmd/**`, `internal/**`, `docs/security/**`, and
`docs/testing/strategy.md` in parallel; T3 neither authored those nor used them
as design inputs.

## Findings closed

### Finding 1 — GoReleaser config coherent with one explicitly pinned version

- **Pin bumped to a currently-compatible v2** in `scripts/tools.env`:
  `GORELEASER_VERSION=v2.5.1 → v2.12.7` (the diagnostic-proven pin). Added a
  coherence note: the archive `ids`/`formats` keys require GoReleaser ≥ 2.6, so
  config and pin now move together.
- **Config made parseable + valid** (`packaging/goreleaser/goreleaser.yaml`):
  `default_file_info: { mode: 0644 }` → `info: { mode: 0644 }` (the real
  per-file archive key; invalid in every version) at both archive `files`
  entries; `ids`/`formats` kept (valid at the new pin).
- **Completion inputs staged OUTSIDE the cleaned output tree**: the before-hook
  now writes `build/completions` (was `dist/completions`), and the archive
  `files` glob reads `build/completions/*`. `release --clean` wipes `dist/` at
  setup, so staging there was the failure; `build/` is a clean staging tree
  (added to `make clean` and `.gitignore`).
- **Publishing stays disabled**: `release.disable: true` + `--snapshot`
  unchanged. No credentials, no external push.
- **Pin enforced fail-closed**: new `make release-toolcheck` (prereq of
  `release-check`/`release-snapshot`) refuses to run unless the `goreleaser` on
  PATH is exactly the pin and `syft` is present. New `make release-tools`
  installs the pinned goreleaser+syft into `./.tools/bin`. `release.yml` now also
  installs the pinned `syft` (the SBOM generator GoReleaser invokes).
- **Docs**: `docs/dependencies.md` (pinned versions table row + syft row),
  `docs/release/versioning-and-release.md` and
  `docs/release/artifact-verification.md` (local dry-run now reproduces the full
  artifact set with the pin; hosted CI remains the matrix authority — no
  hosted-CI claim).

Proof (pinned tool, this host): `make release-check` ✓; `make release-snapshot`
built both `amux_*_linux_{amd64,arm64}.tar.gz` + per-archive CycloneDX SBOMs
(syft) + `checksums.txt`; `scripts/release/record-provenance.sh` wrote
`build-metadata.json`; `make release-verify` recomputed checksums OK, SBOM +
metadata present. **Installed tarball smoke PASS in all three targets** — Arch
amd64, Ubuntu amd64, Ubuntu arm64 containers: both binaries executable, `amux
--version`/`amuxd --version` print the stamped identity, static/cgo-free linkage
proven, completions (bash/zsh/fish) present.

### Finding 2 — owner-only TMPDIR in every Linux CI/release/nightly job

STR-3 rejects any socket path with a group/other-writable ancestor, and default
`TMPDIR=/tmp` is mode 1777 — so socket-binding tests fail closed there.

- **Reusable fail-closed setup+verify step**: `scripts/ci/owner-tmpdir.sh`
  creates a 0700 dir under an owner-safe base (never `/tmp`/`/var/tmp`), verifies
  the whole ancestor chain is not group/other-writable (mask 0022, GNU+BSD
  `stat`), proves a real unix socket binds under it, and exports `TMPDIR` to
  `$GITHUB_ENV`. It exits non-zero rather than fall back to a world-writable
  tree. Wrapped as composite action `.<HIGH_ENTROPY_REDACTED>-tmpdir`.
- **Wired before any go-test/daemon command** in every Linux job across the
  supported matrix: `ci.yml` gate + `test-{ubuntu,arch}-{amd64,arm64}`;
  `release.yml` gate + `soak-30m`; `nightly-soak.yml` soak-8h.
- **Honest labels preserved**: the compile-only `cross-build` job was left
  without the step (it runs no test/daemon and is not runtime evidence); no
  local container run is labelled hosted-CI evidence.

Proof (ubuntu:24.04 container): with `TMPDIR=/tmp`, 10 `internal/transport/local`
socket tests FAIL with "unsafe permissions" (STR-3 rejecting the 1777 chain);
with the script's owner-only TMPDIR the package passes (`ok`). Script verified in
Arch + Ubuntu containers (root `HOME=/root`) and on the darwin host; shellcheck
clean.

### Finding 3 — narrow repository hygiene

- New `.gitignore` (narrow, generated/local only): `/amux`, `/amuxd`,
  `/.amux-artifacts/`, package-relative `**/.amux-artifacts/`, `/.tools/`,
  release output (`/dist/`, `/build/`, `/coverage.out`), and OS/editor residue
  (`.DS_Store`, `Thumbs.db`, `*.swp`, `*.swo`, `*~`). **`.guild/` is
  deliberately NOT ignored** — verified via `git check-ignore` that the spec,
  plan, contexts, and receipts remain tracked while every generated tree is
  ignored (root and nested).
- **Removed only generated residue/binaries**: deleted the stray root `amux`
  (19 MiB Linux ELF build output F6 flagged). Left all canonical Guild evidence
  and the QA/security `.amux-artifacts/**` logs the T6 review cites intact —
  now gitignored, so they cannot become history noise without being destroyed.
- **Secrets policy unchanged**: no new deliberate fixtures were introduced, so
  `.gitleaks.toml` was not loosened (never widen an allowlist to hide findings).

### Finding 4 — a Darwin race run cannot satisfy the Linux-CI `race-full-suite`

The prior `race-full-suite.receipt.json` recorded `outcome: pass` from a
`host_os: darwin` run, though the manifest freezes the check to the Linux CI
matrix.

- **Fail-closed emitter** `scripts/ci/race-full-suite-receipt.sh`: on any
  non-linux host it records `deferred_prerequisite` and exits 3 — it can never
  write `pass` off-Linux. Demonstrated on this darwin host (exit 3,
  `deferred_prerequisite`; proof kept at
  `.amux-artifacts/devops-t6/race/darwin-refused.receipt.json`).
- **Real Linux race suite executed** in the ubuntu:24.04 aarch64 container
  harness (CGO_ENABLED=1 — the race detector needs cgo — under the STR-3
  owner-only TMPDIR). The emitter wrote a **truthful** receipt: `host_os:
  linux`, `outcome: fail`, exit 1. The failure is deterministic 2/2 full-suite
  runs in `internal/daemon <HIGH_ENTROPY_REDACTED>`
  (`engine_test.go:431` — `replay_gap` structured detail not surfaced under
  load). The same test PASSES 2/2 in isolation on Linux and PASSES on darwin, so
  it is a full-suite/load-sensitive **backend replay defect** (review F1 /
  strategy F-3) — precisely the Linux-only failure the darwin "pass" was
  masking. Fixing it is backend/QA and explicitly out of this lane's scope.
- **Contained CI repair discovered while executing the real suite**: `make race`
  inherited the global `CGO_ENABLED=0`, so `go test -race` errored
  `-race requires cgo` on Linux (works on darwin). Set `CGO_ENABLED=1` on the
  `race` target only (test-only; product builds stay CGO-free) and added `gcc`
  to the Arch amd64 race lane prerequisites so the Linux race lane is runnable.

## changed_files (this rework round — devops surfaces only)

- `scripts/tools.env` (edited) — GoReleaser pin v2.5.1 → v2.12.7 + coherence note.
- `packaging/goreleaser/goreleaser.yaml` (edited) — `info:` key; completions
  staged in `build/`; pin-coherence header.
- `Makefile` (edited) — `release-tools`/`release-toolcheck` (fail-closed pin
  gate) on `release-check`/`release-snapshot`; `race` target cgo override;
  `clean` also removes `build/`.
- `.github/workflows/ci.yml` (edited) — owner-tmpdir step on gate + all four
  `test-*` jobs; `gcc` on the Arch amd64 race lane.
- `.<HIGH_ENTROPY_REDACTED>.yml` (edited) — owner-tmpdir on gate + soak-30m;
  pinned `syft` install for the SBOM step.
- `.<HIGH_ENTROPY_REDACTED>-soak.yml` (edited) — owner-tmpdir on soak-8h.
- `.<HIGH_ENTROPY_REDACTED>-tmpdir/action.yml` (new) — reusable composite step.
- `scripts/ci/owner-tmpdir.sh` (new) — fail-closed owner-only TMPDIR setup/verify.
- `scripts/ci/race-full-suite-receipt.sh` (new) — host-gated fail-closed race
  receipt emitter.
- `.gitignore` (new) — narrow generated/local rules; `.guild/` NOT ignored.
- `docs/dependencies.md`, `docs/release/versioning-and-release.md`,
  `docs/release/artifact-verification.md` (edited) — pinned versions + local
  reproduce notes (no hosted-CI claims).
- Removed: stray root `amux` binary.
- Regenerated evidence (gitignored): `.amux-<HIGH_ENTROPY_REDACTED>-full-suite.*`
  (truthful Linux receipt) and `.amux-artifacts/devops-t6/**` (this round's logs).
- This receipt file (final filesystem action).

No forbidden surface was written: no `cmd/**`, `internal/**`, `api/**`,
`docs/security/**`, `docs/adr/**`, `packaging/aur/**`, `.guild/spec/**`, TUI, or
`PRODUCT.md`. Backend replay, trust identity, and the AUR placeholder
publication data are unchanged.

## validation run (2026-07-17)

- YAML parse (python3-yaml, container): ci/release/nightly workflows + composite
  action + goreleaser.yaml + Taskfile.yml — all ok.
- shellcheck (container): `owner-tmpdir.sh`, `race-full-suite-receipt.sh` clean
  (`-S style`). actionlint: only pre-existing SC2015/SC2034 style nits on the
  legacy `for i in 1 2 3; do go mod download` loops — none on any T3-added line.
- Pinned release path: `make release-check` ✓, `make release-snapshot` ✓ (both
  arch tarballs + SBOMs + checksums), `record-provenance.sh` ✓,
  `make release-verify` ✓ — using goreleaser v2.12.7 + syft v1.18.1.
- Install-tarball smoke: PASS in Arch amd64 + Ubuntu amd64 + Ubuntu arm64.
- TMPDIR: negative control (`/tmp` → 10 STR-3 rejections) + positive (owner-only
  → pass) in ubuntu:24.04.
- `make verify` rc=0 (<HIGH_ENTROPY_REDACTED>
  smoke-selftest); `make test` rc=0 on the darwin host (0 FAIL).
- Scope audit: T3 write-set is the devops surfaces above + the F4 receipt only.

## honest deferrals / not-claimed

- **Hosted-CI evidence is NOT claimed.** All runs above are local darwin-host +
  local Docker container harness. The GitHub-hosted matrix, reference-profile
  numbers, 8-hour soak, and AUR clean-chroot remain hosted/backend gates.
- **Cross-compile is not runtime evidence** — the compile-only `cross-build`
  job is unchanged and unlabelled as runtime.
- `race-full-suite` is a truthful **FAIL** blocked on a backend replay defect,
  not a devops issue — recorded, not hidden, not made green.

## followups

- Fresh checksum-bound G-lane review is mandatory before downstream dispatch.
- **Backend**: fix `internal/daemon` ReplayRead so `replay_gap` structured detail
  is surfaced under full-suite/concurrent load (review F1 / strategy F-3) — this
  is what keeps `race-full-suite` red on Linux. Then re-run
  `scripts/ci/race-full-suite-receipt.sh` on Linux for a green receipt.
- **QA**: update `docs/testing/strategy.md` §11 finding #4 (F-4 release pipeline)
  to closed and refresh the `race-full-suite` note now that darwin cannot count.
- **Security**: the frozen `trust-matrix-replay` `-run` binding (F5) is theirs.
- Publishing (GitHub Release, AUR push, credential-backed signing) remains a
  separate, explicitly user-authorized operation — not performed here.

## learnings

- A tool pin and the config it parses are one unit: bumping GoReleaser to
  v2.12.7 and moving completions out of the `--clean`-wiped `dist/` had to land
  together, and a fail-closed `release-toolcheck` makes "uses the exact pin"
  provable instead of hoped.
- STR-3's world-writable rejection is real and load-bearing: proving it (10
  failures under `/tmp`, green under a 0700 owner-only TMPDIR) turned an
  environment footnote into a verified, reusable fail-closed CI step.
- A green receipt from the wrong host hides a real defect. Running the frozen
  `race-full-suite` on actual Linux flipped a darwin "pass" into a truthful fail
  that surfaced a backend replay bug the host masked — the fix was a host-gated
  emitter, not a re-run on the convenient platform.

```guild.handoff.v2
{
  "schema_version": "guild.handoff.v2",
  "task_id": "T3-devops",
  "tier": "mid",
  "status": "done",
  "summary": "G-lane (T6-qa) round-2 rework: closed all four devops release-pipeline findings with executable config + real Linux-container evidence; nothing published, committed, tagged, or pushed. F1 (GoReleaser coherence): pinned GORELEASER_VERSION v2.5.1->v2.12.7 in scripts/tools.env (config+pin move together: ids/formats need >=2.6); fixed goreleaser.yaml default_file_info->info (valid archive key) at both files entries; staged completions in build/ OUTSIDE the --clean-wiped dist/ (before-hook + archive glob); kept release.disable:true/--snapshot (publishing off); added make release-tools + fail-closed make release-toolcheck (refuses to run unless goreleaser==pin and syft present) as prereqs of release-check/release-snapshot; release.yml now installs pinned syft; updated docs/dependencies.md + docs/release/{versioning-and-release,artifact-verification}.md. Pinned path green on this host: release-check, release-snapshot (both linux amd64+arm64 tarballs + CycloneDX SBOMs + checksums), record-provenance (build-metadata.json), release-verify; install tarball smoke PASS in Arch amd64 + Ubuntu amd64 + Ubuntu arm64 containers (amux --version wired by CLI lane, static cgo-free linkage, completions present). F2 (owner-only TMPDIR): new scripts/ci/owner-tmpdir.sh (0700 dir, ancestor chain g/o-non-writable verify, unix-socket bind proof, exports TMPDIR to GITHUB_ENV, fail-closed never falls back to /tmp) wrapped as composite action .<HIGH_ENTROPY_REDACTED>-tmpdir; wired before every go-test/daemon command in ci.yml gate + test-{ubuntu,arch}-{amd64,arm64}, release.yml gate + soak-30m, nightly-soak soak-8h; cross-build left compile-only (not runtime evidence). Proof: TMPDIR=/tmp -> 10 STR-3 socket-test rejections; owner-only TMPDIR -> pass. F3 (hygiene): new narrow .gitignore (/amux,/amuxd,/.amux-artifacts/,**/.amux-artifacts/,/.tools/,/dist/,/build/,/coverage.out,OS/editor residue); .guild/ deliberately NOT ignored (verified tracked via git check-ignore); removed stray 19MiB root amux ELF binary; kept all canonical Guild + QA evidence; .gitleaks.toml unchanged (no new fixtures, never loosen to hide findings). F4 (race-full-suite host truth): new fail-closed scripts/ci/race-full-suite-receipt.sh records deferred_prerequisite + exit 3 on any non-linux host (proven on darwin) so a darwin run can never be recorded as pass; executed the real suite in ubuntu:24.04 aarch64 (CGO_ENABLED=1, owner-only TMPDIR) and emitted a truthful receipt host_os=linux outcome=fail: internal/daemon <HIGH_ENTROPY_REDACTED> fails 2/2 full-suite (passes 2/2 isolated on Linux, passes on darwin) = a backend replay defect (review F1 / strategy F-3) the darwin pass had masked, out of devops scope; also fixed make race CGO_ENABLED=1 (race detector needs cgo; product builds stay cgo-free) and added gcc to the Arch amd64 race lane. Validation: YAML parse all clean; shellcheck clean on both new scripts; make verify rc=0; make test rc=0 on host; scope audit confirms only devops surfaces + the F4 receipt were written. No forbidden surface touched (cmd/**, internal/**, api/**, docs/security/**, docs/adr/**, packaging/aur/**, .guild/spec/**, TUI, PRODUCT.md); backend replay, trust identity, AUR placeholder data unchanged. No hosted-CI or reference-profile evidence claimed — all evidence is local darwin host + local Docker containers. race-full-suite remains a truthful FAIL blocked on a backend fix, not a devops issue. Fresh checksum-bound G-lane review required before downstream dispatch.",
  "artifacts": [
    "scripts/tools.env",
    "packaging/goreleaser/goreleaser.yaml",
    "Makefile",
    ".github/workflows/ci.yml",
    ".<HIGH_ENTROPY_REDACTED>.yml",
    ".<HIGH_ENTROPY_REDACTED>-soak.yml",
    ".<HIGH_ENTROPY_REDACTED>-tmpdir/action.yml",
    "scripts/ci/owner-tmpdir.sh",
    "scripts/ci/race-full-suite-receipt.sh",
    ".gitignore",
    "docs/dependencies.md",
    "docs/release/versioning-and-release.md",
    "docs/release/artifact-verification.md",
    ".amux-<HIGH_ENTROPY_REDACTED>-full-suite.receipt.json",
    ".amux-<HIGH_ENTROPY_REDACTED>-full-suite.txt",
    ".guild/runs/run-8c2926bb-2ebe-4166-8b77-<HIGH_ENTROPY_REDACTED>-T3-devops.md"
  ],
  "issues": [
    "race-full-suite is a truthful FAIL on Linux: internal/daemon <HIGH_ENTROPY_REDACTED> (replay_gap structured detail not surfaced under full-suite load) — a backend replay defect (review F1 / strategy F-3), out of devops scope, must be fixed by backend before the check can go green."
  ],
  "learnings": [
    "A tool pin and the config it parses are one unit: bumping GoReleaser to v2.12.7 and moving completions out of the --clean-wiped dist/ had to land together, and a fail-closed release-toolcheck makes 'uses the exact pin' provable rather than hoped.",
    "STR-3's world-writable rejection is real and load-bearing — proving it (10 failures under /tmp, green under a 0700 owner-only TMPDIR) turned an environment footnote into a verified, reusable fail-closed CI step.",
    "A green receipt from the wrong host hides a real defect: running the frozen race-full-suite on actual Linux flipped a darwin 'pass' into a truthful fail that exposed a backend replay bug the host masked; the fix was a host-gated emitter, not a re-run on the convenient platform."
  ],
  "notes": "Round-2 G-lane rework, exactly the four devops release-pipeline findings from review/G-lane:T6-qa/result-1.json (F3 goreleaser, F6 TMPDIR + gitignore + race-full-suite darwin overclaim). Backend (F1/F2), security (F5), and CLI (F4 amux --version, already wired) findings were not in scope and not touched. All evidence is local: darwin author host + local Docker container harness (Arch amd64, Ubuntu amd64/arm64) — no hosted-CI or reference-profile claim. Did not publish/commit/tag/push. Fresh checksum-bound G-lane round mandatory before downstream dispatch.",
  "injection_clean": "clean"
}
```
