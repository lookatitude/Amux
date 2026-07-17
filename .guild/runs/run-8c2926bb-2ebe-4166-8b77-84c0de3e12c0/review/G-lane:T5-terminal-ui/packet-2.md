---
packet_id: G-lane-T5-terminal-ui-r2
gate: G-lane:T5-terminal-ui
run_id: run-8c2926bb-2ebe-4166-8b77-84c0de3e12c0
author_host: claude-code-cli
reviewer_host: codex-cli
artifact_path: .guild/runs/run-8c2926bb-2ebe-4166-8b77-84c0de3e12c0/handoffs/terminal-ui-T5-terminal-ui.md
artifact_sha256: 657fae4e549332da1aeb9374ec924ed9c13bb5ebe872bee764d4a0b29f245347
independence: strong
prior_round: 1
---

# G-lane review packet — T5 Terminal UI — round 2

Perform a skeptical, repository-grounded re-review of the exact receipt and
live implementation. Round 1 accepted the real Bubble Tea/Lip Gloss renderer,
projection-only authority, input non-leakage, eight-pane semantics, dependency
integrity, and T6 deferral. It found two blocking production workflow gaps.
This round verifies those findings are closed and accepted T5 behavior remains
intact.

## Prior finding F1 — attach lifecycle

Verify the shipped `amux tui` path now:

1. opens a real typed-client attach stream for the focused surface;
2. consumes atomic snapshot/cutover and frame sequence information without
   parsing raw VT or becoming cell-state authority;
3. remembers the last delivered sequence and reattaches with bounded replay;
4. surfaces replay gaps, slow consumers, daemon loss, and lease denial using
   truthful recovery/read-only states;
5. closes/supersedes old streams safely on focus/surface changes and discards
   stale-generation messages;
6. makes `Ctrl+b d` close the stream, release the UI-owned input lease, and
   detach/quit without stopping the PTY;
7. avoids leaked goroutines, duplicate streams, stale writes, and fabricated
   live state.

Inspect production call paths, not just fakes. Confirm tests drive the same
Bubble Tea Update/dispatch path and cover cancellation/race behavior.

## Prior finding F2 — reachable hook trust

Verify the shipped key/event path now:

1. exposes a discoverable hook-trust action from the live default keymap/help;
2. reaches `hook.inspect` for the selected daemon-reported project from real
   Bubble Tea key messages, not a test-only helper;
3. renders project state/epoch and frozen grant executable, digest, events,
   scope, env keys, timeout, and output cap before mutation;
4. makes approve, deny, and revoke reachable with their correct backend method
   and confirmation/token semantics;
5. makes cancel/escape, absent grants, absent project, and inspect errors
   fail closed without mutation;
6. leaves trust/audit/grant authority entirely in the daemon.

## Regression and integrity gates

Confirm the rework preserved the real Bubble Tea v2 Model/Update/View, Lip
Gloss rendering, four daemon projections, eight-pane behavior, prefix/input
non-leakage, immutable authority boundary, tidy-clean module graph, and truthful
scope. No `internal/terminal`, direct store/SQLite, persistence, or hooks-internal
import may exist under the TUI or TUI command.

Independent orchestrator evidence for this exact workspace is green:
`go mod tidy -diff`; module verification; frozen dependency and license gates;
host/Linux vet; Linux amd64/arm64 no-cgo builds; 724 full tests across 45
packages; 724 race tests; 112 focused TUI/cmd tests; and 21 targeted attach/
trust/recovery tests. Counts are not semantic proof.

Return only one valid `review_result.v1` JSON object for packet
`G-lane-T5-terminal-ui-r2`, reviewed SHA
`657fae4e549332da1aeb9374ec924ed9c13bb5ebe872bee764d4a0b29f245347`, round 2,
reviewer host `codex`. A satisfied result must have empty `findings` and
`blocking_findings`; every issue must cite exact live evidence and a violated
acceptance contract.
