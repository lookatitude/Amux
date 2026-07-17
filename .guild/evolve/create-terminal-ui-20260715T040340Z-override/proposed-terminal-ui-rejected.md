---
type: specialist-creation-gate-result
run_id: create-terminal-ui-20260715T040340Z-override
role: terminal-ui
outcome: rejected
gate_failed: new-specialist
proposed_path: .guild/agents/proposed/terminal-ui.md
live_path: null
created_at: 2026-07-15T04:03:40Z
---

# Proposed terminal-ui specialist — shadow gate result

The user explicitly waived the historical extraction-evidence gate. The paired evaluation then passed: baseline `6/11`, candidate `11/11`.

Historical shadow mode failed with two collisions:

1. `pane attach UX` can capture attach lifecycle, transport, detach, and restore-contract design owned by `architect` and `backend`.
2. `VT/cell rendering` can capture the raw VT parser and authoritative cell-state engine owned by `backend` under the approved system boundary.

Registration did not run. The proposal remains incubating.

## Refinement options

1. Narrow the role to **client-side presentation over an already-approved attach protocol** and explicitly exclude attach transport/lifecycle contract definition.
2. Narrow the role to **projection of backend-provided immutable cell snapshots/deltas** and explicitly exclude raw VT parsing and authoritative cell-state ownership.
3. Broaden the role intentionally and transfer those contracts from backend/architect, which would require revising and reapproving the product specification.
4. Abandon the role and use the scoped frontend fallback.
