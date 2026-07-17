# Security readiness gate (T2-security, frozen pre-implementation)

Status: frozen 2026-07-15 (run-8c2926bb-2ebe-4166-8b77-84c0de3e12c0, lane T2-security).
This document freezes, before backend dispatch: the scanner set, the secret-scan
policy, the malicious fixture inventory, the manual misuse cases, and the
severity/blocking rules for release promotion. Its machine-readable half is
`docs/security/readiness-manifest.json` — the exhaustive list of required
integrated-candidate checks with owner, blocking severity, reproducible
command, prerequisites, and evidence path. `TestReadinessManifestIsWellFormed`
and `TestGateConstantsMatchManifest` (`internal/securitytest`) keep the
manifest, the Go gate constants, and the prose from drifting apart.

## 1. Two gates

- **Backend-dispatch gate (this lane, passed).** The security design review of
  the T1-frozen contracts against this lane's threat model reports **no
  unresolved high-severity contract finding** (§7). The fail-closed contract
  (`hook-authorization.md`), transport hardening (`local-transport-hardening.md`),
  redaction/audit rules (`redaction-and-audit.md`), trust matrix, and
  adversarial fixtures are frozen and executable. T4 may be dispatched.
- **Release-promotion gate (T6, pending by design).** Every check in
  `readiness-manifest.json` is executed against the integrated candidate; each
  produces an `amux.security.check-receipt.v1` record (§6). A blocking check
  that fails — or is skipped without a recorded `deferred_prerequisite`
  receipt and an orchestrator-approved deferral — stops promotion.

## 2. Frozen scanner set

| Scanner | Purpose | Reproducible invocation | Pinning |
|---------|---------|------------------------|---------|
| `go mod verify` | Module checksum integrity against `go.sum` | `go mod verify` | Go toolchain (pinned in `go.mod`) |
| `go mod tidy -diff` | Undeclared/stale requirement drift | `go mod tidy -diff` | Go toolchain |
| `govulncheck` | Known CVEs on the module graph + reachable call paths | `govulncheck ./...` | version pinned by T3 in CI (ADR-0007 pipeline) |
| `gitleaks` | Secrets in working tree + full git history | `gitleaks detect --source . --config .gitleaks.toml --redact` | version pinned by T3 in CI |
| `go-licenses` | License inventory vs the §4 allowlist | `go-licenses report ./... --ignore github.com/amux-run/amux` | version pinned by T3 in CI |

T3 devops wires these into the pipeline per ADR-0007; the *policy* (what runs,
what blocks) is frozen here and changes only through the spec confirmation
gate. `--redact` is mandatory for gitleaks so candidate secret values never
enter CI logs or artifacts.

## 3. Severity and blocking rules

- **high** — a violated frozen requirement (HA-*/STR-*/RED-*/AUD-*), a
  reachable HIGH/CRITICAL vulnerability, or any confirmed leaked secret.
  Always blocking; no promotion until fixed or the orchestrator explicitly
  accepts the residual through the confirmation gate.
- **medium** — scanner findings without demonstrated reachability, tidy/license
  drift. Blocking until triaged; a written per-finding rationale
  (`deferred_prerequisite` or documented false positive) may pass it.
- **low** — informational. Recorded in the receipt, never silently dropped.
- Scanner *absence* is never a pass: a check whose tool is unavailable on the
  executing host produces a `deferred_prerequisite` receipt naming the host
  limitation and the exact deferred command. Fabricating a clean scan is a
  contract violation.
- gitleaks false positives are handled by extending the `.gitleaks.toml`
  allowlist in a reviewed commit — never by ignoring the finding at run time.

## 4. License allowlist

Allowed for the shipped binary's dependency graph: MIT, BSD-2-Clause,
BSD-3-Clause, Apache-2.0, ISC. Anything else (including unknown/undetected) is
a medium blocking finding for T3 triage. The current pinned graph
(`docs/dependencies.md`, ADR-0007) is MIT/BSD/Apache-only.

