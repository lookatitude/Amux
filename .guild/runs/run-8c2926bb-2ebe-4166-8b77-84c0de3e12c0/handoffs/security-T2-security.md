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
rework_round: 2
host:
  selected: claude-code-cli
  degraded: false
  independence: weak
---

# T2-security handoff receipt — G-lane rework round 2 (2026-07-17)

Round-2 review (`review/G-lane:T2-security/result-2.json`, verdict `issues`)
blocked the reopened T2 receipt on two findings. Both are closed here,
TDD-first, with the accepted replacement-discriminator and manifest-binding
design fully preserved: the frozen durable key definition, the
replacement-validation discriminator mechanism (`internal/platform/
validation*.go`), the AUD-2 audit vocabulary, and the F5 gate-binding
self-gates are all byte-for-byte untouched.

## F1 — audited project transitions are now durably atomic

Round 2 correctly showed the transition was split across independent durable
writes (`SaveProject`, then `DeactivateGrants`, then ignored `AppendAudit`):
a mid-sequence failure could leave disk at a bumped revoked epoch while
memory stayed approved (authorizable), and a retry could trip
`<HIGH_ENTROPY_REDACTED>` against the half-committed epoch.

One explicit transaction/commit primitive replaces that sequencing:

- `internal/control/store.go` — `TrustStore.ApplyTransition(TrustTransition)
  ([]string, error)` replaces `DeactivateGrants` on the seam. One
  `TrustTransition` carries the full next project record (state + monotonic
  epoch + replacement discriminator), the deactivate-all-grants flag, the
  main audit record, and the per-deactivated-grant `grant_inactive`
  template. The contract: everything commits together or not at all; the
  exact deactivated grant IDs are returned only after durable commit; audit
  failures abort the transition and are RETURNED, never ignored. A new
  `control.<HIGH_ENTROPY_REDACTED>` backs the memory path.
- `internal/store/trusttx.go` — `Store.<HIGH_ENTROPY_REDACTED>` executes the
  whole transition in ONE real SQL transaction (epoch-monotonicity and state
  vocabulary enforced at the storage layer as before; project update, active
  grant id capture + deactivation `ORDER BY id`, main audit insert, one
  `grant_inactive` insert per grant, commit). `<HIGH_ENTROPY_REDACTED>`
  is a deterministic, fail-closed-only test failpoint at the five stage
  labels `project-update | deactivate-grants | audit-main | audit-grant |
  commit`.
- `internal/control/store.go` — `memStore.ApplyTransition` gives the memory
  implementation equivalent all-or-nothing semantics: every precondition
  (registration, state vocabulary, strict epoch increase) is validated before
  the first mutation; all mutations then land under a single lock hold with
  deterministic (sorted) grant order matching SQLite.
- `internal/daemon/truststore.go` — the SQLite adapter maps the transition
  1:1 onto `store.<HIGH_ENTROPY_REDACTED>` (pure translation, no policy).
- `internal/control/actor.go` — `transitionProject` (approve / deny /
  revoke) and `<HIGH_ENTROPY_REDACTED>` (replacement invalidation) now
  build the `TrustTransition`, commit it durably, and only AFTER the commit
  update in-memory project/grant state and notify revoke listeners. On any
  failure, memory and disk both remain at the old approved epoch/state with
  grants active and zero partial audit; the retry re-derives the same epoch
  and succeeds. `appendAudit` and `RecordAudit` remain deliberately
  best-effort ONLY for records with no accompanying state transition
  (<HIGH_ENTROPY_REDACTED> denials, `grant_approved`, hook-lifecycle
  records) — that scope is now documented at both functions.
  `internal/control/doc.go` documents the seam change.

### Failure-injection proof (every stage, both paths)

- `internal/store/trusttx_test.go` — SQLite unit level:
  `<HIGH_ENTROPY_REDACTED>` injects a failure at each
  of the five stages and proves full rollback (old state/epoch/discriminator,
  grants active, zero partial audit), retry success with the SAME epoch (no
  `<HIGH_ENTROPY_REDACTED>`), exact post-retry audit count, and reopen
  (restart) rehydration of exactly the committed transition. Plus atomic
  commit shape, typed failures, audit shapes (audited approve / unaudited
  deny), and a stage-label pin so a refactor cannot silently drop a stage
  from coverage.
