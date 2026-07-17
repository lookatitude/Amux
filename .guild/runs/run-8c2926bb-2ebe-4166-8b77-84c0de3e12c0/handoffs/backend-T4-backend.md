# T4 backend — handoff (reopened: G-lane review round 5, findings F5 + F6)

- task-id: T4-backend
- owner: backend
- depends-on: T1-architect (frozen contracts), T2-security (trust/redaction contracts)
- status: done (F5 atomic ring snapshot + F6 deterministic fake-PTY drain barrier; all accepted T4 behavior preserved)
- reopened-for: `.guild/runs/run-8c2926bb-2ebe-4166-8b77-<HIGH_ENTROPY_REDACTED>-lane:T4-backend/result-5.json` findings F5, F6 (both blocking; nothing else in that packet was in scope)

## Finding F5 — `ReplayRead` page + `latest_seq` now come from ONE ring snapshot

**Defect (confirmed red):** `Engine.ReplayRead` selected the page via
`Ring.ReplayLimitBytes` (lock 1) and then sampled `Ring.LatestSeq()` (lock 2).
A chunk appended between the two locks made an EMPTY page advertise
`latest_seq = <new seq>` and `next_seq = <new seq>+1`, so a client continuing
from `next_seq` skipped the newly appended first unseen chunk with no
`replay_gap` and no duplicate. The new race test failed against the old code
on its first run:

```
engine_test.go: empty page at cursor 38 advertised latest_seq 267 and
next_seq 268: sequences [38..267] would be silently skipped
```

**Fix (mechanism):**

- `internal/terminal/ring.go`: new `ReplayPage{Chunks, LatestSeq}` +
  `Ring.ReplayPageBytes(fromSeq, maxChunks, maxBytes) (ReplayPage, error)` —
  the selected whole-chunk page AND the exact latest sequence observed under
  the SAME `r.mu` acquisition. `ReplayLimitBytes` now delegates to it
  (`page.Chunks, err`), so `Replay`/`ReplayLimit`/`ReplayLimitBytes` semantics
  and every existing caller (attach fan-out pump included) are byte-for-byte
  preserved: cursor 0 → typed `ErrInvalidFromSeq`; cursor ahead of latest →
  empty page carrying the snapshot's `LatestSeq`; evicted cursor → typed
  `*ReplayGapError` (fields from the same snapshot); positive byte budget
  below the next whole chunk → typed `*BoundTooSmallError`; chunks are never
  split; page-proportional copying unchanged.
- `internal/daemon/surface.go` (`Engine.ReplayRead`): derives the result
  exclusively from that snapshot — `LatestSeq = page.LatestSeq`; non-empty
  page `NextSeq = last returned + 1`; empty page `NextSeq = page.LatestSeq+1`.
  The second `LatestSeq()` sample is gone. Flow-14 comment documents the
  invariant.
- `internal/rpcapi/rpcapi.go`: `ReplayReadResult` contract doc now states
  chunks/latest_seq/next_seq derive from one ring snapshot (doc-only change;
  no wire shape, no method, no protocol bump).

**Preserved semantics** (all pinned by tests): current cursor, ahead-of-latest
pull-back (`next_seq = latest+1`, never the overshoot cursor), partial page
(`next_seq = last returned + 1`), typed gap with structured
`ReplayGapDetails`, tiny bound typed with `ReplayBoundDetails`, negative
`max_bytes` typed, 512 KiB/4096-chunk server caps, 16 MiB retention floor,
concurrent-eviction continuation via typed gap.

**Red→green evidence (TDD; each new test was written and run before the fix):**

- RED `<HIGH_ENTROPY_REDACTED>`
  (internal/daemon/engine_test.go): metadata-only surface, writer appends
  30k chunks while a reader paginates; asserts the sharp invariant *empty page
  ⇒ `latest_seq` < cursor ∧ `next_seq` = `latest_seq`+1*, plus contiguous
  sequence truth and exact completion at seq 30000. **Failed against the old
  Engine on the first run** (output quoted above); green after the fix,
  10× under `-race`.
