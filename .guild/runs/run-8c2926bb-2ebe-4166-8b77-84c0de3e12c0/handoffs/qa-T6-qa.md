# T6 qa — handoff (release evidence system, executed)

- task-id: T6-qa
- owner: qa
- depends-on: T4-backend, T2-security, T3-devops, T5-terminal-ui
- status: blocked (lane work complete and executed; release blocked on pinned product/pipeline findings + platform-bound unproven gates)

## What was built (durable, test-only)

- `docs/testing/strategy.md` — requirement→test→runner→fixture→evidence→classification ledger for every spec/PRD criterion, plus seed policy, fixture taxonomy, environment prerequisites, blocking/nightly rules, and the pinned-findings register (F-1..F-5).
- `internal/soak/` — the previously missing `TestSoak` workload (`-tags soak`): boots the production daemon assembly, 20 real PTYs across 4 workspaces, seeded stimulus (<HIGH_ENTROPY_REDACTED>-detach/stop-restart/snapshot), a contiguity-asserting subscriber with typed `event_gap` recovery, metrics/pprof capture, generator-liveness check, zero-orphan teardown gates, and first-vs-last-quartile trend gates (goroutines/FDs/heap/children). `scripts/soak/run-soak.sh` updated (QA harness-invocation carve-out): targets the defining package (`-soak.*` flags would abort every other test binary), adds `-timeout` (default 10 m would kill the 30 m/8 h profiles), absolute evidence paths.
- `scripts/qa/linux-gates.sh`, `scripts/qa/linux-soak.sh` — real-Linux gate runners (Arch x86_64 via `--platform linux/amd64`, Ubuntu 24.04 native) with owner-safe `TMPDIR` (STR-3 forbids socket chains under 1777 `/tmp`) and recorded image digests/kernels.
- `<HIGH_ENTROPY_REDACTED>` — deterministic plan-integrity gate (Q1): unique task IDs, deps exist, DAG acyclic, prose refs resolve. All rules hold.
- `internal/transport/local/seconduid_integration_test.go` — REAL second-UID STR-3/4 cases (foreign-owned chain component / crash-shaped stale socket / live socket / owner-mismatch dial); all pass as root on Linux. Replaces the always-skip stub as the substance behind `integration-second-uid`.
- `internal/daemon/exhaustion_integration_test.go` — resource-exhaustion acceptance: 48 MiB PTY flood (daemon healthy, heap bounded), 100-client burst (all served), and the bounded-replay assertion that pins F-3.
- `.amux-artifacts/security/` — all 14 frozen readiness-manifest checks executed at their frozen evidence paths with one `amux.security.check-receipt.v1` each (11 pass, 2 fail honestly — F-2 phantom pattern, F-3 pin — 1 `deferred_prerequisite`: history secrets-scan needs a first commit).
- `docs/security/reviews/release-candidate.md` — fresh-context security review (dispatched reviewer, evidence-cited): release BLOCKED; 4 high + 4 medium blocking findings, low findings accepted-with-conditions, RR-1..RR-5 remain accepted. Note: check receipts were emitted after the review snapshot, closing its F-7 for executed checks.

## Executed gates (evidence: `.amux-<HIGH_ENTROPY_REDACTED>`, env in `environment.env`)

| Gate | Result | Evidence |
|---|---|---|
| `go mod tidy -diff`, `go mod verify`, `make verify` (fmt/vet/staticcheck/deps-manifest/license/generated/linkage/backup-restore) | PASS (incl. re-run after all QA additions) | `q0-baseline-verify.log`, `q0-verify-final.log` |
| Full suite host (darwin/arm64) | 39 pkgs ok, re-stamped post-additions | `q0-test-full.log`, `q0-test-full-final.log` |
| Race suite host | 39 pkgs ok | `q0-test-race.log` |
| Linux cross-builds amd64+arm64, CGO=0 | PASS | `q0-build-linux.log` |
| Arch x86_64 container full suite (incl. 20-flow E2E via real `amux daemon start` + SO_PEERCRED) | 38/39 ok; sole failure = pinned F-1 | `linux-gates-arch.log` |
| Ubuntu 24.04 container full suite (native arm64) | 38/39 ok; sole failure = pinned F-1 | `linux-gates-ubuntu.log` |
| Fuzz smoke (6 targets × 15 s) | PASS on unloaded host; one shutdown-timeout flake under saturation diagnosed (not a crasher; see strategy §9) | `q2-fuzz-smoke-clean.log`, `q2-fuzz-smoke.log` |
| Plan integrity | PASS (6 lanes, acyclic, refs resolve) | `q1-plancheck.log` |
| 14 frozen security checks + receipts | 11 pass / 2 fail (pins) / 1 deferred | `.amux-artifacts/security/*` |
| Misuse walk AB-1..AB-12 | 12/12 pass (AB-10 now on real harness) | `q5-misuse-walk.md` |
| govulncheck | No vulnerabilities | `q5-govulncheck.log` |
| gitleaks v8.30.1 (working tree; no history exists) | findings only in untracked `.guild/` telemetry + 1 deliberate defanged fuzz seed | `q5-gitleaks.log` |
| 30-min blocking soak, 20 PTYs, seed 1 — Arch x86_64 container (Docker VM, 10 CPU/7.8 GiB ≥ CI profile) | **PASS** (1807 s elapsed; 0 gaps, 0 orphans, 362 samples: goroutines 63→58, heap flat, children constant 30, 812 contiguous events, 1584 ops) | `.amux-<HIGH_ENTROPY_REDACTED>` |
| Snapshot artifacts (diagnostic dry run, see F-4) + `verify-artifacts.sh` | checksums OK, SBOMs present, build-metadata present | `q8-release-snapshot.log`, `q8-release-dryrun-diagnostic.log` |
| Install + daemon/CLI smoke of built tarballs in clean containers (Arch amd64, Ubuntu amd64, Ubuntu arm64) | daemon/CLI smoke PASS ×3; install smoke fails only on F-5 (`amux --version`) | `q8-install-smoke.log` |
| Non-goal/scope audit (cgo, TCP/HTTP listeners, cmux reuse, dep graph, binary build info) | CLEAN | `q8-nongoal-audit.log` |
| Benchmarks (indicative only) | frame p50 ≈0.2–0.3 ms, full 8-pane frame ≈0.9–1.4 ms (macOS + Ubuntu container) | `q7-bench-host.log`, `q7-bench-linux.log` |

