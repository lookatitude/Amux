# Release-candidate security review (T6-qa work package Q8)

- Reviewer: fresh-context security review, 2026-07-17 (not involved in build/QA)
- Candidate: uncommitted working tree — `environment.env` records
  `git_commit=UNBORN_HEAD_no_commits_yet`, `git_status_dirty=20` (see F-8)
- Evidence root: `.amux-artifacts/qa/20260716T233403Z/`; every claim cites a file
  actually read; missing evidence is stated as such, never assumed green.
- Governing policy: `docs/security/readiness-manifest.json` (frozen 2026-07-15),
  `docs/security/security-readiness.md` §3/§6, `docs/security/threat-model.md`
  §5/§6, `docs/testing/strategy.md` §10–§11.

## 1. Evidence verified (positive results)

- Unit + race suites green on host (`q0-test-full.log`, `q0-test-race.log`, both
  `exit=0`); verify chain green — tidy-diff clean, `go mod verify`, licenses "all
  modules permissive", linkage fixture, backup-restore selftest
  (`q0-baseline-verify.log`, `q0-verify-final.log`, `exit=0`).
- `govulncheck` clean (`q5-govulncheck.log`, `exit=0`); non-goal audit clean — no
  cgo, no tcp/http listeners, CGO_ENABLED=0 static (`q8-nongoal-audit.log`).
- New second-UID harness (`internal/transport/local/seconduid_integration_test.go`,
  `integration && linux`, root-gated): 4/4 PASS as root on Ubuntu
  (`q5-security-linux/linux-gates-ubuntu.log:13-19`).
- Misuse walk AB-1..AB-12 recorded per row with executed evidence
  (`q5-misuse-walk.md`); all pass, AB-10 with a residual (F-11).

## 2. Findings

