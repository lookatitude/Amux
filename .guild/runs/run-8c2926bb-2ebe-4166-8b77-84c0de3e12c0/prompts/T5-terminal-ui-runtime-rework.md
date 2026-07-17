Perform a hard T5-terminal-ui rework to close the two blocking Guild G-lane
round-1 findings. Preserve all accepted Bubble Tea/Lip Gloss rendering,
projection authority, input non-leakage, tests, and tidy-clean module state.

Read the exact review result at
`.guild/runs/run-8c2926bb-2ebe-4166-8b77-84c0de3e12c0/review/G-lane:T5-terminal-ui/result-1.json`,
the approved T5 plan/spec/ADRs, the current handoff, and live source/tests.

F1 — real attach lifecycle:

- The production TUI must establish a real daemon attach stream through the
  typed client/backend API for the focused surface, not merely poll projections.
- Fold delivered attach frames/sequences into the presentation while preserving
  daemon-owned cell state and never parsing raw VT in the TUI.
- Track last delivered sequence and implement reconnect/resume/replay semantics
  using the approved attach protocol. Surface gaps/overflow as recovery state.
- `Ctrl+b d` must close/cancel the stream and release any UI-owned input lease;
  it may then quit/detach according to the approved UX. It cannot be a no-op.
- Focus/surface changes must move the attach lifecycle safely without leaked
  goroutines, duplicate streams, stale writes, or fabricated `live` state.
- Add production-path and adversarial tests for attach, detach, reconnect from
  last sequence, replay gap/read-only behavior, lease release, focus changes,
  daemon loss, and cancellation/race safety.

F2 — reachable hook trust workflow:

- Add discoverable live TUI actions/keybindings for hook inspect and explicit
  approve, deny, and revoke workflows over backend semantic state.
- The shipped Bubble Tea Update/dispatch path—not a test helper alone—must reach
  `hook.inspect` and the confirmation card for the selected pane/project.
- Approval and revoke require explicit confirmation and backend confirm tokens;
  deny/cancel is fail-closed and performs no mutation. Do not make the TUI trust
  authority or bypass audit/grant enforcement.
- Render the frozen executable hash/events/env/timeout/scope/project detail
  before confirmation. Handle absent grants and errors safely.
- Add integration tests driven through real key messages/action dispatch, for
  approve, deny/cancel, and revoke, and update truthful user docs/key tables.

Verification and boundaries:

- Keep all four daemon projections and the real Bubble Tea v2 Model/Update/View.
- No imports of internal/terminal, direct SQLite, hooks internals, or backend
  authority from internal/tui or the TUI command.
- Prove gofmt, vet, staticcheck without module mutation, `go mod tidy -diff`,
  module verification, frozen dependency/license gates, full tests, full race
  tests, focused TUI tests, Linux amd64/arm64 no-cgo builds, and benchmark truth.
- Update the existing T5 handoff with exact final evidence and these review
  findings closed. Emit exactly one valid `guild.handoff.v2` fenced object with
  summary <=600 characters and notes <=200, with no prose after it.
