# ADR-0004 — Event, attach, and launch/revoke ordering

- Status: Accepted
- Date: 2026-07-15
- Deciders: architect (T1)
- Significance: high
- Enforced by: `internal/ordering` executable model (`go test -race ./internal/ordering/`)

## Context

Amux's correctness rests on ordering guarantees: events must be gap-free and
allocated only after commit; an attaching client must see a clean snapshot →
replay → live cutover with no gap or duplicate; input leases must admit exactly
one writer; and hook launch/revoke across two sessions sharing one project must
linearize so no hook launches after a completed revoke. This ADR states the
happens-before relationships and proves the load-bearing ones as an executable
model **before** the backend implements them.

## Decision drivers

- No silent state drift; gaps are explicit typed boundaries (spec success
  criterion 9; PRD principle 5).
- Detach is not stop; one writer per surface (spec attach/detach contract).
- No hook launch after revocation; both legal orderings are audit-visible (spec
  "Linearizable hook launch contract").
- Prove the concurrency, don't just assert it.

## Decision

### Event sequencing

- Each session actor owns its `seq`. A sequence is allocated **only after a
  command commits**; a rejected command allocates nothing, so sequences are
  strictly monotonic and gap-free. Proof: `TestEventSequenceMonotonicContiguous`
  (16 goroutines × 50 submissions, 1-in-5 rejected, log is `1..N` contiguous).
- Events carry `boot_id` + `session` + `seq`; a subscriber tracks that total
  order. A detected discontinuity is a typed `event_gap` recovery boundary
  requiring a fresh snapshot and a new cursor — never a silent bridge.

### Attach replay/live cutover

- Attach delivers (1) an atomic metadata + cell snapshot taken at output sequence
  `N`, (2) bounded raw replay ending exactly at `N`, then (3) ordered live output
  strictly `> N`. The concatenation must be contiguous and duplicate-free.
- Proof: `AttachCutover` + `TestAttachCutoverContiguous` /
  `TestAttachCutoverDetectsGaps` — a live frame `≤ N`, a replay short of `N`, or a
  missing sequence all report `ErrGap` (the `replay_gap`/`event_gap` boundary).
- Slow-consumer boundary: a lagging attachment is disconnected with its last
  delivered sequence and must reattach via bounded replay or snapshot-on-gap
  (mechanism implemented in T4 B4/B7; the ordering contract is frozen here).

### Input-lease state machine

- Output is shared; input requires a single per-surface lease
  (`internal/ordering/lease.go`). States: free ↔ held(client). Transitions:
  `Acquire` (free → held; never implicit takeover of another holder), `Takeover`
  (deliberate, evented), `Release`/`Disconnect` (holder → free). A non-holder
  write is rejected (`not_input_lease_holder`) before reaching the PTY. Detach
  releases the lease but never stops the PTY. Proof: `TestLeaseStateMachine`.

### Global-control / session ordering and hook launch/revoke

- The control actor is the single linearization point for launch authorization
  and revocation (ADR-0001). It processes `Authorize` and `Revoke` messages on
  one goroutine, comparing the requested epoch against the current epoch under
  its own goroutine.
- **Revoke-first** → the launch authorization at the pre-revoke epoch fails →
  **zero children** (`TestRevokeFirstCreatesNoChild`).
- **Launch-first** → authorization succeeds (one child); a later revoke bumps the
  epoch and clears trust, so no further launch succeeds and the runtime drives
  terminate → kill-after-2s for the in-flight child
  (`TestLaunchFirstThenRevoke`).
- **Two sessions sharing one project**: a `-race` stress test races 2×200
  authorizations against one revoke and asserts (a) no data race, (b) the audit
  child tally equals the authorized replies, and (c) after the revoke completes,
  no stale-epoch launch can succeed
  (`TestTwoSessionsShareProjectNoPostRevokeLaunch`). This establishes the
  "no launch linearizes after revocation" guarantee by construction plus proof.

### Ownership of mutable counters and trust epoch

- Event `seq` and workspace `rev`: owned by the session actor, mutated only on
  its goroutine.
- Project trust epoch and launch authorization: owned by the control actor,
  mutated only on its goroutine. No nested waits (ADR-0001 rules 1–2).

## Scope and non-guarantees

The MVP guarantees no launch **linearizes** after revocation. It does NOT claim
retroactive zero-execution of an already-spawned hook, OS sandboxing, or network
isolation (spec). Those are explicit non-goals.

## Consequences

**Positive**

- The hardest concurrency properties are proven under `-race` before backend
  code exists; T4 implements against a green model and must keep it green.
- Gap handling is explicit and typed end to end.

**Negative**

- The single control-actor linearization point serializes trust transitions; at
  single-user scale this is not a bottleneck and simplifies the proof.

## Alternatives considered

- **Optimistic launch with post-hoc revoke reconciliation** — rejected: cannot
  guarantee "no launch after revoke" and complicates audit.
- **Per-session trust epochs** — rejected: a project spans sessions; trust must
  be global to the project identity, so the epoch lives in the control actor.

## Follow-ups

- T4 B4/B5/B7/B11 implement replay, PTY supervision, attach, and hook launch
  against this model. T2 S4 reviews the pre-spawn authorization seam.