- `<HIGH_ENTROPY_REDACTED>` (deterministic barrier pin):
  append → page → empty-page snapshot → append → the appended chunk IS
  returned from the advertised `next_seq`, byte-checked; ahead-of-latest
  pull-back pinned in the same test.
- `<HIGH_ENTROPY_REDACTED>`: 24 MiB in 256 KiB
  appends races pagination through the 16 MiB floor; gaps must be typed and
  strictly forward (`oldest_retained > cursor`), empty pages must satisfy the
  snapshot invariant, pagination must land exactly on the final sequence.
- `<HIGH_ENTROPY_REDACTED>` (internal/terminal/ring_test.go):
  pins every cursor class of the new API — empty ring, invalid cursor 0,
  partial page, current cursor, append-after-empty-snapshot barrier,
  ahead-of-latest, tiny bound, eviction gap — with `LatestSeq` checked against
  the same snapshot each time. (Red as a compile failure before the API
  existed.)
- `<HIGH_ENTROPY_REDACTED>`: 4 seeded xorshift readers with
  randomized cursors/chunk-bounds/byte-bounds against a writer driving 30 MiB
  through the 16 MiB budget (concurrent eviction); per-call invariants:
  empty ⇒ `fromSeq > LatestSeq`; non-empty ⇒ starts at `fromSeq`, contiguous,
  ends ≤ `LatestSeq`; gap ⇒ `OldestRetained ∈ (fromSeq, LatestSeq]`.
  10× under `-race`.

Remaining production `LatestSeq()` call sites were audited: attach stall-lag
accounting (`internal/attach/surface.go`) and observability reads
(`InspectPane`, projections) — none pairs a page selection with a separate
latest sample; the attach fan-out appends and delivers under one lock by
design.

## Finding F6 — `spawnAndQuiesce` timing heuristic replaced with an exact lifecycle barrier

**Defect (confirmed red):** the fake PTY's `Wait()` returned immediately while
the supervisor drained output asynchronously in 32 KiB reads, and
`spawnAndQuiesce` declared quiescence after three unchanged 20 ms polls of
`InspectPane.LatestSeq`. A scheduling pause ≥ ~60 ms mid-drain (routine under
`-race` + full-suite load) let the 17 MiB gap test read before eviction had
happened — the recorded `evicted cursor: got <nil>` flake.

**Fix (no sleeps, no retries — an exact barrier):**

- `fakeHandle` (internal/daemon/engine_test.go) gained a one-shot `drained`
  channel, closed exactly once when `Read` first returns `io.EOF` — i.e.
  strictly after every output byte was handed to the supervisor's read loop.
  Because `readLoop → OnOutput → ring.Append` is synchronous in that same
  goroutine, observing `Drained()` guarantees ALL fake output is in the
  replay ring (happens-before via the channel close).
- `fakeHandle.Wait()` now blocks on that barrier, modeling the process/output
  lifecycle coherently (a real child blocks on PTY writes until the master
  drains), so the reap path never runs ahead of undelivered output. The
  channel is lazily created so the package's direct `&fakeHandle{…}`
  constructions (`argvEchoPTY`, `stayOpenHandle` — which overrides
  `Read`/`Wait`/`Close`) remain valid and unchanged in behavior.
- `fakePTY` records spawned handles (`lastHandle`); `spawnAndQuiesce` now
  blocks on `<-handle.Drained()` — the exact barrier — with a 2-minute
  failsafe that only bounds a genuinely hung drain (it is not part of the
  correctness argument). All `InspectPane` polling is gone.
- `fakePTY.stallAt/stallFor` injects one mid-drain read stall, giving the
  heuristic-vs-barrier difference a deterministic pin.

**Red→green evidence:**