- `internal/control/transition_test.go` — memory path:
  `<HIGH_ENTROPY_REDACTED>` (all-or-nothing semantics,
  deterministic IDs, exact audit ordering/count) and actor-level injection
  via a failing-store wrapper for operator revoke, replacement invalidation,
  approve, and deny: on failure no observer can authorize from partially
  committed state (memory, durable store, and `AuthorizeLaunch` all agree on
  the old approved epoch), no listener fires, no partial audit exists;
  retries succeed at old-epoch+1 and listeners fire exactly once,
  post-commit.
- `internal/daemon/atomicity_test.go` — end to end, actor + REAL SQLite with
  the store failpoint: `<HIGH_ENTROPY_REDACTED>`
  and `<HIGH_ENTROPY_REDACTED>` inject at every stage
  under the real adapter and additionally prove restart rehydration after
  BOTH failure (fresh actor incarnation sees intact approved trust and
  retries the invalidation cleanly across the restart) and success (fresh
  incarnation sees the committed revocation and continues the monotonic
  epoch), with exact audit tails (`project_revoked` +
  `project_trust_required` on the invalidation path, then one
  `grant_inactive` per grant).

SQLite history and forward-only migrations are untouched (no schema change;
grants retained forever; audit append-only).

## F2 — fresh second-UID evidence + recorded-receipt validation

The frozen-path `integration-second-uid` receipt/evidence recorded the
retired `<HIGH_ENTROPY_REDACTED>` stub (an undeclared skip) and a
command that was not byte-identical to the manifest.

- Fresh exact-manifest runs against the CURRENT tree (post-F1 fix), executed
  2026-07-17 as root via `scripts/qa/linux-gates.sh` with `-v` added for the
  F5 receipt evidence: ubuntu:24.04 (`Ubuntu 24.04.4 LTS`, Linux
  6.12.76-linuxkit aarch64, native arm64, owner-only `TMPDIR=/qatmp`,
  go1.26.5) and archlinux (amd64 under emulation) —
  `.amux-<HIGH_ENTROPY_REDACTED>-seconduid-20260717/linux-gates-{ubuntu,arch}.log`,
  both `exit=0`, image digests recorded. Both show exactly the 4 real
  STR-3/STR-4 foreign-owner cases: 4 `=== RUN`, 4 PASS, 0 SKIP, 0 FAIL.
- `.amux-<HIGH_ENTROPY_REDACTED>-second-uid.txt` (frozen path)
  replaced with the Ubuntu `-v` transcript + environment line;
  `.amux-<HIGH_ENTROPY_REDACTED>-second-uid.receipt.json` replaced:
  command byte-identical to the manifest (`go test -count=1 -tags
  integration -run 'SecondUID' ./...`), `host_os: linux`, the actual
  container named in `tool_version`, substantive top-level RUN/PASS/SKIP
  counts in `notes`, zero reference to the retired stub.
- Receipt validation extended so stale evidence can never pass again:
  `internal/securitytest/receipts.go` (`<HIGH_ENTROPY_REDACTED>`,
  `ReceiptPathFor`, `CheckReceipt`) + `receipts_test.go`. For every recorded
  `pass` receipt of a `-run` check: the command must be byte-identical to
  the manifest; every pattern-matching test name in the receipt notes or
  evidence transcript must bind to a real test function under the check's
  tags on GOOS=linux (a retired/nonexistent name fails — the exact stale
  shape round 2 caught); the transcript must show ≥1 substantive top-level
  PASS of a bound test (no vacuous pass); an undeclared top-level skip of a
  pattern-matching test is refused (declared = named explicitly in the
  receipt notes, and declaration cannot resurrect a retired name because
  binding is checked first). `<HIGH_ENTROPY_REDACTED>` runs
  this over the frozen evidence paths inside the blocking
  `security-contract-self-gates` check; fixture tests pin each rule,
  including a byte-exact reproduction of the stale round-2 receipt failing.
- Under the extended gate, `security-conformance` evidence was regenerated
  from `-v` output (1 substantive PASS — the real internal/hooks conformance
  execution — plus the long-documented `<HIGH_ENTROPY_REDACTED>` placeholder
  skip, now declared by name in the receipt notes), and the
  `security-contract-self-gates` receipt/evidence were refreshed (13
  top-level tests: 12 PASS + 1 declared SKIP, including the three new
  receipt-validation gates).
- `docs/security/security-readiness.md` §6 — recorded-evidence half of the
  F5 rules appended (G-lane F2 remediation, 2026-07-17).

## changed_files

New: `internal/store/trusttx.go` (147), `internal/store/trusttx_test.go`
(263), `internal/control/transition_test.go` (299),
`internal/daemon/atomicity_test.go` (262),
`internal/securitytest/receipts.go` (168),
`internal/securitytest/receipts_test.go` (185),
`.amux-<HIGH_ENTROPY_REDACTED>-seconduid-20260717/`,
`.amux-<HIGH_ENTROPY_REDACTED>-trust-atomicity-20260717/` (container gate
logs); this receipt.

