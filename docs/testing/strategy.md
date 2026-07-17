# Amux release test strategy and traceability ledger (T6-qa)

This document is the requirement-to-evidence map for the Amux MVP
(spec `.guild/spec/amux-go-linux-runtime.md`, PRD
`.guild/prd/amux-go-linux-runtime.md`). Every spec/PRD acceptance criterion
maps to a named automated test (or an explicitly-unproven gate), the runner
command that executes it, its fixture source, where evidence lands, and its
blocking/nightly classification. QA's rule: a gate is green only when its
command was executed and its output retained — never by inference.

## 1. Runners

| Runner | Command | What it proves |
|---|---|---|
| R1 deterministic gate | `make verify` | gofmt, `go vet` (host+linux), staticcheck (pinned), `go mod verify`, tidy drift, dependency manifest, license audit, generated-file drift, packaging linkage fixture, backup/restore selftest |
| R2 unit/integration | `go test -count=1 ./...` | full blocking suite (unit, property, golden, integration, E2E incl. the 20-flow CLI suite) |
| R3 race | `go test -race -count=1 ./...` | R2 under the race detector |
| R4 fuzz smoke | `make fuzz-smoke` (`AMUX_FUZZTIME=15s`) | every `Fuzz*` target runs a bounded campaign; corpus regressions replay in R2 |
| R5 Linux gates | `scripts/qa/linux-gates.sh <arch\|ubuntu> <evidence-dir> [go-test args]` | R2 inside a real Linux container (Arch x86_64, Ubuntu 24.04) — exercises the Linux-only production seams (`amux daemon start` subprocess, `SO_PEERCRED`, /proc) |
| R6 security matrix | commands frozen in `docs/security/readiness-manifest.json` (`checks[]`) | scanner + integration-tagged security acceptance |
| R7 soak | `scripts/soak/run-soak.sh` (host) / `scripts/qa/linux-soak.sh <dur> [seed] [ptys]` (Linux container) | 20-PTY soak workload `TestSoak` (`internal/soak`, `-tags soak`) with metrics/pprof/trend evidence |
| R8 bench | `make bench` | benchmark smoke; reference-profile numbers come only from the documented Arch workstation (`docs/operations/reference-profile.md`) |
| R9 release | `make release-snapshot && make release-verify` | GoReleaser snapshot (linux amd64+arm64 tarballs, checksums, SBOMs) + integrity verification; install smoke via container |
| R10 plan integrity | `go run ./scripts/qa/plancheck .guild/plan/amux-go-linux-runtime.md` | task-ID uniqueness, dependency existence, DAG acyclicity, prose reference resolution |

Evidence layout: every QA execution writes under
`.amux-artifacts/qa/<UTC-stamp>/` (environment metadata in
`environment.env`, one log per gate, named `q<pkg>-<gate>.log` after the
work package). Soak evidence lands in `.amux-artifacts/soak/<UTC-stamp>/`
(`metadata.env`, `soak.log`, `metrics.jsonl`, `pprof/`, `summary.env`).
The 14 frozen security checks write their evidence to the manifest's
`.amux-artifacts/security/<check-id>.txt` paths with one
`amux.security.check-receipt.v1` record per check
(`<check-id>.receipt.json`) — a missing receipt equals a failed check.
Artifacts are host-local release evidence, not commit material.

## 2. Seed and determinism policy

- Property/stress tests use fixed in-code seeds or fully enumerated inputs.
- Fuzz targets keep their corpora in `testdata/fuzz/` (regressions replay
  deterministically in R2); campaign findings must be committed as corpus
  entries, never re-found by chance.
- The soak workload derives all stimulus from `-soak.seed` (default 1);
  PTY payloads are fixed `/bin/sh` generators and `/bin/cat` echo surfaces.
- Golden files (protocol vectors, VT grids, TUI frames, trust matrix) are
  committed and diffed — `TestTrustMatrixGoldenIsCurrent` and
  `scripts/check-generated.sh` fail on drift.
- Time is injected (`platform.Clock`); tests never sleep-and-hope on wall
  time for correctness assertions.

## 3. Fixture taxonomy

