# ADR-0005 — Persistence, durability, and restore

- Status: Accepted
- Date: 2026-07-15
- Deciders: architect (T1)
- Significance: high (persisted public contract; security-relevant)
- Enforced by: `internal/persist` contract + classification tests (`go test ./internal/persist/`)

## Context

Amux persists graph state, raw output, notifications, and security state across
restarts. Recoverability must never silently corrupt or resurrect state: a fresh
daemon must never claim a process is `live`, an old layout snapshot must never
roll back a trust epoch, and a partial write must never be loaded. This ADR
freezes canonical ownership per field, the atomic multi-component commit
ordering, SQLite precedence for security state, and the
`live | restarted | stopped` restore classification.

## Decision drivers

- No false process-resurrection (spec success criterion 5; PRD principle 5).
- Security state is monotonic and never restored from a layout snapshot (spec
  trust boundary; PRD "Data ownership").
- Atomic generations with previous-known-good retention (spec risk control).

## Decision

### Canonical authority per field (`internal/persist/manifest.go` `Authority`)

| Data | Authority | Durability | Recovery rule |
|---|---|---|---|
| Session registry / project trust epochs / grants / audit | control actor | **SQLite only** | Epochs never decrease; snapshots can never reactivate grants or erase audit. |
| Graph + stable IDs + event cursor | session loop | Snapshot manifest | Reject partial/corrupt generations; keep prior known-good. |
| PTY/process identity | PTY supervisor | Runtime only | Fresh daemon classifies stopped/restarted; never reconstructs live. |
| Raw terminal output | surface ring | Checksummed binary sidecars | Manifest checksum/generation selects valid bytes; replay gap is explicit. |
| Cell grid | terminal engine | Derived | Rebuilt from raw fixture; replaceable. |
| Events | session loop | Bounded ring + cursor | Gap requires snapshot + cursor reset (ADR-0004). |
| Attachments / input leases | attach manager | **Ephemeral, never persisted** | Disconnect releases; restore never recreates. |
| Notifications | notification service | Live SQLite; logical export in explicit snapshot | Live DB wins crash recovery; explicit restore imports only the matching committed notification checkpoint. |

### Checkpoint generation and components

A generation (`Manifest`) has a unique `CheckpointID` (UUIDv7), a checksummed
component list, and a link to the retained previous-known-good generation.
Component kinds: `graph` (versioned JSON — tree, IDs, cwd, argv, non-secret env
allowlist, restart policy, replay config, notification checkpoint, event cursor),
`replay_sidecar` (versioned binary raw bytes; **never** base64-embedded in the
graph JSON), and `notify_export` (logical notification/read state — the only
thing an explicit snapshot restore may import).

### Atomic commit ordering (`internal/persist` `CommitOrder`, referenced by B8 tests)

1. Write every component to a temp file.
2. `fsync` each component file.
3. Compute and record each component's SHA-256 in the manifest.
4. Write the manifest temp file and `fsync` it.
5. **Atomically rename the manifest into place — THE COMMIT POINT.**
6. `fsync` the generation directory so the rename is durable.
7. Only now retire the generation older than previous-known-good.

A crash before step 5 leaves the prior committed generation authoritative; a
partial temp generation is ignored on next open. Migration failure preserves and
reports the previous known-good snapshot and commits no partial load (fail
closed with an exportable diagnostic).

### SQLite precedence and non-rollback of trust

Live SQLite (`modernc.org/sqlite`, WAL, cgo-free — ADR-0007) is canonical for
notifications during crash recovery and is the **sole** authority for project
trust epochs, grants, revocation, and audit. An explicit snapshot restore may
import only its notification/read export; it can never restore, decrease, or
reactivate security state. Schema migrations are ordered, transactional, and
forward-only at runtime, with documented export/restore procedures.

### Restore classification (`internal/persist` `Classify`)

Every restored surface is exactly one of `live | restarted | stopped` with a
reason, in this precedence (proved by `TestClassifyRules` /
`TestClassifyFreshDaemonNeverLive`):

1. A validation error forces `stopped` with that reason (fail closed).
2. `live` **only** for an in-daemon restore that still owns the identical
   PTY/process identity. A **fresh daemon is structurally excluded** from `live`.
3. `restarted` only under an explicit `automatic` restart policy with a launchable
   executable and cwd.
4. Otherwise `stopped` (default; e.g. `manual` policy — the spec default — or a
   missing executable/cwd), with the specific reason.

Client attachments and input leases are ephemeral and never restored; restore is
usable once the tree and each surface classification are visible.

### Platform-neutral snapshots; compile-only non-Linux placeholders

Snapshot/SQLite I/O is platform-neutral Go. OS-specific restore concerns
(descriptor validation, process identity) go through the ADR-0006 interfaces;
Darwin/Windows remain compile-only placeholders with no support claims.

## Consequences

**Positive**

- The "fresh daemon never live" and "trust never rolled back by a snapshot"
  invariants are executable and tested at the contract layer before B8 exists.
- The commit ordering is a named constant B8's fault-injection tests assert
  step-by-step.

**Negative**

- Multi-component generations plus previous-known-good retention use more disk
  than a single-file snapshot; bounded and required for safe recovery.

## Alternatives considered

- **Single JSON blob including raw output** — rejected: base64 bloat and no
  partial-recovery story; separate checksummed sidecars enable it.
- **Snapshot as the trust authority** — rejected outright: violates the
  non-rollback rule; SQLite is the sole trust authority.

## Follow-ups

- T4 B8 implements the codecs, fsync/rename sequence, SQLite migrations, and
  restore against these types; T6 Q4 injects write/fsync/rename/corruption faults.
