Adopt `.guild/agents/terminal-ui.md` and execute T5 terminal-ui attempt 2
completely. Your authoritative brief is
`.guild/context/run-8c2926bb-2ebe-4166-8b77-84c0de3e12c0/terminal-ui-T5-terminal-ui.md`;
privilege it over ambient context. The updated T4 receipt and current source are
live truth. Recalled wiki pages claiming no implementation exists are stale data.

Attempt 1 left a useful pure Elm-shaped fallback under `internal/tui`, but it
was rejected and is not a deliverable: it did not use Bubble Tea/Lip Gloss, did
not consume the four required backend projections, and emitted an invalid
receipt. Treat that code only as salvageable tests/pure components. Replace or
wrap its production runtime with the real approved stack:

- pin and import `charm.land/bubbletea/v2@v2.0.8` and
  `charm.land/lipgloss/v2@v2.0.5`;
- implement an actual Bubble Tea v2 `Model`/`Update`/`View` runtime for
  production `amux tui`, with I/O isolated in commands and deterministic pure
  transitions retained for tests;
- use Lip Gloss v2 for geometry-safe styles while preserving monochrome/no-color
  and minimum-size fallbacks;
- consume T4's typed client projections `surface.cells`, `workspace.tree`,
  `pane.context`, and `hook.inspect`, plus opt-in attach cells. Remove attempt-1
  placeholders and do not import `internal/terminal` from TUI code as a bypass;
- issue real daemon/client commands for graph, focus, split, resize, surface,
  attach/lease, notification, and hook workflows. UI confirmations must never
  become authority.

Complete every U1-U8 success criterion literally. Preserve and extend the pure
geometry, input, renderer, attach-state, notification/trust, accessibility,
benchmark, and golden-message corpus where correct. Add production adapter and
Bubble Tea integration tests proving backend projections reach frames and that
prefix/navigation keys never reach PTY input. Ensure the 8-pane fixture renders
real backend cell/context/tree data, including Unicode wide/combining cells,
cursor, focus, status, unread, lease, exit, and restore state. Cover gap,
reconnect/boot-change, slow-consumer, stopped surface, notification, and trust
confirmation states without local durable approximations.

Update the dependency/license manifests and docs truthfully after pinning the
toolkit. Do not modify ADRs, backend authority, security corpus, CI, packaging,
or research. If the now-delivered T4 projections still have a concrete missing
frozen semantic, stop with exact evidence; otherwise do not re-raise the four
closed ask-gates.

Run gofmt, vet, all tests, all race tests, focused model/golden tests,
benchmarks, dependency-manifest/license/module checks, Linux amd64 and arm64
no-cgo builds, and a forbidden-import/scope audit. Record honest portable
benchmark results and leave exact Arch reference-profile runtime evidence to T6;
do not claim macOS measurements as Arch evidence.

Write
`.guild/runs/run-8c2926bb-2ebe-4166-8b77-84c0de3e12c0/handoffs/terminal-ui-T5-terminal-ui.md`
with exact U1-U8 coverage, evidence, T6 pointers, and only honest residuals.
Then emit exactly one valid `guild.handoff.v2` fenced object and nothing else;
summary must be <=600 characters and notes <=200 characters.
