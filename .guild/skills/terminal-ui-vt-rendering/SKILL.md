---
name: terminal-ui-vt-rendering
description: "Projects backend-provided immutable terminal cell snapshots/deltas into deterministic Bubble Tea frames. Use for client-side glyph/style/cursor presentation, clipping, damage tracking, and golden frames. Do not use for raw VT parsing, authoritative cell-state mutation, PTY supervision, protocol design, web rendering, or suite-wide test strategy."
when_to_use: "When implementing or reviewing the client renderer that presents an approved immutable cell snapshot/delta interface as deterministic TUI frames."
type: specialist
derived_from_template: guild.skill_template.v1
---

# When to use it

Use for presentation of backend-provided cell snapshots/deltas, frame composition, cursor/style projection, Unicode-width display, clipping, damage regions, and golden-frame fixtures.

# When not to use it

Do not use for parsing raw PTY/VT bytes, defining or mutating authoritative cell state, spawning PTYs, changing IPC, implementing browser canvases, or defining the global QA strategy.

# Required inputs

- Architect-approved renderer boundary and backend-provided immutable cell snapshot/delta interface.
- Supported presentation/Unicode corpus and terminal capability matrix; raw VT conformance remains a backend input.
- Viewport dimensions, pane rectangles, focus/cursor state, and ordered cell updates.
- Explicit frame-time and allocation budgets.

# Output format

Produce Go renderer code, renderer-neutral fixtures, golden terminal frames, benchmark results, and a handoff listing unsupported sequences or contract changes as follow-ups.

# Workflow steps

1. Pin the cell, style, cursor, grapheme, width, and clipping invariants with fixtures.
2. Implement pure cell-grid-to-frame projection before Bubble Tea integration.
3. Add wide, combining, zero-width, alternate-screen, cursor, and resize cases.
4. Integrate damage-aware frame composition without changing protocol truth.
5. Measure allocations and frame duration against the accepted profile.
6. Hand raw-VT, parser, or authoritative-state defects back to `backend` and systemic boundary defects to `architect`.

# Evidence requirements

Golden fixtures must be byte-reviewable; repeated replay must produce identical frames; benchmarks report profile, frame size, allocations, and percentile duration.

# Escalation rules

Escalate raw-VT semantic ambiguity to `backend`, renderer/interface redesign to `architect`, and new suite-wide coverage requirements to `qa`.

# Safety constraints

Never interpolate terminal content into shell commands, trust terminal escape content as control-plane input, or claim conformance beyond the tested corpus.

# Eval cases

- Backend-cell-snapshot presentation request produces a deterministic renderer plan and golden fixtures.
- Unicode width defect includes combining and wide-glyph regression cases.
- PTY spawn request is rejected and routed to `backend`.