- RED `<HIGH_ENTROPY_REDACTED>`: 1 MiB payload with a single
  400 ms stall at the 512 KiB offset; asserts the ring holds the full payload
  after `spawnAndQuiesce`. **Against the old heuristic it failed
  deterministically on the first run** (`after quiesce the ring holds 524288
  bytes, want the full 1048576-byte payload`); green with the barrier,
  10× under `-race`.
- Formerly flaky `<HIGH_ENTROPY_REDACTED>` (unchanged
  assertions; eviction of the 17 MiB payload through the 16 MiB floor is now
  guaranteed at read time by the barrier):
  - host darwin/arm64: `go test -race -count=50 -run
    <HIGH_ENTROPY_REDACTED> ./internal/daemon` → **50/50
    passed** — executed WHILE the full suite, full `-race` suite, 20-flow E2E,
    and `make verify` ran concurrently on the same host (genuine parallel
    load).
  - explicit parallel-load repeat: the same test `-race -count=20`
    concurrently with a full `go test -race ./internal/daemon` → both exit 0
    (`ok … 11.5s` / `ok … 104.0s`).
  - Linux containers: see gates below (20× arm64, 10× amd64, all green).

## Gates (author host darwin/arm64; Linux evidence under
`.guild/runs/run-8c2926bb-2ebe-4166-8b77-<HIGH_ENTROPY_REDACTED>-f5f6/`)

- Focused <HIGH_ENTROPY_REDACTED>: `go test ./internal/terminal
  ./internal/daemon` → 104 passed; focused RPC/client/CLI replay tests green;
  attach stress `go test -race ./internal/attach` → ok (the `ReplayLimit`
  pump path now rides the delegation).
- `go test -count=1 ./...` → **769 passed / 47 packages** (was 744/47 at the
  prior receipt; growth is the six new F5/F6 tests plus other lanes).
- `go test -race -count=1 ./...` → 769 passed / 47 packages.
- Formerly flaky test under `-race`: 50× focused + 20× under explicit
  concurrent-suite load + 20× Linux arm64 + 10× Linux amd64 → all pass.
- `go test -count=1 -tags integration -run 'ResourceExhaustion'
  ./internal/daemon` → PASS (production `Run` assembly; structured-details
  consumption unchanged).
- 20-flow E2E `TestTwentyFlows` (real binary + daemon + PTYs, including the
  flow-14 bounded-page/continuation/typed-bound steps) → PASS.
- `make verify` → exit 0 (fmt-check, vet, staticcheck, mod-verify, tidy-check,
  deps-manifest, license, generate-check, linkage fixtures, backup-restore).
- `go vet ./...` and `GOOS=linux go vet ./...` clean; gofmt clean on every
  touched file; `go mod verify` clean; `go mod tidy` no-op.
- `CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build ./...` and `…arm64` both
  succeed (no cgo).
- **Linux arm64 race** (`ubuntu:24.04` container, `go1.26.5 linux/arm64`,
  bind-mounted repo, `CGO_ENABLED=1` for the race detector only — a test-only
  exception to ADR-0007; shipped builds stay cgo-free): full
  `go test -race ./...` + 20× focused gap test —
  `evidence/T4-f5f6/linux-race-arm64-ubuntu-rerun.log`.
- **Linux amd64 race** (`ubuntu:24.04 --platform linux/amd64` under
  Rosetta/qemu, `go1.26.5 linux/amd64`): focused `-race` over the changed
  surfaces (`Replay|Ring|SpawnAndQuiesce`, internal/terminal +
  internal/daemon) + 10× gap test → all ok —
  `evidence/T4-f5f6/linux-race-amd64-ubuntu.log`. Honest boundary: the FULL
  suite under amd64 emulation was not run (hours under qemu); the scoping is
  recorded in the log itself.

**Honest boundaries / followups for other lanes:**