### F-1 — Project-identity trust replacement undetectable on overlayfs
- Severity: high | Disposition: blocking | Owner: security
- Evidence: `linux-gates-arch.log:44`, `linux-gates-ubuntu.log:45` — both distros
  `--- FAIL: TestReplacedRootChangesIdentity` ("replaced root produced the same
  project key"); mechanism (deterministic inode reuse) in `strategy.md` §11 F-1.
- Impact: on overlayfs (any container deployment) replacing a trusted project
  root reproduces the same `(realpath, st_dev, st_ino)` key, so trust is not
  invalidated — voiding the spec guarantee and weakening AB-6. Violated frozen
  contract ⇒ high per `security-readiness.md` §3, even though ext4/btrfs/xfs
  workstations are not deterministically affected.
- Repro: `TMPDIR=<0700 dir> go test -count=1 -run 'TestReplacedRootChangesIdentity$' ./internal/control` in any overlayfs container.
- Retest to close: test green in the Arch + Ubuntu container gate logs, OR an
  approved spec/threat-model amendment (explicit non-guarantee for inode-reusing
  filesystems + compensating identity control) with the pinning test updated.
- Status: **CLOSED 2026-07-17 (security lane, T2 reopen).** Compensating
  identity control shipped as the HA-2e replacement-validation discriminator
  (`hook-authorization.md` §1; frozen key definition unchanged): statx
  birth-time discriminator persisted beside the project row, revalidated at
  registration / restart rehydration / pre-launch, fail-closed on
  unsupported capability, trust invalidated with a monotonic epoch bump and
  audited system revocation on mismatch. Retest evidence: full suite green
  in both overlayfs containers
  (`.amux-artifacts/security/linux-gates-20260717/linux-gates-{arch,ubuntu}.log`,
  `exit=0`) and focused verbose runs of the pinning + new tests
  (`.amux-artifacts/security/linux-focused-20260717/`,
  `.amux-artifacts/security/linux-focused-arch-20260717/`).

### F-2 — Frozen security gates passed vacuously (phantom test / permanent skip)
- Severity: high | Disposition: blocking | Owner: security
- Evidence: `readiness-manifest.json` `trust-matrix-replay` runs
  `-run 'TrustMatrixReplay'` — matches zero tests; the real test is unit-level
  `TestDecideReplaysTrustMatrix` (`internal/control/decide_test.go:137`), not the
  "integrated daemon" replay the manifest describes. `integration-second-uid`
  (`-run 'SecondUID'`) matched only `TestSecondUIDVariantsDeferred`
  (`internal/transport/local/local_test.go:397`), which skips unconditionally even
  as root (second `t.Skip`, line 401; still SKIPs in
  `q5-security-linux/linux-gates-ubuntu.log:11`). `q5-security-host.log` shows the
  vacuous pattern: near-universal `[no tests to run]`, `rc=0`.
- Impact: two blocking high-severity release gates could report green while
  verifying nothing — an assurance-framework failure regardless of underlying
  behavior. Partially remediated by QA's real harnesses (§1), but the frozen
  manifest commands remain wrong and its self-gates missed the zero-match pattern.
- Repro: `go test -count=1 -tags integration -run 'TrustMatrixReplay' ./...` — exit 0, nothing executed.
- Retest to close: security-owned manifest correction (real test names; honest
  integrated-vs-unit scope); a self-gate asserting every `-run` pattern matches
  ≥1 non-skipped test; stub removed from the gate pattern; receipts per F-7.
- Status: **CLOSED 2026-07-17 (security lane, T2 reopen).**
  `trust-matrix-replay` now binds to the new integrated replay
  `TestTrustMatrixReplayIntegrated` (`internal/hooks`, `-tags integration`;
  real control actor + SQLite trust store + hook runtime; 41/41 rows with
  per-row driver class recorded — the unit-level Decide replay was NOT
  relabeled). `TestSecondUIDVariantsDeferred` deleted;
  `TestRetiredForeignUIDStubStaysRetired` pins that `-run 'SecondUID'` binds
  to nothing outside the real integration harness. Static self-gate
  `TestManifestRunPatternsBindToRealTests` (blocking, inside
  security-contract-self-gates) fails on any zero-binding `-run` pattern;
  runtime skip/zero-execution receipt rules added to
  `security-readiness.md` §6. Fresh receipts:
  `.amux-artifacts/security/trust-matrix-replay.receipt.json`,
  `.amux-artifacts/security/security-contract-self-gates.receipt.json`.

### F-3 — `Engine.ReplayRead` ignores `MaxBytes`; daemon severs connection
- Severity: high | Disposition: blocking | Owner: backend
- Evidence: `q5-resource-exhaustion-host.log` —
  `FAIL: TestResourceExhaustionOutputFloodAndClientBurst`: bounded `replay.read`
  (MaxBytes=1MiB) on a flooded surface returns "connection lost … EOF"; the whole
  retained window (≤16 MiB base64-expanded) is one unary response exceeding
  `v1.MaxHeaderBytes`. Harness: `internal/daemon/exhaustion_integration_test.go`.
- Impact: (a) availability — frozen blocking CLI flow 14 (`amux replay read`)
  reliably breaks above ~768 KiB retained output; (b) a client-supplied resource
  bound is unenforced server-side (multi-MiB response materialized per call),
  defeating the STR bounded-allocation contract the
  `integration-resource-exhaustion` gate exists to prove. Same-UID attackers are
  out of scope (threat-model §7): rated high on contract violation + availability.
- Repro: `go test -tags integration -run ResourceExhaustion ./internal/daemon`
- Retest to close: that test green in evidence; regression pinning MaxBytes at the
  engine boundary; fix the related wart (`replay_gap` bound only in the human
  message, not structured details — `strategy.md` §11 F-3).
- Status: open, failing in this evidence set.

### F-4 — Release pipeline cannot build with pinned GoReleaser (no artifacts as frozen)
- Severity: high | Disposition: blocking | Owner: devops
- Evidence: `q8-release-snapshot.log` — pinned v2.5.1 fails config parse (`field
  ids/formats not found`, `default_file_info` invalid ×2); `make release-check`
  exit=2. `q8-release-dryrun-diagnostic.log`: the pin "cannot parse the config";
  the successful dry run needed unpinned v2.12.7 + fixture-patched config, then
  produced tarballs/SBOMs/checksums and passed `verify-artifacts.sh`.
- Impact: the ADR-0007 provenance pipeline (SBOM/checksums/attestation) is
  inoperative as frozen — no artifact exists without violating the tool pin or
  editing frozen config. High: the supply-chain gate set cannot run; the
  diagnostic shows the fix is contained to config + pin.
- Repro: `make release-check` (pinned GoReleaser v2.5.1).
- Retest to close: corrected config + coherent pin committed; `release-check`
  exit 0; snapshot + `verify-artifacts.sh` + install smoke green with the pinned
  tool, logs archived.
- Status: open; only the unpinned patched diagnostic built artifacts.

### F-5 — `amux --version` unimplemented; frozen install smoke red
- Severity: medium | Disposition: blocking | Owner: backend
- Evidence: `q8-install-smoke.log` — `amux: unknown flag: --version`,
  `smoke: FAIL amux --version`, `SMOKE_INSTALL_EXIT=1` on Arch amd64 and Ubuntu
  24.04 amd64; all other smoke checks OK (static linkage, completions, daemon
  boot/shutdown; a version string prints via another path).
- Impact: interface mismatch with the frozen smoke contract; operationally
  relevant to deployed-version identification, not an exposure. Blocking because a
  frozen blocking gate is red.
- Repro: `packaging/smoke/smoke-install.sh` against the snapshot tarball.
- Retest to close: smoke green on Arch amd64, Ubuntu amd64, Ubuntu arm64.
- Status: open.

### F-6 — gitleaks: 9 findings (`.guild/` telemetry + fuzz seed literal)
- Severity: low | Disposition: accepted-with-conditions | Owner: devops
- Evidence: `q5-gitleaks.log` — 9 leaks, `exit=1`, all rule
  `amux-derived-candidate-secret`: 8 in `.guild/runs/run-8c2926bb…/logs/*`
  (dev-agent telemetry, untracked, unshipped) and 1 at
  `internal/redact/fuzz_test.go:14` — read directly: the deliberate
  credential-shaped fuzz seed (marker prefix `AMUXTEST_`, defanged here so
  this review passes the docs/security fixture sweep itself); line 15
  carries the canonical AWS docs example key (public documentation value,
  unflagged). No real credential in evidence.
- Conditions: frozen policy (`security-readiness.md` §3/§5) forbids run-time
  waivers — allowlist or purge the `.guild/` paths and fuzz seeds in a reviewed
  `.gitleaks.toml` commit; pin the scanner image (this run used
  `zricethezav/gitleaks:latest`); rerun clean once history exists (F-8).
- Status: conditions open; not release-blocking alone.

### F-7 — No `amux.security.check-receipt.v1` receipts at the frozen paths
- Severity: medium | Disposition: blocking | Owner: qa
- Evidence: `readiness-manifest.json` fixes `evidence_path` under
  `.amux-artifacts/security/…` for all 14 checks; that directory does not exist
  (listing of `.amux-artifacts/` verified). `security-readiness.md` §6: "a missing
  receipt equals a failed check." Raw logs exist only under `.amux-artifacts/qa/`.
- Retest to close: one receipt per manifest check (pass/fail/
  deferred_prerequisite) at the frozen paths, referencing the QA logs.
- Status: open — formally every manifest check currently reads as failed.

### F-8 — Candidate provenance: unborn HEAD, dirty tree, evidence not commit-bound
- Severity: medium | Disposition: blocking | Owner: devops
- Evidence: `environment.env` (`git_commit=UNBORN_HEAD_no_commits_yet`,
  `git_status_dirty=20`); diagnostic build metadata instead cites side-tree commit
  `134bf0a0…+dirty` (`q8-release-dryrun-diagnostic.log`, `/tmp/amux-relfix` in
  `q8-nongoal-audit.log`). `security-readiness.md` §10 concedes the history half
  of the secrets scan is meaningless while history is empty.
- Retest to close: candidate committed/tagged; blocking evidence re-run or
  re-attested against that immutable commit; history-inclusive gitleaks run.
- Status: open.

### F-9 — 30-minute soak (blocking gate R7): no completion evidence
- Severity: medium | Disposition: blocking | Owner: qa
- Evidence: `q7-soak-linux-30m.log` records only the launch;
  `.amux-artifacts/soak/20260717T001113Z/soak.log` is 0 bytes. Per `strategy.md`
  §10, a gate without executed evidence is "unproven", never green.
- Retest to close: completed 30-minute soak log archived in the evidence set.
- Status: not in evidence.

### F-10 — Fuzz smoke recorded red (`FuzzEngineFeed`)
- Severity: low | Disposition: accepted-with-conditions | Owner: qa
- Evidence: `q2-fuzz-smoke.log:43` — `FAIL: FuzzEngineFeed` "context deadline
  exceeded", overall `exit=1`; no crasher corpus in the log. `strategy.md` §9
  claims 3/3 isolated re-runs pass — those logs are not in evidence.
- Conditions: archive a green isolated `FuzzEngineFeed` run.
- Status: condition open.

### F-11 — AB-10 residual: no end-to-end second-UID client connect
- Severity: low | Disposition: accepted-with-conditions | Owner: qa
- Evidence: `q5-misuse-walk.md` AB-10 — foreign dir/socket/dial/owner cases pass
  as root, but "a true second-UID *client connect* end-to-end remains a residual
  gap"; daemon-side SO_PEERCRED rejection rests on injected-seam units.
- Conditions: add the second-UID client-connect case to the Linux CI harness, or
  record a security-approved deferral receipt.
- Status: condition open.

## 3. Residual risks RR-1..RR-5

`threat-model.md` §6 RR-1..RR-5 (launch-first window; approved-hook latitude; raw
PTY replay rings; transitive-library trust; wall-clock skew) remain as frozen:
all ≤ medium, none contradicted by this evidence set. RR-3 intersects F-3 (the
replay ring feeds `ReplayRead`) but F-3 is a bounds bug, not a new foreign-UID
confidentiality exposure. RR-1..RR-5 stay **accepted**; no high-severity residual
was introduced, so no orchestrator confirmation is needed.

## 4. Verdict

**Release is BLOCKED.** Four unresolved high-severity findings each independently
block promotion under `security-readiness.md` §3:

- **F-1** — trust replacement undetectable on overlayfs (violated frozen contract)
- **F-2** — frozen security gates passed vacuously; manifest still wrong
- **F-3** — `ReplayRead` ignores `MaxBytes`; bounds contract violated, flow 14 broken
- **F-4** — release/provenance pipeline inoperative with the pinned GoReleaser

Additionally blocking as red or unproven frozen gates: **F-5** (install smoke),
**F-7** (no check receipts — formally all checks fail), **F-8** (no immutable
candidate provenance), **F-9** (soak completion not in evidence). F-6, F-10 and
F-11 are accepted-with-conditions and do not block once the blockers above close
with the per-finding retest evidence. No release promotion may proceed while any
high-severity finding remains unresolved.