| Class | Home | Examples |
|---|---|---|
| Protocol golden vectors | `api/v1/testdata`, `internal/rpcapi/testdata` | frame/header/error encodings, projection vectors |
| VT corpus | `internal/terminal` (corpus + goldens) | escape-sequence replay to golden cell grids |
| TUI golden frames | `internal/tui/app/testdata` | 8-pane scene, focus, modal, min-size, stopped/restarted |
| Fuzz corpora | `testdata/fuzz/` per package | sidecar, JSONC, ANSI engine, protocol header, redaction, adapter feeds |
| Security vectors/matrix | `internal/securitytest` (`vectors.go`, `matrix.go`, 41-row trust matrix golden) | misuse fixtures AB-1..12, HA rows, redaction contexts |
| Fault-injection seams | `internal/testkit`, per-package fakes | fsync/rename/disk-full failers, crash-at-commit-step, fake clocks/PTYs, barriers |
| Real-process fixtures | `cmd/amux/e2e_test.go`, `internal/soak` | real daemon + `cat`/`sh` PTY children |

## 4. Traceability — spec functional acceptance

| # | Spec criterion | Named evidence (primary) | Runner | Class |
|---|---|---|---|---|
| 1 | session lifecycle + persistence | `TestTwentyFlows` (flows 3–6, 16–17), `internal/session` state-machine tests | R2/R5 | blocking |
| 2 | 8-pane live TUI (focus/resize/redraw/input/exit) | `TestEightPaneFixtureRendersBackendData`, `TestSceneHasEightPanes`, `TestOneToEightPanesTileExactly`, `TestEightPaneCursorAndLeaseVisible`, golden frames | R2/R5 | blocking |
| 3 | per-pane cwd + git-root discovery | `internal/context` collectors + `TestTwentyFlows` cwd fixtures | R2/R5 | blocking |
| 4 | snapshot restore preserves IDs/tree/cwd/argv/env/policy/floor/notifications/cursor | `TestGraphRoundtrip`, `TestOpenLatestRoundtrip`, `TestSnapshotRestoreResumesCursor`, `TestExportImportRoundtripRestoresReadState` | R2/R5 | blocking |
| 5 | restore classes live/restarted/stopped, no resurrection, no restored attachments | `TestClassifyFreshDaemonNeverLive`, `TestInDaemonRestoreReconcilesOwnedSurfaceLive`, `TestFreshDaemonRestoreNeverLiveEvenWhileProcessStillRuns`, `TestEngineSnapshotSaveRestoreNeverLive`, e2e flow 17 | R2/R5 | blocking |
| 6 | deterministic VT corpus replay | `internal/terminal` corpus goldens + `FuzzEngineFeed` corpus | R2/R4 | blocking |
| 7 | zero orphaned PTYs (normal + forced termination) | `TestStopAllLeavesZeroOrphans`, `TestSignalTargetsWholeProcessGroup`, `TestSoak` descendant gates | R2/R7 | blocking |
| 8 | hook failure modes fail closed + audited | `internal/hooks` + `TestHookTrust*` family, securitytest vectors | R2/R6 | blocking |
| 9 | snapshot-on-gap recovery | `TestGapRecoveryFlow`, `TestRingReplayGapTyped`, `TestAttachCutoverDetectsGaps`, e2e flow 20 (`event_gap` + resume) | R2/R5 | blocking |
| 10 | both arches compile/package; Arch+Ubuntu CI | `make build-linux` (amd64+arm64), R5 both distros, R9 artifacts | R1/R5/R9 | blocking |
| 11 | multi-repo trust isolation, zero hook process on invalid scope | `TestTwoSessionsShareProjectNoPostRevokeLaunch`, `TestTrustLifecycleEpochsAndGrants`, securitytest matrix (41 rows) | R2/R6 | blocking |
| 12 | two-client attach/lease contract | `TestAttachDeliversSnapshotThenReplayThenLive`, `TestNonHolderWriteRejectedHolderReaches`, `TestAcquireDoesNotImplicitlyTakeover`, `TestDetachReleasesLeaseButSurfaceAndSinkStayLive`, e2e flows 12–13 | R2/R5 | blocking |

## 5. Traceability — 20 CLI flows

All 20 flows execute in one black-box suite, `TestTwentyFlows`
(`cmd/amux/e2e_test.go`), against the real built binary and a real daemon
(on Linux: `amux daemon start` subprocess + production `SO_PEERCRED`).
Runner: R2 (host, in-process seam off Linux) and R5 (Arch + Ubuntu,
production path). Classification: blocking. The suite uses no internal
package access for flow execution — argv/exit-code/`--json` only.

## 6. Traceability — performance and reliability

