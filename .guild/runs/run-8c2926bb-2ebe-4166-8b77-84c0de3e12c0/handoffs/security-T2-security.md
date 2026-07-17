---
schema_version: guild.handoff_receipt.v1
task_id: T2-security
run_id: run-8c2926bb-2ebe-4166-8b77-84c0de3e12c0
task_run_id: trun-017
retry_attempt: 0
specialist: security
tier: powerful
status: done
completed_at: 2026-07-17
resume: false
rework_round: 1
host:
  selected: claude-code-cli
  degraded: false
  independence: weak
---

# T2-security handoff receipt — G-lane reopen (2026-07-17)

T2-security was reopened for the two release-blocking findings the mandatory
T6 G-lane review (`review/G-lane:T6-qa/result-1.json`) assigned to this lane:
**F2** (overlayfs reuses `(st_dev, st_ino)` across `rm -rf root && mkdir
root`, so the frozen durable key — and any trust bound to it — survived root
replacement) and **F5** (the frozen readiness manifest's `trust-matrix-replay`
`-run` pattern bound zero tests, and `integration-second-uid` could be
satisfied by an always-skipping stub). Both are closed with production-path
fixes, TDD-first, inside the frozen specification: **the public durable key
definition (SHA-256 of canonical realpath + dev + ino) is byte-for-byte
unchanged**, and no ask-gate was needed.

## What closed F2 (replacement validation)

A separately persisted replacement-validation discriminator, additive beside
the frozen key (HA-2e amendment, `docs/security/hook-authorization.md` §1):

- `internal/platform/validation.go` + `validation_linux.go` /
  `validation_darwin.go` / `validation_other.go` — new
  `platform.<HIGH_ENTROPY_REDACTED>` capability, OUTSIDE the 13 frozen ADR-0006
  seams (seam-freeze tests unchanged and green). Linux derives the value from
  `statx(2)` birth time (`STATX_BTIME`) — a kernel identity surface that
  changes exactly when the object at a path is recreated (even when the inode
  number is reused) and never under child create/write/remove. Darwin (author
  host) uses `st_birthtimespec`. Explicit fail-closed capability: masked-out
  BTIME, ENOSYS, zero birth time, or an unsupported platform returns
  `<HIGH_ENTROPY_REDACTED>` — trust reuse is then DENIED (typed
  `project_trust_required`, audited), never guessed. Surface selection
  rationale (btime over `stx_mnt_id` and `FS_IOC_GETVERSION`) and the
  conservative overlayfs first-copy-up edge are documented in the file and in
  HA-2e.
- `internal/control` — `ProjectRecord.Validation`; `Deps.Validator`;
  `RegisterProject` recomputes the discriminator on every call and on
  mismatch (or an ABSENT persisted value) invalidates approved trust as a
  system revocation: monotonic epoch bump, durable write-through, grants
  deactivated, `project_revoked` audit record carrying the
  `project_trust_required` code (distinguishable from an operator revoke),
  revoke listeners notified (containment preserved). `TrustStore` gained
  `LoadProject`/`SaveValidation`: persisted trust now REHYDRATES into the
  actor after a restart only through this revalidation path — this also fixed
  a latent restart defect where a post-restart re-approval collided with the
  store's epoch-monotonicity gate.
- `internal/store` — forward-only migration v2 adds
  `projects.validation_scheme/validation_value` (empty default = ambiguous ⇒
  deny reuse on first post-upgrade registration); `UpsertProject` persists
  the discriminator; `<HIGH_ENTROPY_REDACTED>` added.
- `internal/daemon/truststore.go` — adapter passes the discriminator through
  and implements `LoadProject`/`SaveValidation` (pure translation; no policy).
- `internal/hooks/runtime.go` — the pre-launch root recheck
  (`rootIdentityMatches`, HA-14) now requires tuple match AND discriminator
  match, fail-closed on resolution errors.

## What closed F5 (manifest gate binding)

