# ADR-0002 — Domain graph, identifiers, and invariants

- Status: Accepted
- Date: 2026-07-15
- Deciders: architect (T1)
- Significance: high
- Enforced by: `internal/domain` property/replay suites (`go test ./internal/domain/`)

## Context

The domain graph — `session -> workspaces -> split-tree panes -> ordered
surfaces` — is the object model every other subsystem serializes, restores, and
renders. It must be deterministic (for replay/snapshot), invariant-preserving
(no orphan panes, always one active surface), and identity-stable (opaque IDs
survive snapshots and never encode structure). This ADR freezes those rules.

## Decision drivers

- Deterministic transitions for replay and snapshot equality (spec "Raw output is
  truth"; PRD principle 3 "stable identity before presentation").
- Opaque, sortable IDs decoupled from names and layout.
- A single invariant definition testable as a property.

## Decision

### Identifiers

- `SessionID`, `WorkspaceID`, `PaneID`, `SurfaceID`, and `ProjectID` are distinct
  named string types (`internal/domain/ids.go`) so the compiler rejects passing
  one where another is required.
- IDs are **opaque, stable, and sortable**. No code may parse structure out of an
  ID. Production mints **UUIDv7** via `github.com/google/uuid` (see ADR-0007);
  the `IDSource` interface lets tests substitute a deterministic `CountingSource`
  so a command sequence replays to byte-identical IDs.
- **Monotonicity of UUIDv7** (opaque-but-sortable-by-creation) is guaranteed by
  google/uuid's process-global monotonic timestamp and proved empirically in the
  A6 spike (`spikes/uuidv7`): 100k sequential values are strictly increasing and
  unique; concurrent generation is unique; and an Amux-owned monotonic-floor
  clamp (`spikes/uuidv7/monotonic.go`) documents the fallback that keeps the
  invariant if the dependency is ever swapped, verified under a regressing clock.

### Graph model (`internal/domain/graph.go`)

- A workspace holds a **binary split tree**: internal nodes are splits with an
  orientation (`SplitHorizontal` = panes left/right; `SplitVertical` = panes
  top/bottom) and a `ratio` (first child's fraction), clamped to
  `[MinRatio, 1-MinRatio]` = `[0.05, 0.95]`. Leaves are panes.
- Each pane owns an independent `Cwd`, an optional opaque `Project` tag (the
  control actor computes the value; the domain never validates it), and a
  **non-empty ordered surface list with exactly one active surface**.
- A workspace has one focused pane and a **full recency-ordered focus history**;
  the focused pane is always the most-recent history entry.
- A workspace optionally records one `PrimaryRoot` (hook `workspace-primary`
  scope resolves only against it — spec trust boundary).
- Geometry (pixel/cell rectangles, directional-neighbour selection) is a pure
  function the TUI owns (U2); the domain guarantees only the ratio/tree
  invariants, keeping it free of presentation concerns.

### Invariants (the frozen set — `State.Check`)

1. Tree shape: every node is a leaf (pane set, no children) XOR a split (valid
   orientation, in-bounds ratio, two non-nil children).
2. The pane map equals the tree's leaf set exactly (no orphans, no phantoms).
3. Every pane has ≥1 surface; its active surface is a member; surfaces are unique.
4. Focus history is a permutation of the pane set; `focused` is its last element.
5. `workspaceOrder` is a permutation of the workspace keys (stable creation
   order).

### Revisions

Each committed mutation bumps the affected workspace's `rev` and the session
`State.Rev`. Events carry the resulting workspace revision; responses and events
reference the same revision (ADR-0003/0004).

### Commands, results, and deletion semantics

- Commands are immutable inputs; `Apply(state, cmd, ids)` validates the whole
  command first and, only if valid, mutates a **clone** and returns it — on error
  the caller's state is returned untouched with a typed error and no events (no
  partial transition). Proved by `TestApplyDoesNotMutateInput` and
  `TestTypedErrors`.
- Deletion cascades: closing a pane collapses its parent split (the sibling takes
  the parent's place) and drops its surfaces; closing a workspace removes its
  whole subtree; closing a surface reassigns active deterministically.
- Typed conflict errors (`internal/domain/errors.go`): closing the last pane
  (`conflict` — use CloseWorkspace), closing the last surface (`conflict`),
  resizing the root pane (`conflict`), missing entities (`not_found`), bad inputs
  (`invalid_argument`). These map onto the ADR-0003 taxonomy.

## Consequences

**Positive**

- One `Check` function is the sole definition of "valid graph", asserted after
  every command across 8 pseudo-random seeds × 600 commands and on deterministic
  replay (`internal/domain/property_test.go`).
- Determinism is proven: replaying recorded commands from scratch yields a
  byte-identical graph (`TestDeterministicReplay`) — the property snapshot restore
  relies on.

**Negative**

- Clone-on-apply copies the graph per command; acceptable at single-user scale
  and localized behind `Apply`. Structural sharing can be introduced later
  without changing the contract.

## Alternatives considered

- **Integer/semantic IDs** — rejected: couples identity to structure and leaks
  ordering semantics; UUIDv7 gives opacity + sortability.
- **In-place mutation with rollback journal** — rejected: clone-then-validate is
  simpler to prove correct and matches the no-partial-state requirement.

## Follow-ups

- T4 backend runs the domain behind the session actor and adds command families
  as needed inside these invariants; any new invariant extends `State.Check`.