Modified: `internal/control/store.go` (TrustTransition +
<HIGH_ENTROPY_REDACTED> + memStore.ApplyTransition; DeactivateGrants removed
from the seam), `internal/control/actor.go` (transitionProject,
<HIGH_ENTROPY_REDACTED>, <HIGH_ENTROPY_REDACTED> scope docs),
`internal/control/doc.go`, `internal/store/store.go` (failpoint field),
`internal/daemon/truststore.go` (ApplyTransition adapter),
`docs/security/security-readiness.md` (§6 recorded-evidence rules),
`.amux-<HIGH_ENTROPY_REDACTED>-second-uid.{txt,receipt.json}`
(replaced), `.amux-<HIGH_ENTROPY_REDACTED>-conformance.{txt,receipt.json}`
(evidence regenerated with -v, declared skip),
`.amux-<HIGH_ENTROPY_REDACTED>-contract-self-gates.{txt,receipt.json}`
(refreshed for the new gates).

## evidence

All commands executed 2026-07-17 by this attempt on the post-fix tree;
nothing claimed from unrun evidence.

Host (macOS darwin/arm64, go1.26.5):

- Focused atomicity/failure tests: store 33 (incl. the 5-stage rollback
  matrix), control (incl. the 4 failure-injection actor tests), daemon
  (incl. both end-to-end atomicity tests) — all pass; race-enabled focused
  run of control/store/hooks/securitytest/transport = 142 pass.
- Exact manifest commands: `go test -count=1 .<HIGH_ENTROPY_REDACTED>` →
  13 top-level, 12 PASS + 1 declared SKIP, exit 0 (receipt refreshed);
  `go test -count=1 -run 'SecurityConformance' ./...` → 1 substantive PASS +
  1 declared SKIP, exit 0 (evidence regenerated with -v);
  `go test -count=1 -tags integration -run 'TrustMatrixReplay' ./...` →
  42 pass, exit 0; `go test -count=1 ./internal/archtest/` → 3 pass;
  `go test -count=1 ./internal/platform/` → 9 pass (seams frozen);
  `go mod verify` → all modules verified; `go mod tidy -diff` → clean.
- Full suite: `go test -count=1 ./...` → **772 pass in 47 packages, exit 0**.
- Full race suite: `go test -race -count=1 ./...` → **772 pass, exit 0**.
- `go vet ./...` clean; `GOOS=linux go vet -tags integration ./...` clean;
  `gofmt -l` clean.
- No-cgo Linux builds: `CGO_ENABLED=0 GOOS=linux go build ./...` green for
  amd64 AND arm64.

Linux containers (`scripts/qa/linux-gates.sh`, owner-only `TMPDIR=/qatmp`,
pinned go1.26.5, Docker 29.5.2, image digests in the logs):

- Second-UID gate (manifest command + `-v`), ubuntu arm64 native AND arch
  amd64: 4/4 real foreign-owner cases PASS, 0 SKIP, `exit=0` —
  `linux-seconduid-20260717/linux-gates-{ubuntu,arch}.log`; frozen-path
  receipt/evidence generated from the Ubuntu run.
- Focused container trust + atomicity gate, ubuntu (root, overlayfs):
  `-tags integration -run 'TrustMatrixReplay|<HIGH_ENTROPY_REDACTED>|
  <HIGH_ENTROPY_REDACTED>|<HIGH_ENTROPY_REDACTED>|RevokeFailure|
  ApproveDenyFailures|<HIGH_ENTROPY_REDACTED>|TrustRehydrates|
  AmbiguousPersisted|<HIGH_ENTROPY_REDACTED>|<HIGH_ENTROPY_REDACTED>|
  RevokeListener|<HIGH_ENTROPY_REDACTED>|TrustLifecycle'` →
  19 top-level PASS (41/41 matrix rows + all new atomicity/failure tests),
  0 FAIL, 0 SKIP, `exit=0` —
  `linux-trust-atomicity-20260717/linux-gates-ubuntu.log`.

## decisions

- One primitive, not a saga: the seam-level `ApplyTransition` carries the
  complete transition so the storage layer can make it a single SQL
  transaction; the actor no longer sequences durable writes at all.
- `DeactivateGrants` was REMOVED from the TrustStore seam rather than kept
  beside the primitive — no caller may reconstruct the split-write path.