- `internal/hooks/trustmatrix_integration_test.go` (new, `-tags
  integration`) — `<HIGH_ENTROPY_REDACTED>`: all 41 golden rows
  against the real integrated trust engine (production control actor + SQLite
  trust store via `daemon.NewTrustStore` + hook runtime + real project
  dirs/files + spy launcher). The unit-level `<HIGH_ENTROPY_REDACTED>`
  was NOT relabeled; rows whose condition needs privileged mounts or
  not-yet-integrated product surface drive the real `AuthorizeLaunch`
  linearization point with the single deviated fact injected — the driver
  class and reason are recorded per row in the test.
- `internal/transport/local/local_test.go` — the always-skipping
  `<HIGH_ENTROPY_REDACTED>` stub is DELETED (comments updated to point
  at the real `seconduid_integration_test.go` harness).
- `internal/securitytest/gatebind.go` + `gatebind_test.go` (new) —
  deterministic self-gate: `<HIGH_ENTROPY_REDACTED>` parses
  every manifest command, statically enumerates the test functions each
  `-run` pattern binds under its build tags on GOOS=linux, and fails the
  blocking `security-contract-self-gates` check on zero bindings.
  `<HIGH_ENTROPY_REDACTED>` pins that untagged `SecondUID`
  binds to nothing and the tagged harness keeps ≥4 cases.
- `docs/security/readiness-manifest.json` — `trust-matrix-replay`
  <HIGH_ENTROPY_REDACTED> now truthful (command pattern unchanged and now
  binding); `security-contract-self-gates` description names the binding
  self-gate.
- `docs/security/security-readiness.md` §6 — receipt binding/skip rules: a
  `pass` receipt must show ≥1 substantive execution and zero undeclared
  skips; declared-prerequisite skips are `deferred_prerequisite`, never
  `pass`; receipt commands must be byte-identical to manifest commands.

## changed_files

New: `internal/platform/validation.go` (99), `validation_linux.go` (44),
`validation_darwin.go` (37), `validation_other.go` (19), `validation_test.go`
(107); `internal/daemon/truststore_test.go` (215);
`internal/hooks/validation_test.go` (154),
`internal/hooks/trustmatrix_integration_test.go` (587);
`internal/securitytest/gatebind.go` (196), `gatebind_test.go` (84);
`.amux-<HIGH_ENTROPY_REDACTED>-gates-20260717/`,
`linux-focused-20260717/`, `linux-focused-arch-20260717/`,
`linux-integration-20260717/`, `linux-integration-arch-20260717/` (container
gate logs); this receipt.

Modified: `internal/control/actor.go`, `internal/control/store.go`,
`internal/control/actor_test.go` (incl. `<HIGH_ENTROPY_REDACTED>`
now asserting both the fresh-inode and inode-reuse branches, plus
deterministic reuse tests `<HIGH_ENTROPY_REDACTED>`,
`<HIGH_ENTROPY_REDACTED>`,
`<HIGH_ENTROPY_REDACTED>`); `internal/store/migrations.go`,
`projects.go`, `projects_test.go`, `store_test.go`;
`internal/daemon/truststore.go`; `internal/hooks/runtime.go`;
`internal/transport/local/local_test.go`;
`docs/security/hook-authorization.md` (HA-2e),
`docs/security/security-readiness.md` (§6),
`docs/security/readiness-manifest.json`,
`docs/security/reviews/release-candidate.md` (F-1, F-2 → CLOSED with cited
evidence), `docs/testing/strategy.md` (§11 F-1/F-2 → RESOLVED);
`.amux-<HIGH_ENTROPY_REDACTED>-matrix-replay.{txt,receipt.json}`,
`.amux-<HIGH_ENTROPY_REDACTED>-contract-self-gates.{txt,receipt.json}`.

## evidence

All commands executed 2026-07-17 by this attempt; nothing below is claimed
from unrun reference/CI evidence.

Host (macOS darwin/arm64, go1.26.5):

- `gofmt -l` clean; `go vet ./...` clean;
  `go vet -tags integration` (GOOS=linux) clean.
- `go test -count=1 ./...` → **747 tests pass in 47 packages**.
- `go test -race -count=1 ./...` → 747 pass (one earlier full-suite race run
  had a single failure in T4-owned `<HIGH_ENTROPY_REDACTED>`;
  it passes in isolation with and without `-race` and on the full rerun —
  recorded as a flake followup below, NOT touched: T4 replay logic is out of
  scope for this lane).
