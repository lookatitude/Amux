# ADR-0001 — Authority model and package boundaries

- Status: Accepted
- Date: 2026-07-15
- Deciders: architect (T1)
- Significance: high (foundational; expensive to reverse)
- Supersedes: none
- Enforced by: `internal/archtest` (`go test ./internal/archtest/`)

## Context

Amux is a clean-room, Go-authoritative, Linux-first workspace runtime. The spec
and PRD require exactly one durable state authority (the daemon) with no second
authority in the CLI, TUI, or persistence layers, and an inward-only package
dependency graph so the domain model never couples to transport, storage, or UI
concerns. This ADR freezes the actor topology, the cross-actor ordering rules,
the package map, and the import direction that every downstream lane must obey.

## Decision drivers

- One authority (spec constraint "architecture"; PRD principle 1).
- No nested actor waits / no deadlock under concurrent sessions sharing a project.
- A domain model that is deterministic, testable, and free of I/O so property and
  replay suites can stand on it.
- Import direction enforceable as a build-time gate, not prose.

## Decision

### Actor topology

- **Daemon-global control actor** (`internal/control`) is the single owner of the
  session registry, project identities, hook grants, monotonic project trust
  epochs, launch authorization, and cross-session revocation. It runs one
  goroutine and serializes every trust/registry transition.
- **Per-session graph actor** (`internal/session`) owns one session's workspace
  graph (`internal/domain` state), its revision counter, and its event-sequence
  counter. One goroutine per session; all graph mutation flows through it.
- **Peripheral producers** (PTY readers, clients, timers, persistence, hook
  workers, context collectors) never mutate owned state directly; they submit
  immutable messages to the owning actor.

### Cross-actor ordering (happens-before)

1. A session actor MUST NOT hold mutable session state while synchronously
   waiting on the control actor.
2. The control actor MUST NOT wait on a session actor while a trust transition is
   open.
3. Cross-session effects use one-way messages plus revision/epoch checks, never
   nested locks. This is proved as an executable model in `internal/ordering`
   (`TestTwoSessionsShareProjectNoPostRevokeLaunch`, run under `-race`).

### Event allocation

An event sequence is allocated ONLY after a command commits (ADR-0004). Rejected
commands allocate nothing and therefore never create a gap
(`internal/ordering` `TestEventSequenceMonotonicContiguous`).

### Package map and import direction

Imports point inward toward contracts. Canonical tree (PRD "Repository and
package architecture"):

```
cmd/amuxd, cmd/amux            entry points (skeletons at T1)
api/v1/                        wire contract: framing, envelopes, error taxonomy, golden vectors
internal/domain/              IDs, graph, commands, events, invariants  (inward-most; imports only stdlib + google/uuid)
internal/control/             global registry, trust epochs, launch serialization
internal/session/             per-session graph actor / event loop
internal/protocol/            framing/codec glue over api/v1
internal/transport/local/     Unix socket listener, peer validation, permissions
internal/client/              shared CLI/TUI protocol client and recovery
internal/pty/                 platform-neutral PTY interface + Unix impl
internal/terminal/            raw replay ring, ANSI parser adapter, cell engine
internal/attach/              replay/live cutover, input leases
internal/snapshot/            versioned JSON, atomic I/O, migration/restore
internal/store/               SQLite migrations and repositories
internal/hooks/               identity, grants, trust epoch, execution, audit
internal/notify/              notification store and delivery adapters
internal/context/             cwd/git/process/agent collectors
internal/tui/                 Bubble Tea models, geometry, rendering, keymaps
internal/config/              JSONC, schema, XDG resolution
internal/observability/       slog, metrics, profiling, diagnostics
internal/platform/            narrow OS interfaces (PTY, containment, peercred, fsid, launch, clock)
internal/persist/             persistence/restore contract types (ADR-0005)
internal/ordering/            executable ADR-0004 ordering model (proof)
internal/testkit/             fakes, barriers, fixtures, fault injection
packaging/aur/                PKGBUILD template and install metadata
docs/adr/                     frozen decisions
```

**Interface-exposing vs. implementation-detail packages.** `api/v1`,
`internal/domain`, `internal/platform`, and `internal/persist` expose stable
interfaces/types other packages depend on. `internal/transport`,
`internal/store`, `internal/snapshot`, `internal/pty`, `internal/terminal`,
`internal/tui`, and provider adapters are implementation details: nothing inward
of them may import them.

**Hard rule (enforced):** `internal/domain` imports only the Go standard library
plus the allowlisted `github.com/google/uuid`. It imports no other Amux package.
`tui` imports only client-facing immutable types. Provider adapters import the
adapter contract, not domain internals.

### Prohibited duplicate authorities

- No CLI-only or TUI-only durable mutation; both call the same protocol method.
- No second state authority in persistence: SQLite and snapshots are derived
  durability, not parallel authorities (ADR-0005 assigns each field one owner).
- No Go/Rust mux split; Go is the sole authority.

## Ownership table (summary; full durability rules in ADR-0005)

| Data | Owning actor |
|---|---|
| Session registry, project trust epochs, hook grants/audit | control actor |
| Workspace graph + stable IDs + event sequence | session actor |
| PTY/process identity | PTY supervisor (session-scoped) |
| Raw output ring | surface (session-scoped) |
| Attachments / input leases | attach manager (ephemeral) |
| Notifications | notification service |

## Consequences

**Positive**

- Import direction is a CI gate (`archtest`), so drift is caught mechanically.
- The no-nested-wait rule is proved executably, de-risking the hardest
  concurrency property before backend code exists.
- The domain is pure and deterministic, enabling property/replay/snapshot suites.

**Negative**

- Cross-session effects via messages + epoch checks are more verbose than shared
  locks; the ordering model documents the required discipline.
- One goroutine per session bounds per-session parallelism; acceptable for a
  local single-user runtime and revisitable behind the actor interface.

## Alternatives considered

- **Shared-lock graph with a global mutex** — rejected: invites nested-wait
  deadlock across the trust/graph boundary and is hard to prove correct.
- **Domain imports a small "events bus" package** — rejected: any inbound edge
  into domain erodes determinism and the archtest guarantee.

## Follow-ups

- T4 backend implements `internal/control` and `internal/session` against the
  `internal/ordering` model and must keep those proofs green.
- `internal/archtest` gains per-layer rules as outer packages land (the current
  test already forbids every inbound edge into `domain`).