## 5. Secret-scan policy and rotation protocol

- Policy file: `.gitleaks.toml` (frozen with this document): upstream default
  ruleset extended with the `AMUXTEST_` derived-candidate-secret rule; only
  `{{SECRET:label}}` placeholders and `[REDACTED:label]` markers are
  allowlisted secret-shaped strings.
- Derived-secret discipline (`internal/securitytest/secrets.go`): fixtures
  exercise real credential-shaped values, but every value is derived at run
  time via `DeriveCandidateSecret(label)`; durable artifacts carry only
  placeholders. `TestRedactionFixturesContainNoRawSecrets` walks
  `docs/security/` and `testdata/security/` on every test run proving neither
  a derived value nor an `AMUXTEST_` remnant ever landed on disk.
- **Rotation protocol on a confirmed hit:** (1) treat the credential as
  compromised at first sight — rotate/revoke at the issuer immediately, before
  any git surgery; (2) enumerate uses of the credential and re-issue; (3) only
  then decide history rewrite (usually not worth it once rotated); (4) add a
  regression rule or allowlist entry to `.gitleaks.toml` in a reviewed commit;
  (5) record the incident and the audit trail of the rotation in the run's
  evidence path. Secrets are invalidated, never "scrubbed".

## 6. Check receipts

Every executed manifest check emits an `amux.security.check-receipt.v1` record
(schema frozen in `readiness-manifest.json`): `check_id`, `command`,
`exit_code`, `outcome` (`pass` | `fail` | `deferred_prerequisite`),
`started_at`, `host_os`, `host_arch`, `tool_version`, `evidence_path`,
`notes`. Receipts are the release-promotion evidence T6 hands to
`guild:verify-done`; a missing receipt equals a failed check.

**Binding and skip rules (G-lane F5 amendment, 2026-07-17).** `go test` exits
0 when a `-run` pattern matches nothing and when every matched test skips, so
exit code alone can never justify `outcome: pass`:

- Static half (enforced in code): `TestManifestRunPatternsBindToRealTests`
  (`internal/securitytest/gatebind.go`) fails the blocking
  `security-contract-self-gates` check whenever any manifest `-run`
  expression binds to zero test functions under its build tags on the Linux
  target. A phantom pattern can no longer be frozen. The retired
  always-skipping `TestSecondUIDVariantsDeferred` stub is pinned retired by
  `TestRetiredForeignUIDStubStaysRetired`.
- Runtime half (receipt discipline): a `pass` receipt for a `-run` check must
  be generated from `-v` output and record in `notes` the number of executed
  top-level tests (`=== RUN` count ≥ 1) and the skip count for the bound
  pattern. Zero executions ⇒ `fail` (vacuous). Skips are `pass`-compatible
  only when the skip is a declared prerequisite of the check (e.g. the
  second-UID harness without root) — then the outcome is
  `deferred_prerequisite`, never `pass`. An undeclared skip ⇒ `fail`.
- A check may not be satisfied by a differently-named test "close enough" to
  the pattern: the command in the receipt must be byte-identical to the
  manifest command, and the manifest command is what the self-gate binds.
- Recorded-evidence half (G-lane F2 remediation, 2026-07-17, enforced in
  code): `TestRecordedReceiptsBindToCurrentTests`
  (`internal/securitytest/receipts.go`) re-validates every receipt recorded
  at a frozen evidence path against the CURRENT tree on each run of the
  blocking `security-contract-self-gates` check. A `pass` receipt for a
  `-run` check fails when its command drifts from the manifest, when its
  notes or evidence transcript name a pattern-matching test that no longer
  binds to a real test function (retired or nonexistent — the stale-evidence
  class), when the transcript shows zero substantive top-level `PASS` lines
  for the bound pattern, or when it records a top-level skip not explicitly
  declared by name in the receipt notes. Stale evidence can therefore never
  outlive the tests it once described.