| Gate | Evidence | Runner | Class | Status discipline |
|---|---|---|---|---|
| 30-min blocking soak, 20 PTYs, no crash/gap/orphan/unbounded trend | `TestSoak` via R7; verdict in `.amux-artifacts/soak/<stamp>/summary.env` | R7 | blocking | must be executed for its full wall-clock duration; the harness fails closed when the workload is missing |
| 8-h nightly soak (reference profile) | same workload, `AMUX_SOAK_DURATION=8h` (`.github/workflows/nightly-soak.yml`) | R7 | nightly (release-promotion) | requires elapsed 8 h on the reference profile — never simulated |
| restore 8-pane fixture < 2 s (reference profile) | restore-path timing on reference hardware | R8 + reference host | blocking (release) | measured only on the documented Arch x86_64 workstation |
| split/focus/resize p95 < 75 ms (reference profile) | `BenchmarkFrameLatency`, `BenchmarkRenderDamageAware` + reference-profile run | R8 + reference host | blocking (release) | container/macOS numbers are indicative only, never gate evidence |
| monotonic contiguous event IDs; injected gap recovers or fails | `TestSoak` subscriber contiguity + `TestGapRecoveryFlow` | R2/R7 | blocking |  |

## 7. Traceability — hook trust acceptance

| Gate | Evidence | Runner | Class |
|---|---|---|---|
| absent trust: `project_trust_required` ≤ 250 ms, zero processes | securitytest gates (`Gates.AbsentTrustMS`) + `TestHookTrustAbsentGrantsFailsClosed` | R2/R6 | blocking |
| approval audit references both grants | `TestTrustLifecycleEpochsAndGrants`, audit assertions | R2/R6 | blocking |
| revocation: cancel ≤ 250 ms, epoch monotonic, history retained | `TestRevokeListener`, `TestUpsertProjectPreservesTrustState`, matrix rows | R2/R6 | blocking |
| deterministic revoke-first / launch-first barriers | `TestRevokeFirstCreatesNoChild`, `TestLaunchFirstThenRevoke`, `TestNoAuthorizeLinearizesAfterRevoke` | R2/R6 | blocking |
| cwd containment denies before launch | scope-violation vectors (securitytest matrix) | R2/R6 | blocking |
| scanners: govulncheck, go-licenses, gitleaks, race suite | R6 frozen commands, versions recorded in evidence log | R6 | blocking |

## 8. Traceability — PRD release gates

| PRD gate | Evidence | Class |
|---|---|---|
| 1 protocol vectors + snapshot migration | `api/v1` goldens, `TestGraphV0MigratesForward`, `TestOpenLatestV0GenerationMigratesInMemory` | blocking |
| 2 unit/property/fuzz/race on supported arches | R2+R3+R4 host, R5 both distros (fuzz where tooling permits) | blocking |
| 3 20 CLI flows | §5 | blocking |
| 4 8-pane TUI + latency evidence | §4.2 + §6 | blocking |
| 5 PTY forced-termination + orphan scan | §4.7 | blocking |
| 6 event/replay gap recovery | §4.9 | blocking |
| 7 hook trust family | §7 | blocking |
| 8 30-min soak (+8-h for promotion) | §6 | blocking / nightly |
| 9 Arch+Ubuntu package install smoke (amd64+arm64) | R9 snapshot artifacts + container install smoke | blocking |
| 10 operator docs (backup/restore/diagnostics/trust/verification) | `docs/operations/*`, `docs/release/*`, `docs/security/*` — reviewed for existence and command accuracy | blocking |

## 9. Environment prerequisites (learned the hard way)

- **TMPDIR must be owner-safe on Linux.** The STR-3 transport hardening
  rejects any socket path with a group/other-writable component, so a test
  socket chain can never live under `/tmp` (mode 1777). Production is
  unaffected (`$XDG_RUNTIME_DIR/amux` is 0700), but every Linux test runner
  (CI included) must export a 0700 `TMPDIR` (see `scripts/qa/linux-gates.sh`).
- **`archlinux` images are x86_64-only** — Arch containers on aarch64 hosts
  run under `--platform linux/amd64` emulation; metadata records this.
- **Fuzz smoke wants an unloaded host.** A saturated host can abort a fuzz
  worker shutdown with `context deadline exceeded` (observed once for
  `FuzzEngineFeed` while two containers and a soak shared the machine;
  3/3 isolated re-runs pass). Not a product failure — no crasher corpus is
  produced — but schedule R4 without heavy parallel load.

## 10. Blocking vs nightly rules