- The failpoint lives inside the store, is fail-closed-only by construction
  (it can only abort a transaction, never skip a stage), and is pinned by a
  stage-label test so coverage cannot rot silently.
- `appendAudit`/`RecordAudit` remain best-effort ONLY for non-transition
  records, now documented at both functions; transition audit rides inside
  the transaction and its failure aborts the transition.
- Receipt skip rule: a top-level skip under `outcome: pass` must be declared
  by name in the receipt notes; binding is validated before declaration, so
  a retired stub can never be "declared" back to life. Prerequisite skips of
  the check's own substance remain `deferred_prerequisite`, never `pass`.
- Explicitly NOT touched, per mandate: backend replay/version
  (`internal/daemon/engine.go`, `surface.go`), release tooling/CI, TUI,
  research, the frozen key definition, `internal/platform/validation*.go`,
  and the F5 gatebind self-gates (extended alongside, not modified).

## assumptions

- The receipt-location convention `<evidence_path minus extension>
  .receipt.json` matches every receipt in `.amux-artifacts/security/` today;
  `ReceiptPathFor` freezes it. `.amux-artifacts/` stays gitignored, so the
  recorded-evidence gate validates whatever receipts exist locally and skips
  absent ones (a missing receipt already equals a failed check at promotion
  time under §6).

## risks

- The recorded-evidence gate scans receipt notes for pattern-matching test
  identifiers; prose naming a bound test in an unusual inflection is treated
  as a claim about that test (fail-closed direction — it can only demand the
  name bind, never weaken a gate).

## followups

- T4/qa: `<HIGH_ENTROPY_REDACTED>` and
  `<HIGH_ENTROPY_REDACTED>` (internal/daemon event engine)
  failed deterministically on the darwin host WHILE the qemu/arch container
  was saturating the machine, and pass 3/3 isolated and 772/772 full-suite
  (plus full `-race`) once load dropped — load-sensitive replay/ring timing
  in T4 territory, same defect class as the honest `race-full-suite` Linux
  FAIL receipt (which remains a truthful FAIL owned by backend; not
  overwritten here).
- Backend: `ApproveGrant` still pairs `SaveGrant` with a best-effort
  `grant_approved` audit record (no state transition splits, so no F1-class
  divergence, but if the audit contract is later tightened to grant records
  the same transactional primitive shape applies).
- T6-qa: `integration-resource-exhaustion` receipt validates against the
  current tree under the new gate but predates the F1 code change; rerun at
  promotion time with the rest of the manifest.

## learnings

- Fail-closed sequencing is not atomicity: three individually fail-closed
  durable writes still expose every prefix. The seam must offer a commit
  primitive so the actor cannot express the torn state at all.
- A failpoint inside the transaction plus a stage-label pin turns "we think
  it rolls back" into a per-stage proof that survives refactors.
- Receipts rot in the opposite direction from code: gate recorded evidence
  against the CURRENT tree, not just commands against the manifest.