- `scripts/qa/linux-gates.sh` cannot run `-race` as shipped: it sources
  `scripts/tools.env` (pins `CGO_ENABLED=0`) and `go test -race` then aborts
  with `-race requires cgo` — first attempts are preserved at
  `evidence/T4-f5f6/linux-gates-{ubuntu,arch}.log`. The race evidence above
  therefore used direct `docker run` with the same image/recipe and
  `CGO_ENABLED=1`. Harness fix is T6-qa scope; flagged in `followups`.
- One transient failure of `securitytest.<HIGH_ENTROPY_REDACTED>`
  appeared during the FIRST arm64 container full-race run
  (`evidence/T4-f5f6/linux-race-arm64-ubuntu.log`): the loaded second-UID
  receipt carried the command variant `go test -count=1 -v -tags integration
  -run 'SecondUID' .<HIGH_ENTROPY_REDACTED>`, which exists only in the
  pre-existing QA artifact
  `.amux-<HIGH_ENTROPY_REDACTED>-security-linux/linux-gates-ubuntu.log`
  (recorded 2026-07-16, before this rework). The checked-in receipt matches
  the manifest byte-for-byte, the gate passes on the host, and it passes in
  the same container image run standalone — consistent with a stale
  bind-mount read of the pre-fix receipt content. Second-UID receipts are
  explicitly out of this lane's scope (T2-security); flagged in `followups`,
  nothing touched.

## Scope

Modified (production): `internal/terminal/ring.go` (ReplayPage +
ReplayPageBytes; ReplayLimitBytes delegates), `internal/daemon/surface.go`
(ReplayRead derives from one snapshot), `internal/rpcapi/rpcapi.go`
(ReplayReadResult contract doc only).
Modified (tests): `internal/daemon/engine_test.go` (fake PTY drained barrier +
stall injection + barrier/race/stress tests + spawnAndQuiesce rewrite),
`internal/terminal/ring_test.go` (snapshot-semantics + concurrent property
tests).
Did NOT modify: trust atomicity / control actor / trust store, second-UID
receipts and securitytest sources, GoReleaser/CI files, `scripts/qa/*`, TUI
packages, research, protocol version (doc-only rpcapi change; no new method,
no result-shape change), client, CLI, attach, supervisor, exhaustion
integration test.