## Blocking findings (pinned, not fixed — outside QA ownership)

1. **F-3 backend (high):** `Engine.ReplayRead` ignores `MaxBytes`; >~768 KiB retained output → response exceeds `v1.MaxHeaderBytes` → connection severed with untyped EOF; CLI flow 14 breaks. Repro: `go test -tags integration -run ResourceExhaustion ./internal/daemon/`.
2. **F-1 security/backend (high):** overlayfs reuses dir inodes on rm+mkdir → `(realpath,dev,ino)` trust identity cannot detect root replacement. Repro: `TMPDIR=<0700> go test -run '<HIGH_ENTROPY_REDACTED>$' ./internal/control` in any overlayfs container.
3. **F-4 devops (high):** `packaging/goreleaser/goreleaser.yaml` incompatible with pinned v2.5.1 (`ids`/`formats` need ≥2.6) and invalid in all versions (`default_file_info`→`info`); before-hook writes into `dist/`. `make release-check` exits 2. Diagnostic dry run (fixture-patched config + v2.12.7) built/verified both arch tarballs — fix is config+pin only.
4. **F-2 security (high per review):** manifest patterns `TrustMatrixReplay` (matches zero tests) and old second-UID stub passed vacuously; correct commands + retire stub. Real coverage now exists and is green.
5. **F-5 backend/devops (medium, blocks packaging gate):** `amux --version` unimplemented; frozen install smoke requires it (`amuxd --version` works).
6. Devops followups: CI lanes never set an owner-safe `TMPDIR` (hosted runners with `/tmp` will fail suite-wide per STR-3); no `.gitignore` (19 MiB binary, `.guild/`, `.amux-artifacts/` would enter history and the telemetry trips gitleaks); `replay_gap` bound only in human message (automation contract wart).

## Unproven gates (honest blockers — harness ready, environment absent; never simulated)

- Reference-profile performance (restore <2 s, split/focus/resize p95 <75 ms) — requires the documented Arch x86_64 workstation: `make bench` + restore timing per `docs/operations/reference-profile.md`.
- 8-hour nightly soak — `AMUX_SOAK_DURATION=8h scripts/soak/run-soak.sh` on the reference profile (workflow wired; inherits fixed harness).
- AUR clean-chroot install — PKGBUILD is a pre-release template (placeholder URL, zeroed sha256); needs a published release + real Arch host with devtools (`makepkg`/`extra-x86_64-build`).
- Hosted-CI Arch/Ubuntu amd64/arm64 lanes — no commits/remote exist; container evidence here is local Linux, not CI.
- Real-terminal interactive 8-pane TUI smoke — automated model/golden/replay coverage is green; a human-driven TTY session on Linux remains unautomated.
- History-mode secrets scan and commit-bound provenance (reviewer F-8) — repo has an unborn HEAD; bind evidence to a commit once one exists.

## Environment

Host: macOS 26.5.2 (Darwin 25.5.0 arm64, Apple M4, 10 cores, 24 GiB), go1.26.5, Docker 29.5.2 (linuxkit VM 10 CPU/7.8 GiB, kernel 6.12.76). Containers: archlinux:latest (x86_64 under Rosetta/qemu), ubuntu:24.04 (native arm64 + amd64 emulated; digest in gate logs). Repo state: unborn HEAD, working tree only. GoReleaser v2.5.1 + syft v1.18.1 in `.tools/bin`; gitleaks v8.30.1 (container); diagnostic GoReleaser v2.12.7 via `go run`.

## Reproduce (current commands)