- Focused: control 54, store, daemon (incl.
  `<HIGH_ENTROPY_REDACTED>`,
  `<HIGH_ENTROPY_REDACTED>`), platform 9, hooks 27 —
  all pass.
- `go test -count=1 .<HIGH_ENTROPY_REDACTED>` → 10 PASS + 1 declared SKIP
  (receipt updated at the frozen path).
- Exact manifest command `go test -count=1 -tags integration -run
  'TrustMatrixReplay' ./...` → 42 `=== RUN`, 0 SKIP, 0 FAIL, exit 0
  (receipt + evidence at the frozen paths).
- `go mod verify` → all modules verified; `go mod tidy -diff` → clean (the
  old §7 low finding no longer reproduces).
- `CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build ./...` and `GOARCH=arm64` →
  both green; Windows placeholder still compiles (`GOOS=windows go build
  ./internal/platform/`).

Linux overlayfs containers (`scripts/qa/linux-gates.sh`, owner-safe
`TMPDIR=/qatmp` 0700, pinned go1.26.5, Docker 29.5.2; Arch amd64 under
emulation, Ubuntu 24.04 arm64 native):

- Full suite, both distros: `linux-gates-20260717/linux-gates-{arch,ubuntu}.log`
  → `exit=0` (the previously red overlayfs replacement contract is now green
  in the exact environment that pinned it).
- Focused verbose repro, both distros:
  `linux-focused-20260717/` + `linux-focused-arch-20260717/` →
  `<HIGH_ENTROPY_REDACTED>`,
  `<HIGH_ENTROPY_REDACTED>`,
  `<HIGH_ENTROPY_REDACTED>`,
  `<HIGH_ENTROPY_REDACTED>`, `TestValidationID*`,
  `<HIGH_ENTROPY_REDACTED>`,
  `<HIGH_ENTROPY_REDACTED>`,
  `<HIGH_ENTROPY_REDACTED>` — all PASS under overlayfs
  (birth-time discriminator live on the container upper layer).
- Integration gates, both distros (root in container):
  `linux-integration-20260717/` + `linux-integration-arch-20260717/` →
  `<HIGH_ENTROPY_REDACTED>` 41/41 rows PASS and the 4 real
  `TestSecondUID*` foreign-owner cases PASS; `exit=0`.

Proof obligations mapped: ext4/btrfs/xfs-style behavior → host suite +
fresh-inode branch of `<HIGH_ENTROPY_REDACTED>`; overlayfs dev+ino
reuse → container runs above + deterministic fakes; content churn never
invalidates → `<HIGH_ENTROPY_REDACTED>` +
`<HIGH_ENTROPY_REDACTED>`; restart/persistence
revalidation → `<HIGH_ENTROPY_REDACTED>` (SQLite, three
actor incarnations); unsupported capability typed+audited →
`<HIGH_ENTROPY_REDACTED>`; macOS conservative behavior →
darwin birthtime resolver, documented in HA-2e and validation.go.

## decisions