```guild.handoff.v2
{
  "schema_version": "guild.handoff.v2",
  "task_id": "T4-backend",
  "tier": "mid",
  "status": "done",
  "summary": "Reopened T4 closed G-lane round-5 blocking findings F5+F6. F5: page selection and latest_seq are now ONE atomic ring snapshot — new terminal.ReplayPage + Ring.ReplayPageBytes returns the selected whole-chunk page plus the exact latest sequence under the same lock; ReplayLimitBytes delegates (all existing callers preserved); Engine.ReplayRead derives LatestSeq, partial-page NextSeq (last+1), and empty-page NextSeq (snapshot latest+1) only from that snapshot, so a chunk appended immediately after an empty-page snapshot always sits at or past the advertised next_seq — never silently skipped. Red proven by a new race test against the old Engine (empty page at cursor 38 advertised latest_seq 267); green with deterministic barrier, concurrent-eviction stress, and seeded-property ring coverage, all repeated under -race. Current/ahead-of-latest/partial/gap/tiny-bound/concurrent-eviction cursor semantics preserved and pinned. F6: the spawnAndQuiesce timing heuristic is gone — the fake PTY handle now closes a one-shot drained channel when Read first returns EOF (strictly after the synchronous readLoop→OnOutput→ring.Append chain delivered every byte), fake Wait blocks on that barrier so process/output lifecycle is coherent, and the helper waits on the exact barrier; a deterministic 400 ms mid-drain stall test failed the old heuristic (ring held 512 KiB of 1 MiB) and is green with the barrier. Formerly flaky <HIGH_ENTROPY_REDACTED>: 50x -race focused (while full suite + full race + E2E + make verify ran concurrently), 20x -race under explicit concurrent daemon race suite, 20x Linux arm64 container race, 10x Linux amd64 (Rosetta) container race — all pass. Full suite 769/47 and full -race 769/47 green on host; ResourceExhaustion integration PASS; 20-flow E2E PASS; make verify exit 0; vet + GOOS=linux vet clean; tidy no-op; linux amd64+arm64 no-cgo builds green. Accepted F1 bounded-replay contract, structured gap/bound details, resource-exhaustion behavior, and root --version behavior all preserved (their pinning tests unchanged and green).",
  "artifacts": [
    "internal/terminal/ring.go",
    "internal/terminal/ring_test.go",
    "internal/daemon/surface.go",
    "internal/daemon/engine_test.go",
    "internal/rpcapi/rpcapi.go",
    ".guild/runs/run-8c2926bb-2ebe-4166-8b77-<HIGH_ENTROPY_REDACTED>-f5f6/linux-race-arm64-ubuntu-rerun.log",
    ".guild/runs/run-8c2926bb-2ebe-4166-8b77-<HIGH_ENTROPY_REDACTED>-f5f6/linux-race-amd64-ubuntu.log",
    ".guild/runs/run-8c2926bb-2ebe-4166-8b77-<HIGH_ENTROPY_REDACTED>-f5f6/linux-gates-ubuntu.log",
    ".guild/runs/run-8c2926bb-2ebe-4166-8b77-<HIGH_ENTROPY_REDACTED>-f5f6/linux-gates-arch.log",
    ".guild/runs/run-8c2926bb-2ebe-4166-8b77-<HIGH_ENTROPY_REDACTED>-T4-backend.md"
  ],
  "issues": [],
  "followups": [
    "T6-qa: scripts/qa/linux-gates.sh cannot execute -race — it sources scripts/tools.env (CGO_ENABLED=0) and go aborts with '-race requires cgo'; the pipe through tee also lets callers miss the non-zero exit. Needs a race lane that sets CGO_ENABLED=1 inside the container (test-only ADR-0007 exception) or an explicit refusal.",
    "T2-security: transient <HIGH_ENTROPY_REDACTED> failure in one containerized full-race run loaded a second-UID receipt command variant that exists only in the pre-existing 2026-07-16 QA artifact; checked-in receipt+manifest match and the gate passes standalone in the same image — likely stale bind-mount read; worth a look from the receipt owners."
  ],
  "learnings": [
    "Any response field pair that encodes 'you are current as of X' must be captured under the same lock as the emptiness decision itself; a second sample of X after the page returns is a TOCTOU hole that only concurrency tests expose — the sharp testable invariant is: empty page ⇒ advertised latest < cursor.",
    "Returning a snapshot struct (page + latest) and making the old API delegate to it closes the race without touching any existing caller — extend-and-delegate beats changing signatures when a lock-scope bug needs a wider atomic unit.",
    "A test-double must model lifecycle edges, not just data: a fake Wait that returns before the fake's output is drained lets the reap path race the read loop in ways no real PTY exhibits, and every timing heuristic built on top inherits that incoherence.",
    "Replace 'stopped changing for N polls' with a barrier the SUT itself signals (one-shot EOF close observed via channel happens-before); then prove the heuristic was the bug by injecting a stall longer than the old stability window — deterministic red beats statistical rerun arguments.",
    "A -race gate that silently can't run (CGO_ENABLED=0 in a sourced env file) looks green from the outside; race evidence must be validated by checking the log actually contains test results, not just a zero exit."
  ],
  "notes": "F5/F6 were the only round-5 blocking findings; both closed with red→green TDD evidence. The linux-gates -race incompatibility and the transient securitytest receipt read are recorded as followups for their owning lanes (qa/security), not fixed here. Remaining Ring.LatestSeq() production call sites audited: attach stall accounting and <HIGH_ENTROPY_REDACTED> observability only — no other page/latest pairing exists.",
  "injection_clean": "clean"
}
```