- Blocking (every merge): R1, R2, R3 (amd64 lane), R4, R10; R5 on both
  distros; R6 scanner set; R7 30-minute soak on the CI profile; R9 snapshot +
  install smoke on release branches.
- Nightly / release-promotion: 8-hour soak on the reference profile,
  long fuzz campaigns, reference-profile performance runs.
- A gate that cannot execute in an environment is reported **unproven**, with
  its exact command — never skipped-as-green, never reclassified without
  explicit approval.

## 11. Known pinned findings (current)

1. **F-1 RESOLVED 2026-07-17 (security, G-lane F2 remediation).** Overlayfs
   deterministically reuses the directory inode across `rm -rf root && mkdir
   root`, so the frozen `(realpath, st_dev, st_ino)` key alone could not see
   the replacement. Fixed by the HA-2e replacement-validation discriminator
   (`platform.ReplacementValidator`, statx `STATX_BTIME` on Linux, persisted
   beside the project row, fail-closed capability semantics): registration,
   restart rehydration, and the pre-launch recheck all invalidate trust on a
   discriminator mismatch. The frozen key definition is unchanged.
   `TestReplacedRootChangesIdentity` now asserts BOTH branches (fresh-inode
   key change; inode-reuse trust invalidation) and is green inside the Arch
   and Ubuntu overlayfs containers
   (`.amux-artifacts/security/linux-gates-20260717/`,
   `.amux-artifacts/security/linux-focused-20260717/`). Reproduction command
   unchanged:
   `TMPDIR=<0700 dir> go test -count=1 -run 'TestReplacedRootChangesIdentity$' ./internal/control`.
   Deterministic inode-reuse coverage on any host:
   `TestReusedInodeReplacementInvalidatesTrust` (internal/control) and
   `TestTrustRehydratesAcrossRestartAndRevalidates` (internal/daemon).
2. **F-2 RESOLVED 2026-07-17 (security, G-lane F5 remediation).** The
   `trust-matrix-replay` pattern now binds to the real integrated replay
   `TestTrustMatrixReplayIntegrated` (`internal/hooks`, `-tags integration`:
   production control actor + SQLite trust store + hook runtime; 41/41 rows,
   per-row driver class recorded in the test). The always-skipping
   `TestSecondUIDVariantsDeferred` stub was retired, so `-run 'SecondUID'`
   binds only to QA's real harness
   (`internal/transport/local/seconduid_integration_test.go`). A
   deterministic self-gate (`TestManifestRunPatternsBindToRealTests`,
   `internal/securitytest/gatebind.go`) now fails the blocking
   security-contract-self-gates check whenever any manifest `-run` pattern
   binds to zero tests; receipt rules for skip handling are in
   `security-readiness.md` §6.
3. **F-3 `Engine.ReplayRead` ignores `ReplayReadParams.MaxBytes`**
   (backend-owned, blocking): the whole retained window (≤16 MiB,
   base64-expanded) is encoded into one unary JSON response, exceeding
   `v1.MaxHeaderBytes` (1 MiB) — the daemon severs the connection with an
   untyped EOF. CLI flow 14 (`amux replay read`) fails on any surface with
   more than ~768 KiB retained output. Pinned by
   `go test -tags integration -run ResourceExhaustion ./internal/daemon`.
   Related wart: the `replay_gap` oldest-retained bound travels only in the
   human message, not structured details.
4. **F-4 release pipeline cannot build as frozen** (devops-owned, blocking):
   `packaging/goreleaser/goreleaser.yaml` uses `ids`/`formats` (require
   GoReleaser ≥2.6; pin is v2.5.1) and `default_file_info` (invalid in every
   version — the key is `info`); the before-hook also writes
   `dist/completions`, which ≥2.6 rejects after `--clean`. `make
   release-check` exits 2 with the pinned tool. A fixture-patched diagnostic
   dry run (v2.12.7 + `info:` + completions staged outside dist) built both
   arch tarballs, SBOMs, checksums and passed `verify-artifacts.sh`, so the
   fix is contained to config+pin.
5. **F-5 `amux --version` unimplemented** (backend/devops interface
   mismatch, blocking for the packaging gate): the frozen install smoke
   (`packaging/smoke/smoke-install.sh`) requires `amux --version`; the CLI
   rejects the flag (`amuxd --version` works). Everything else in the smoke
   (static linkage, completions, daemon/CLI smoke on installed binaries)
   passes on Arch amd64, Ubuntu amd64 and Ubuntu arm64 containers.