## 7. Security design review (backend-dispatch gate result)

Reviewed against the T1 receipt and frozen seams (`internal/platform`,
`api/v1`, ADRs 0001–0007):

- The complete transport seam (`LocalTransport`/`LocalConn.Control` feeding
  `PeerCredentials.PeerUID`) supports mandatory pre-protocol peer checks
  (STR-2) — no contract gap.
- `DescriptorLaunch` (OpenBound/LaunchBound) supports descriptor-bound
  validate+exec (HA-10..HA-13) — no contract gap.
- The ADR-0005 authority table (trust state SQLite-only, snapshots never
  import it) supports HA-18..HA-21 restore semantics — no contract gap.
- **Findings:** none high. One low-severity operational finding: `go.sum`
  currently carries a few unpruned hash lines (`go mod tidy -diff` nonzero;
  `go mod verify` passes, so integrity holds). Owned by T4/T3 the next time
  the module graph is touched; tracked as a followup, not a waiver.
- Residual risks RR-1..RR-5 (threat-model §6) are all ≤ medium; no
  high-severity residual was accepted, so no orchestrator confirmation was
  required.

## 8. Malicious fixture inventory

Executable via `securitytest.RunConformance` (skips with an explicit
prerequisite until T4 registers a real `Factory`; the vectors themselves are
validated on every test run today):

| Family | Vectors | Pins |
|--------|---------|------|
| `timing.*` (`testdata/security/fixtures/timing.json`) | absent-trust, revoke-cancel, revoke-first, launch-first | 250 ms gates, HA-14/HA-15 orderings, zero-children, 2 000 ms kill boundary, AUD-4 trails |
| `races.*` (`fixtures/races.json`) | symlink-swap, rename-swap, exec-byte-replace, config-byte-replace, project-root-replace | HA-10/HA-11/HA-13: approved object executes or launch fails closed; substituted digest never in the child ledger |
| `restore.*` (`fixtures/restore.json`) | epoch-decrease, grant-reactivate, audit-erase, launch-authority | HA-18..HA-21 forged-generation rejection |
| `redaction.*` (`fixtures/redaction.json`) | all 11 RED-1 egress contexts + truncation-boundary | RED-1/RED-2/RED-5; goldens placeholder-only |
| trust matrix (`testdata/security/trust-matrix.json`, generated) | 41 rows | full decision/error-code table, §8 taxonomy |

No fixture executes a real hook: conformance wiring uses a spy behind
`platform.DescriptorLaunch`/`platform.PTY` (harness contract, `contract.go`).

## 9. Manual misuse cases

The red-team walk is `threat-model.md` §5, rows AB-1..AB-12 — one recorded
outcome per row against the release candidate (manifest check
`manual-misuse-review`). Additions to the abuse-case table require a
threat-model amendment, keeping the manual checklist and the model in one
place.

## 10. Host limitations at freeze time (honest record)

Author host: macOS darwin/arm64, go1.26.5, 2026-07-15. Executed here:
`go test ./internal/securitytest/` (self-gates green, conformance skipped with
prerequisite), `go mod verify` (pass), `go mod tidy -diff` (nonzero — §7
finding), repo-wide remnant sweep for `AMUXTEST_[0-9a-fA-F]{8,}` and private
key blocks (no matches; file names only, no values captured). Not executable
here and deferred with reproducible commands: `govulncheck`, `gitleaks`,
`go-licenses` (tools not installed on the authoring host; commands frozen in
§2 and the manifest), `integration-second-uid` and
`integration-resource-exhaustion` (require Linux + a second UID; T3 provides
runners, T6 executes). The git history is currently empty (repo not yet under
version control at freeze time), so the history half of the secrets scan
becomes meaningful at first commit — the working-tree half runs regardless.
No clean scan is claimed for any deferred check.