```guild.handoff.v2
{
  "schema_version": "guild.handoff.v2",
  "task_id": "T2-security",
  "tier": "powerful",
  "status": "done",
  "summary": "G-lane rework round 2 closed both blocking findings from review/G-lane:T2-security/result-2.json. F1: every audited project trust transition (operator approve/deny/revoke and replacement invalidation) now commits as one explicit TrustStore.ApplyTransition unit — project state + monotonic epoch + replacement discriminator, deactivation of all active grants, the main audit record, and one grant_inactive record per affected grant land together or not at all. The SQLite adapter executes the whole transition in one real SQL transaction (store.<HIGH_ENTROPY_REDACTED>) and returns the exact inactive grant IDs only after commit; the memory store validates-then-mutates under one lock for equivalent all-or-nothing semantics; the actor updates in-memory state and notifies revoke listeners only after durable commit; audit failures on these transitions are returned, never ignored (<HIGH_ENTROPY_REDACTED> stay best-effort only for non-transition records, documented). Deterministic failure injection at all five transaction stages (store failpoint) on both memory and SQLite paths proves: no observer can authorize from partially committed state, retries succeed without <HIGH_ENTROPY_REDACTED>, epochs stay monotonic, exact audit ordering/count, listener notification only after commit, and restart rehydration after both failure and success. DeactivateGrants was removed from the seam so the split-write path cannot be reconstructed; SQLite history and forward-only migrations preserved; frozen key/discriminator design untouched. F2: the stale frozen-path integration-second-uid receipt/evidence was replaced from fresh exact-manifest runs (manifest command byte-identical, executed as root in ubuntu:24.04 arm64 and archlinux amd64 containers post-F1-fix): 4 real foreign-owner cases, 4 RUN/4 PASS/0 SKIP, exit 0, zero reference to the retired stub, actual container named. New recorded-evidence validation (securitytest/receipts.go + <HIGH_ENTROPY_REDACTED>, wired into the blocking self-gates check) re-validates every recorded pass receipt against the current tree: byte-identical command, pattern-matching test names must bind (retired/nonexistent names fail), substantive top-level PASS required, undeclared top-level skips refused; security-readiness.md §6 amended accordingly and the security-conformance + self-gates receipts refreshed to comply. Verified: focused atomicity/failure tests green (store 33, control, daemon end-to-end, 142 race-enabled focused), exact manifest commands green (self-gates 12 PASS+1 declared SKIP; conformance; trust-matrix-replay 42; archtest; seam-freeze; go mod verify; tidy -diff clean), full suite 772/47 pass, full -race 772 pass, vet + GOOS=linux -tags integration vet + gofmt clean, no-cgo linux amd64+arm64 builds green, and Linux container gates green: second-UID 4/4 both distros exit=0, focused trust+atomicity gate (TrustMatrixReplay 41/41 + all new atomicity tests) 19 PASS exit=0 under overlayfs as root.",
  "artifacts": [
    "internal/control/store.go:84-160,224-284",
    "internal/control/actor.go:258-321,325-390,652-702",
    "internal/control/doc.go:17-23",
    "internal/control/transition_test.go:1-299",
    "internal/store/trusttx.go:1-147",
    "internal/store/trusttx_test.go:1-263",
    "internal/store/store.go:53-62",
    "internal/daemon/truststore.go:96-137",
    "internal/daemon/atomicity_test.go:1-262",
    "internal/securitytest/receipts.go:1-168",
    "internal/securitytest/receipts_test.go:1-185",
    "docs/security/security-readiness.md:113-126",
    ".amux-<HIGH_ENTROPY_REDACTED>-second-uid.receipt.json:1-14",
    ".amux-<HIGH_ENTROPY_REDACTED>-second-uid.txt:1-11",
    ".amux-<HIGH_ENTROPY_REDACTED>-seconduid-20260717/linux-gates-ubuntu.log:1-97",
    ".amux-<HIGH_ENTROPY_REDACTED>-seconduid-20260717/linux-gates-arch.log:1-96",
    ".amux-<HIGH_ENTROPY_REDACTED>-trust-atomicity-20260717/linux-gates-ubuntu.log:1-210",
    ".amux-<HIGH_ENTROPY_REDACTED>-conformance.receipt.json:1-14",
    ".amux-<HIGH_ENTROPY_REDACTED>-contract-self-gates.receipt.json:1-14",
    ".guild/runs/run-8c2926bb-2ebe-4166-8b77-<HIGH_ENTROPY_REDACTED>-T2-security.md:1-330"
  ],
  "issues": [
    "internal/daemon <HIGH_ENTROPY_REDACTED> and <HIGH_ENTROPY_REDACTED> failed on the darwin host only while the qemu container saturated the machine; both pass 3/3 isolated and in the 772/772 full and full-race suites afterwards — load-sensitive T4 replay/ring timing, same class as the honest race-full-suite Linux FAIL receipt (left truthful, backend-owned, out of this lane's mandate)",
    "ApproveGrant still pairs SaveGrant with a best-effort grant_approved audit record — no state transition splits so no F1-class divergence, flagged for backend if the audit contract tightens to grant records",
    "integration-resource-exhaustion receipt validates under the new recorded-evidence gate but predates the F1 code change; rerun with the manifest at promotion time (T6)"
  ],
  "learnings": [
    "Fail-closed sequencing is not atomicity: individually-aborting durable writes still expose every prefix — the seam must offer a single commit primitive so the actor cannot express the torn state.",
    "A deterministic in-transaction failpoint plus a stage-label pin turns rollback claims into per-stage proofs that survive refactors.",
    "Recorded evidence rots against the tree, not the manifest: receipts must be re-validated against the CURRENT test surface so retired tests cannot keep vouching for a gate."
  ],
  "notes": "Accepted round-1 design preserved exactly: frozen durable key, replacement-validation discriminator (platform validation files untouched), AUD-2 vocabulary, F5 gatebind self-gates. No backend replay/version, release tooling/CI, TUI, or research edits. Frozen key/discriminator policy unweakened; grants history retained forever; audit append-only; migrations forward-only with no new schema change.",
  "injection_clean": "clean"
}
```