- The frozen key definition was preserved exactly; the discriminator is a
  SEPARATELY PERSISTED second factor (the mandate's preferred shape), so no
  spec redefinition and no ask-gate.
- statx birth time chosen over mount identity (changes across reboots of an
  unchanged root ⇒ false invalidation) and inode generation
  (<HIGH_ENTROPY_REDACTED> on overlay stacks); rejection rationale frozen in
  HA-2e and `validation.go`.
- Identity invalidation is audited as `project_revoked` WITH the
  `project_trust_required` code rather than minting a new audit kind — the
  AUD-2 vocabulary stays frozen while the trail remains distinguishable.
- This reopen writes outside the original T2 globs (internal/control, store,
  daemon, hooks, platform, transport/local) under the reopen mandate's
  explicit instruction; the 13 frozen ADR-0006 seams and the frozen key are
  untouched (seam-freeze + archtest green). Explicitly NOT touched: T4 replay
  logic (`internal/daemon/surface.go`), CLI (`cmd/**`), release tooling
  (`packaging/**`), TUI, research.
- Rows of the integrated replay that would need privileged mounts or
  unbuilt product surface are driven at the real linearization point with one
  injected fact, honestly recorded per row — not skipped, not faked as
  pipeline coverage.

## assumptions

- Container upper layers (Docker overlay2 on ext4) report `STATX_BTIME`;
  verified live in both containers by the passing validation tests. A
  filesystem that does not report it fails closed by design (denial, not
  breakage).
- `time.Sleep(50ms)` between remove/recreate in replacement tests keeps
  recreations out of coarse birth-time buckets; ns-resolution on
  ext4/APFS/overlay-upper makes this ample.

## risks

- Birth-time granularity bounds detection: a replacement completed within
  the filesystem's btime resolution of the original creation would collide;
  on ns-resolution filesystems this is not practically reachable.
- Conservative overlayfs edge (documented HA-2e): first copy-up of a
  lower-layer-only root invalidates trust once; fail-closed direction,
  re-approval recovers.
- Pre-migration rows lose trust reuse on first post-upgrade registration
  (absent discriminator = ambiguous). Intentional and documented; operators
  re-approve once.

## followups

- T4/qa: `<HIGH_ENTROPY_REDACTED>` failed once under
  full-suite `-race` load (passes isolated and on rerun) — flake in T4 replay
  territory; needs a T4-owned look alongside the open F1 (ReplayRead
  MaxBytes).
- T6-qa: re-emit the remaining check receipts against the corrected manifest
  (F-7 discipline); the two security-owned receipts are refreshed here.
- G-lane F1 (ReplayRead MaxBytes), F3 (GoReleaser pin), F4 (`amux
  --version`), F6 (release-promotion gate incl. the overstated
  `race-full-suite` receipt, CI TMPDIR, `.gitignore`) remain OPEN with their
  owning lanes (backend/devops/qa) — not addressed here by mandate.
- Grants are not rehydrated into actor memory across restart (trust state
  is); re-approval works and everything fails closed, but backend may want
  grant rehydration for operator ergonomics.

## learnings

- A frozen identity key can be exactly preserved while still closing an
  identity hole: persist a second factor beside it instead of silently
  redefining the digest.
- Gates must bind, not just run: `go test` exits 0 on zero matches and on
  all-skips, so every `-run`-shaped gate needs a static binding check plus a
  receipt-side executed/skip tally.

```guild.handoff.v2
{
  "schema_version": "guild.handoff.v2",
  "task_id": "T2-security",
  "tier": "powerful",
  "status": "done",
  "summary": "G-lane reopen closed F2 and F5. F2: overlayfs reuses (dev,ino) across rm+mkdir so the frozen durable key survived root replacement; fixed with a separately persisted replacement-validation discriminator (platform.<HIGH_ENTROPY_REDACTED>: Linux statx STATX_BTIME, Darwin st_birthtimespec, fail-closed <HIGH_ENTROPY_REDACTED> elsewhere) checked at registration, restart rehydration (new TrustStore.LoadProject path; also fixes latent post-restart epoch-monotonicity collision), and the pre-launch recheck; mismatch or absent value invalidates approved trust as an audited system revocation with monotonic epoch bump and grant deactivation; frozen key definition unchanged (HA-2e amendment). F5: retired the always-skipping <HIGH_ENTROPY_REDACTED> stub; added <HIGH_ENTROPY_REDACTED> (41/41 golden rows vs real control actor + SQLite trust store + hook runtime, per-row driver class recorded); added deterministic manifest self-gate (<HIGH_ENTROPY_REDACTED>) failing on zero-binding -run patterns, plus receipt binding/skip rules in security-readiness.md §6; manifest <HIGH_ENTROPY_REDACTED> corrected truthfully. Verified: 747 tests/47 pkgs pass, full -race pass, vet/gofmt/tidy/verify clean, no-cgo linux amd64+arm64 builds, <HIGH_ENTROPY_REDACTED> suites green inside Arch amd64 and Ubuntu arm64 overlayfs containers via scripts/qa/linux-gates.sh with owner-safe TMPDIR (previously-red <HIGH_ENTROPY_REDACTED> now green under overlayfs; 4 root second-UID cases pass). Fresh receipts at the frozen evidence paths for trust-matrix-replay (pass, 42 RUN/0 SKIP) and security-contract-self-gates (pass).",
  "artifacts": [
    "internal/platform/validation.go:1-99",
    "internal/platform/validation_linux.go:1-44",
    "internal/platform/validation_darwin.go:1-37",
    "internal/platform/validation_other.go:1-19",
    "internal/platform/validation_test.go:1-107",
    "internal/control/actor.go:186-338",
    "internal/control/store.go:19-46,84-131",
    "internal/control/actor_test.go:152-378",
    "internal/store/migrations.go:96-112",
    "internal/store/projects.go:19-97",
    "internal/daemon/truststore.go:27-104",
    "internal/daemon/truststore_test.go:1-215",
    "internal/hooks/runtime.go:44-63,131-137,428-448",
    "internal/hooks/validation_test.go:1-154",
    "internal/hooks/trustmatrix_integration_test.go:1-587",
    "internal/securitytest/gatebind.go:1-196",
    "internal/securitytest/gatebind_test.go:1-84",
    "internal/transport/local/local_test.go:206-210,328-331,391-397",
    "docs/security/hook-authorization.md:37-68",
    "docs/security/security-readiness.md:86-116",
    "docs/security/readiness-manifest.json:30-43,55-66",
    "docs/security/reviews/release-candidate.md:38-52,72-88",
    "docs/testing/strategy.md:153-185",
    ".amux-<HIGH_ENTROPY_REDACTED>-matrix-replay.receipt.json:1-14",
    ".amux-<HIGH_ENTROPY_REDACTED>-contract-self-gates.receipt.json:1-14",
    ".amux-<HIGH_ENTROPY_REDACTED>-gates-20260717/linux-gates-arch.log:1-55",
    ".amux-<HIGH_ENTROPY_REDACTED>-gates-20260717/linux-gates-ubuntu.log:1-60",
    ".amux-<HIGH_ENTROPY_REDACTED>-focused-20260717/linux-gates-ubuntu.log:1-120",
    ".amux-<HIGH_ENTROPY_REDACTED>-focused-arch-20260717/linux-gates-arch.log:1-149",
    ".amux-<HIGH_ENTROPY_REDACTED>-integration-20260717/linux-gates-ubuntu.log:1-180",
    ".amux-<HIGH_ENTROPY_REDACTED>-integration-arch-20260717/linux-gates-arch.log:1-223",
    ".guild/runs/run-8c2926bb-2ebe-4166-8b77-<HIGH_ENTROPY_REDACTED>-T2-security.md:1-330"
  ],
  "issues": [
    "<HIGH_ENTROPY_REDACTED> failed once under full-suite -race load, passes isolated and on rerun — T4-owned replay flake, reported not fixed (T4 replay logic out of scope by mandate)",
    "G-lane F1 (ReplayRead MaxBytes), F3 (GoReleaser pin), F4 (amux --version), F6 (release-promotion gate, race-full-suite receipt overstatement, CI TMPDIR, .gitignore) remain open with backend/devops/qa lanes",
    "Pre-migration project rows (absent discriminator) lose trust reuse on first post-upgrade registration by design — operators must re-approve once",
    "Grants do not rehydrate into actor memory across restart (trust state does); fail-closed, but a backend ergonomics followup"
  ],
  "learnings": [
    "Preserve a frozen identity key by persisting a second validation factor beside it instead of redefining the digest — closes the hole without a spec break.",
    "A -run-shaped gate needs two halves: a static self-gate proving the pattern binds to real tests, and a receipt rule counting executions and skips — exit code 0 alone proves nothing.",
    "Restart rehydration is part of the trust boundary: persisted trust must re-enter memory only through the same identity revalidation applied at registration."
  ],
  "notes": "Frozen durable key definition preserved exactly; discriminator is additive and separately persisted (migration v2, <HIGH_ENTROPY_REDACTED>-reuse). Capability semantics explicit and fail-closed on every platform; non-Linux compiles (Windows placeholder fails closed, Darwin documented conservative birthtime behavior). Audit epochs monotonic throughout; containment unweakened (invalidation reuses the revocation path incl. listeners). No T4 replay/CLI/release-<HIGH_ENTROPY_REDACTED> edits; no unrun reference or CI evidence claimed.",
  "injection_clean": "clean"
}
```
