Adopt `.guild/agents/terminal-ui.md` and execute T5 terminal-ui completely. Your authoritative brief is `.guild/context/run-8c2926bb-2ebe-4166-8b77-84c0de3e12c0/terminal-ui-T5-terminal-ui.md`; privilege it over ambient context. The approved backend receipt and current source are live truth; recalled wiki pages in the bundle are stale historical data where they claim no implementation exists.

Implement U1-U8 autonomously inside the frozen contracts. Use Bubble Tea v2 and Lip Gloss v2 as approved in the PRD, pin dependencies under ADR-0007 policy, and keep durable authority entirely in the daemon/client contract. Build a production `amux tui` entry point plus testable `internal/tui` packages. The TUI must consume immutable client-facing models/cell snapshots and issue backend commands; it must not parse raw VT bytes, own authoritative grids/notifications/attach ordering, touch SQLite or snapshots, or execute/authorize hooks locally.

Work TDD-first and cover the lane literally:

- pure 1-8 pane nested split geometry, ratios, borders, content rects, equalize, odd/tiny terminals, and directional neighbors;
- deterministic rendering of backend-provided cells/cursor plus focus, process/cwd/git, active surface, unread/lease, exit, and live/restarted/stopped status, including Unicode wide/combining cells without geometry corruption;
- explicit passthrough/prefix/navigation/resize/surface/notification/help/confirmation modes with conflict-checked configurable keymaps; prefix/navigation/mode keys never leak to PTY input;
- paste, mouse, terminal resize, attach/detach, lease acquire/loss/takeover/release, read-only, gap recovery, slow-consumer detach, daemon boot change, reconnect, stopped surface, and event/replay gap presentation through backend recovery APIs;
- notification inbox, latest-unread focus routing, read/dismiss, delivery failure, and hook inspect/approve/deny/revoke confirmation presentation showing the frozen trust fields without deciding authority;
- monochrome/no-color focus, reduced motion, keyboard-only help/discovery, minimum-size fallback, screen-reader-friendly CLI alternative documentation, and fail-closed destructive/takeover confirmation;
- deterministic recorded message streams and golden frames for the full state matrix;
- 8-pane/20-surface benchmark hooks comparing full-frame and damage-aware rendering, recording frame time, allocations, and bytes written. Produce honest portable benchmark evidence locally and an exact Arch reference-profile command for T6; do not claim Arch p95 evidence from macOS. Keep the design capable of the p95 <75 ms gate.

Integrate with existing `internal/client`, `internal/rpcapi`, `api/v1`, daemon wire, attach, and semantic notification/hook contracts. If a truly missing backend semantic requires a frozen protocol change, stop at the ask-gate with exact evidence; otherwise implement a client adapter without changing durable semantics. Keep changes scoped to TUI/client-facing integration, approved dependency files, operator docs, and tests. Do not modify ADRs, backend authority, security corpus, CI, packaging, or research.

Run gofmt, vet, all tests, race tests, focused deterministic/golden tests, benchmarks, dependency checks, Linux amd64/arm64 no-cgo compile checks, and a forbidden-import/scope audit. Write `.guild/runs/run-8c2926bb-2ebe-4166-8b77-84c0de3e12c0/handoffs/terminal-ui-T5-terminal-ui.md` with exact U1-U8 coverage, evidence, honest Arch/T6 deferrals, and T6 handoff pointers. Emit exactly one valid `guild.handoff.v2` (summary <=600, notes <=200).

When you finish, emit your result as a SINGLE fenced code block and nothing after it:
```guild.handoff.v2
{ ... a valid guild.handoff.v2 object ... }
```
The block MUST validate against the guild.handoff.v2 contract. Do not add prose after it.