```
make verify && go test -count=1 ./... && go test -race -count=1 ./... && make build-linux
scripts/qa/linux-gates.sh arch  .amux-artifacts/qa/<stamp>
scripts/qa/linux-gates.sh ubuntu .amux-artifacts/qa/<stamp>
scripts/qa/linux-soak.sh 30m 1 20          # blocking soak (Linux container)
go run ./scripts/qa/plancheck .guild/plan/amux-go-linux-runtime.md
go test -count=1 -tags integration -run 'SecondUID' .<HIGH_ENTROPY_REDACTED>   # root on Linux
go test -count=1 -tags integration -run 'ResourceExhaustion' ./internal/daemon/   # fails: pins F-3
make fuzz-smoke
# security checks + receipts: commands in docs/security/readiness-manifest.json, evidence .amux-artifacts/security/
```

## Residual risks

RR-1..RR-5 (T2, ≤medium) remain accepted and explicit. New: overlayfs/container deployments void trust-replacement detection (F-1) until security disposition; soak/perf evidence is container-Linux, not reference-hardware; fuzz smoke wants an unloaded host; second-UID *client-connect* end-to-end remains seam-tested only.

```guild.handoff.v2
{
  "schema_version": "guild.handoff.v2",
  "task_id": "T6-qa",
  "tier": "powerful",
  "status": "blocked",
  "summary": "T6 evidence system built+executed: traceability ledger, plan-check, new 20-PTY TestSoak; 30-min Arch soak PASS (0 gaps/orphans, flat trends); suites green on macOS/Arch/Ubuntu incl 20-flow E2E on the production Linux daemon path; all 14 frozen security checks executed with receipts (new real second-UID + exhaustion harnesses); artifacts verified + install/daemon smokes x3. RELEASE BLOCKED: ReplayRead ignores MaxBytes (flow 14 breaks), overlayfs voids trust-replacement identity, GoReleaser pin/config mismatch, amux --version missing; reference-profile perf, 8h soak, AUR chroot unproven.",
  "artifacts": [
    "docs/testing/strategy.md",
    "docs/security/reviews/release-candidate.md",
    "internal/soak/doc.go",
    "internal/soak/soak_test.go",
    "internal/daemon/exhaustion_integration_test.go",
    "internal/transport/local/seconduid_integration_test.go",
    "scripts/qa/plancheck/main.go",
    "scripts/qa/linux-gates.sh",
    "scripts/qa/linux-soak.sh",
    "scripts/soak/run-soak.sh",
    ".amux-artifacts/security/",
    ".amux-<HIGH_ENTROPY_REDACTED>",
    ".amux-<HIGH_ENTROPY_REDACTED>",
    ".guild/runs/run-8c2926bb-2ebe-4166-8b77-<HIGH_ENTROPY_REDACTED>-T6-qa.md"
  ],
  "issues": [
    "F-3 backend HIGH: Engine.ReplayRead ignores MaxBytes -> >~768KiB replay severs connection (untyped EOF), CLI flow 14 breaks; pinned by go test -tags integration -run ResourceExhaustion ./internal/daemon/",
    "F-1 security/backend HIGH: overlayfs inode reuse defeats (realpath,dev,ino) trust-replacement detection; pinned by <HIGH_ENTROPY_REDACTED> in any overlayfs container",
    "F-4 devops HIGH: goreleaser.yaml incompatible with pinned v2.5.1 and invalid key default_file_info; make release-check exits 2; no release artifacts as frozen",
    "F-2 security HIGH: readiness-manifest patterns TrustMatrixReplay + second-UID stub passed vacuously; real harnesses now exist, manifest strings need correction",
    "F-5 backend/devops MEDIUM: amux --version missing vs frozen install smoke (SMOKE_INSTALL_EXIT=1 on all 3 distro/arch combos)",
    "devops: CI never sets owner-safe TMPDIR (STR-3 makes /tmp socket chains fail suite-wide on hosted runners); no .gitignore for .guild/.amux-artifacts/stray 19MiB binary",
    "unproven (env-bound, never simulated): reference-profile perf gates, 8h nightly soak, AUR clean-chroot, hosted-CI lanes, interactive TUI real-terminal smoke, history-mode secrets scan / commit-bound provenance (unborn HEAD)"
  ],
  "learnings": [
    "go test runs each test binary with the package dir as cwd and passes unknown flags to every selected package: soak-style custom flags need package-targeted invocation and absolute evidence paths.",
    "STR-3 path hardening makes any socket chain under 1777 /tmp unusable: every Linux test runner (CI included) must export an owner-only TMPDIR.",
    "A frozen check list is only as good as its -run patterns: two security gates passed vacuously for a whole phase because the named tests did not exist.",
    "Unary JSON responses share the 1MiB header cap: any result embedding replay-scale data must be chunked or capped server-side, or the frame layer silently kills the connection."
  ],
  "notes": "Fresh security review: docs/security/reviews/release-candidate.md \u2014 release blocked (4 high). No commits, no publishing, no external state changed. Runners: docs/testing/strategy.md section 1.",
  "injection_clean": "clean"
}
```
