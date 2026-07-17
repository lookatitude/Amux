---
schema_version: guild.handoff_receipt.v1
task_id: T1-architect
run_id: run-8c2926bb-2ebe-4166-8b77-84c0de3e12c0
specialist: architect
tier: powerful
status: done
completed_at: 2026-07-15
resume: true
rework_round: 1
host:
  selected: claude-code-cli
  degraded: false
  independence: weak
---

# T1-architect handoff receipt — G-lane round-1 rework (2026-07-15)

This receipt replaces the round-1 receipt after completing the mandatory G-lane
rework. The Codex review (`review/G-lane:T1-architect/result-1.json`, finding
F1, blocking) was valid: ADR-0006 and the approved plan freeze PTY,
local-transport, and notification seams, but `internal/platform/platform.go`
materialized only six of the nine capability interfaces. That gap is now
closed. Scope was exactly the F1 fix brief: interface contracts + seam-freeze
tests + a signature-clarifying ADR amendment. No backend transport, PTY
runtime behavior, notification storage, or TUI work was implemented; no
T2–T6 work was absorbed; no approved semantics or platform-support claims
changed.

## changed_files (this rework round)

- `internal/platform/platform.go` (edited, now 263 lines) — added the three
  missing implementation-neutral seams, cohesive with the existing six:
  - `PTY` / `PTYSpec` / `PTYSize` / `PTYExit` / `PTYHandle` — the frozen
    spawn/resize/input/output/signal/reap surface; `PTYHandle.MasterFD()`
    exists solely to feed the pre-existing
    `ProcessInspector.ForegroundPID(ptyFD uintptr)`.
  - `LocalTransport` / `TransportSpec` / `LocalListener` / `LocalConn` —
    owner-only control-socket lifecycle; `LocalConn.Control(func(fd uintptr)
    error)` exposes the raw descriptor solely for the pre-existing
    `PeerCredentials.PeerUID(rawConnFD uintptr)` check.
  - `Notifier` / `Notification` / `NotifyUrgency` — best-effort desktop
    delivery whose errors are advisory and never mutate the daemon-owned
    store (ADR-0005 authority preserved).
- `internal/platform/seam_test.go` (new, 146 lines) — executable freeze of the
  COMPLETE ADR-0006 seam: compile-time references to all 13 frozen interface
  types (deleting/renaming one breaks the build), compile-time
  `io.Reader`/`io.Writer` assertions on `PTYHandle`/`LocalConn`, plus two
  reflection tests — `TestSeamSetIsComplete` (exact frozen-set membership) and
  `TestSeamShapesAreFrozen` (exact method names + signatures per interface).
  Omission and incompatible shape drift now fail `go test`.
- `docs/adr/0006-platform-interfaces.md` (amended, now 145 lines) — recorded
  the frozen type names/signatures for PTY, LocalTransport, and Notifier;
  added the seam-freeze tests to "Enforced by"; added an amendment line. No
  decision, semantics, or platform-support change.
- This receipt file (final filesystem action).

Prior-round artifacts (ADRs 0001–0005, 0007, `docs/dependencies.md`, the A6
spike evidence, module/skeletons/fixtures) are unchanged and remain as
described in the superseded receipt's history, summarized under
"carried_forward" below.

## carried_forward (verified still green this round)

ADRs 0001–0007; pinned `go.mod`/`go.sum` + toolchain; buildable `cmd/amuxd` /
`cmd/amux`; `internal/domain` property tests; `api/v1/testdata` golden
vectors; ordering/lease contracts; persistence contracts; archtest dependency
gate; `docs/dependencies.md` manifest (its `internal/platform.PTY` pointer is
now a real symbol); A6 spike evidence with explicit Linux-only deferrals.

## decisions

- Seams live in `internal/platform/platform.go` itself (not a sibling file) so
  ADR-0006's "the interface set (`internal/platform/platform.go`)" statement
  stays literally true.
- `PTYHandle`/`LocalConn` embed `io.Reader`/`io.Writer`: the daemon's event
  pipeline and protocol codecs consume plain byte streams; no platform type
  crosses the seam.
- Raw-descriptor access uses a scoped callback (`Control(func(fd uintptr)
  error)`) mirroring `syscall.RawConn` semantics without importing `syscall`,
  keeping OS types below the seam while enabling the mandatory SO_PEERCRED
  check.
- `PTYExit{Code, Signal}` is the implementation-neutral exit classification;
  signal deaths carry the signal name and mark `Code` untrusted.
- No fail-closed constructors were added for the three new seams: the rework
  contract forbids implementing their mechanisms (T4), and constructors
  without implementations would be dead API. The interfaces alone are the
  frozen contract; `unsupported_linuxonly.go` is untouched.

## assumptions

- Freezing exact method signatures in `seam_test.go` is the intended
  "compile-time/unit tests that make the complete seam durable" — the test
  message directs any future change through an ADR-0006 amendment, matching
  the plan's confirmation rules.
- `os.Signal` (stdlib interface) in `PTYHandle.Signal` does not count as an
  OS-specific type leak; it is Go's portable signal abstraction.
- No ask-gate fired: nothing here changes the object model, persisted
  contract, trust semantics, supported platforms, cgo policy, or acceptance
  thresholds.

## evidence

All commands run 2026-07-15 on the author host (macOS darwin/arm64, go1.26.5),
after the rework edits:

- `gofmt -l .` → no output (clean).
- `go vet ./...` → clean; `GOOS=linux GOARCH=amd64 go vet ./...` → clean.
- `go test -count=1 ./...` → 79 tests pass in 14 packages (77 prior + 2 new
  seam-freeze tests).
- `go test -race -count=1 ./...` → 79 tests pass in 14 packages.
- `go test -count=1 ./internal/archtest/` → 3 tests pass (domain import rules,
  forbidden inbound edges, NoCgo — new test file is stdlib-only, no new
  dependency edges).
- `GOOS=linux GOARCH=amd64 go build ./...` and `GOOS=linux GOARCH=arm64 go
  build ./...` → both succeed (compile-only; no Linux runtime claim).
- Coverage of F1's exact gap: `internal/platform/seam_test.go` frozen table
  now includes `PTY`, `PTYHandle`, `LocalTransport`, `LocalListener`,
  `LocalConn`, `Notifier` alongside the original six seams; `grep -n "type
  \(PTY\|LocalTransport\|Notifier\)" internal/platform/platform.go` resolves.

Honest deferrals unchanged: `spikes/containment` and `spikes/launch` runtime
behavior remains deferred to a Linux host (T3 CI); no Linux runtime claims are
made from macOS. The three new seams have frozen contracts only — their Linux
mechanisms are T4 scope by design, not an omission.

## risks

- Signature-level freezing is strict: T4 may surface a legitimately better
  shape (e.g. context-aware `Accept`). That is intentional friction — the
  escape path is an ADR-0006 amendment under the spec's confirmation rules,
  not silent drift.
- The seam-freeze test cannot verify future *implementations* honor documented
  behavior (stale-socket proof, advisory notify errors); T2 fixtures and T4
  tests own that.

## followups

- T2 security: S3/S4 adversarial fixtures consume the frozen containment/
  launch designs and now also the complete transport seam (owner-only socket +
  `Control`-fed SO_PEERCRED path).
- T3 devops: run deferred Linux spike harnesses + full suite/race on the
  Arch/Ubuntu matrix (unchanged).
- T4 backend: implement `PTY` (creack/pty), `LocalTransport`
  (`internal/transport/local`), and `Notifier` (`internal/notify`) behind the
  frozen seams; SQLite selection within the cgo prohibition (unchanged).
- T5 terminal-ui: Bubble Tea pin + manifest regeneration (unchanged).
- Fresh checksum-bound G-lane review required before downstream dispatch.

## learnings

- A reviewer diffing ADR prose against declared Go symbols catches "named in
  the ADR, mechanism later" gaps that a green test suite cannot — interfaces
  that exist only as prose have no failing test. Freezing a contract must
  mean declaring it in code the same round.
- A reflection-based seam-freeze test (method-set + exact signature strings)
  turns an ADR's interface inventory into a standing gate for ~100 lines, and
  its failure message can route future edits through the ADR-amendment
  process.
- Designing late seams against already-frozen neighbors (uintptr-fd parameters
  in `ProcessInspector`/`PeerCredentials`) dictates the handle surface
  (`MasterFD`, `Control`) — cohesion falls out of reading the existing
  contracts first.

```guild.handoff.v2
{
  "schema_version": "guild.handoff.v2",
  "task_id": "T1-architect",
  "tier": "powerful",
  "status": "done",
  "summary": "G-lane round-1 rework complete: closed review finding F1 by materializing the three missing ADR-0006 seams — PTY (PTY/PTYSpec/PTYSize/PTYExit/PTYHandle with MasterFD feeding ProcessInspector), LocalTransport (TransportSpec/LocalListener/LocalConn with Control(fd) feeding PeerCredentials.PeerUID), and Notifier (Notification/NotifyUrgency, advisory-error semantics preserving ADR-0005 store authority) — in internal/platform/platform.go; added seam_test.go freezing the complete 13-interface seam by compile-time reference plus reflection over exact method names/signatures (omission or shape drift now fails go test); amended ADR-0006 with the frozen signatures only (no semantic or platform-support change). Full gate green on author host: gofmt clean; go vet clean (darwin+linux); 79 tests pass ./... and with -race; archtest 3 pass; GOOS=linux amd64/arm64 compile-only builds succeed. No backend, PTY-runtime, notifier-runtime, or TUI implementation; no T2-T6 absorption; no prior-ADR semantic edits. Downstream dispatch awaits a fresh checksum-bound G-lane review.",
  "artifacts": [
    "internal/platform/platform.go:1-263",
    "internal/platform/seam_test.go:1-146",
    "docs/adr/0006-platform-interfaces.md:1-145",
    ".guild/runs/run-8c2926bb-2ebe-4166-8b77-84c0de3e12c0/handoffs/architect-T1-architect.md:1-186"
  ],
  "issues": [],
  "learnings": [
    "Interfaces that exist only as ADR prose have no failing test; a green suite cannot catch a contract that was never declared in code. Freezing a contract must mean declaring it the same round, plus a gate that fails on omission.",
    "A reflection-based seam-freeze test (exact method-set + signature strings per interface) converts an ADR interface inventory into a standing ~100-line gate whose failure message routes changes through the ADR-amendment process.",
    "Late seams should be designed against already-frozen neighbors: the pre-existing uintptr-fd signatures of ProcessInspector and PeerCredentials dictated PTYHandle.MasterFD() and LocalConn.Control(), giving the new contracts cohesion for free."
  ],
  "notes": "Narrow G-lane rework only; exact scope of review finding F1. Receipt replacement was the final filesystem action.",
  "injection_clean": "clean"
}
```
