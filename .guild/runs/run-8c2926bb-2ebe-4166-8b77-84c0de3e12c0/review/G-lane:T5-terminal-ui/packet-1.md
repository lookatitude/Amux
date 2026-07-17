---
packet_id: G-lane-T5-terminal-ui-r1
gate: G-lane:T5-terminal-ui
run_id: run-8c2926bb-2ebe-4166-8b77-84c0de3e12c0
author_host: claude-code-cli
reviewer_host: codex-cli
artifact_path: .guild/runs/run-8c2926bb-2ebe-4166-8b77-84c0de3e12c0/handoffs/terminal-ui-T5-terminal-ui.md
artifact_sha256: f42448ed40726d2e299eb8f55099264a66cc4e2a3d4336588024eb5d16511efc
independence: strong
prior_round: 0
---

# G-lane review packet — T5 Terminal UI — round 1

Perform a skeptical, repository-grounded review of the exact receipt and live
implementation. This gate covers the complete T5 lane, including the final
module-integrity rework. Do not accept claims based only on the handoff.

Primary gate questions:

1. Is the production `amux tui` command a real Bubble Tea v2 program using a
   proper Model/Update/View loop and Lip Gloss v2 styling, rather than a custom
   raw-terminal driver or preview-only facade?
2. Does the TUI consume daemon-owned `surface.cells`, `hook.inspect`,
   `pane.context`, and `workspace.tree` projections through the typed client,
   with no import of `internal/terminal` and no second mutable backend authority?
3. Are navigation, prefix handling, resize, mouse, paste, focus, pane commands,
   split/close commands, attach/reconnect, and confirmation flows wired to real
   messages/commands with fail-closed behavior? Can prefix/navigation input ever
   leak into the PTY byte stream?
4. Do the model, adapter, renderer, bridge, and integration tests prove an
   eight-pane workspace with stable geometry, derived cell content, Unicode
   wide/combining behavior, process/repository context, hook trust/grants,
   recovery state, and deterministic rendering?
5. Are security boundaries preserved: no durable authority in the TUI, no
   direct SQLite/hooks/backend dependency, no raw VT parsing, and no bypass of
   daemon audit/trust/lease enforcement?
6. Did the migration remove the legacy custom raw-terminal driver without
   leaving duplicate production paths or misleading docs?
7. Is the module graph now canonical and reproducible: `go mod tidy -diff`
   empty, no `honnef.co/go/tools` application dependency, frozen compiled graphs
   unchanged where claimed, license gate truthful, and pinned staticcheck unable
   to mutate go.mod/go.sum?
8. Are the benchmark claims honest and scoped to deterministic render work,
   while Arch Linux runtime/packaging and soak evidence remain correctly assigned
   to T6 rather than claimed complete here?

Inspect at minimum `cmd/amux/tui.go`, `internal/tui`, typed client/projection
contracts, tests, `go.mod`, `go.sum`, dependency/license tooling and docs, the
approved spec/plan/ADRs, and the exact receipt. Re-run focused read-only checks
where useful.

Independent orchestrator evidence for this exact workspace is green:
`go mod tidy -diff`; module verification; frozen dependency and license gates;
vet; Linux amd64/arm64 no-cgo builds; 707 full tests across 45 packages; 707 race
tests; 95 focused TUI/cmd tests; and a successful eight-pane static preview.
These counts are not semantic proof.

Return only one valid `review_result.v1` JSON object for packet
`G-lane-T5-terminal-ui-r1`, reviewed SHA
`f42448ed40726d2e299eb8f55099264a66cc4e2a3d4336588024eb5d16511efc`, round 1,
reviewer host `codex`. A satisfied result must have empty `findings` and
`blocking_findings`; every issue must cite exact live evidence and a violated
acceptance contract.
