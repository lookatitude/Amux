Adopt `.guild/agents/backend.md` and execute a hard T4 contract-completion rework discovered by T5. The invalid T5 attempt is archived at `.guild/runs/run-8c2926bb-2ebe-4166-8b77-84c0de3e12c0/review/G-lane:T5-terminal-ui/artifact-attempt1-invalid.md`; its exact dependency/projection evidence is in the Bubble Tea decision and “Ask-gates → T4 backend contract requests” sections. Preserve the checksum-reviewed T4 B1-B12 implementation and all prior G-lane closures. Work autonomously inside the already approved plan/PRD: this is delivery of T4's explicit handoff contract to T5, not a new product semantic.

Close five concrete upstream gaps TDD-first:

1. **Bubble Tea v2 dependency compatibility.** ADR-0007 explicitly selects Bubble Tea for T5 and Decision 3 permits deliberate updates with full gates. `charm.land/bubbletea/v2@v2.0.8` and `charm.land/lipgloss/v2@v2.0.5` require `github.com/charmbracelet/x/ansi@v0.11.7`, while `internal/terminal` uses the removed v0.4.5 streaming parser API. Migrate the backend terminal engine/spike/tests to v0.11.7 or an equally narrow behavior-preserving adapter so every existing VT golden, fuzz, replay, wide/combining, mode, damage, and unsupported-sequence behavior remains true. Do not redesign the TerminalEngine seam or parser authority. Pin Bubble Tea v2 and Lip Gloss v2 only if useful for the compatibility proof; T5 will own their UI imports. Regenerate `docs/dependencies.md` accurately with licenses/cgo/manifests.

2. **Live immutable cells.** T5 is forbidden to parse raw VT. Add a bounded client-facing snapshot/delta projection for a surface's authoritative `terminal.CellSnapshot`, including cursor/geometry/sequence metadata sufficient for initial render and recovery. It must be obtainable through the shared client/RPC surface and integrate with attach/replay/event gap recovery without making the UI sequence authority. Additive v1 fields/methods are allowed under ADR-0003 compatibility; preserve old clients/goldens and strict durable boundaries.

3. **Hook trust detail.** Expose the full frozen trust presentation fields through a read-only hook inspect/list projection: project identity, executable identity/path and digest, events, cwd scope, environment keys, timeout, output cap, epoch/status. Never move authorization decisions to the UI; unknown/unavailable state fails closed and secrets remain redacted.

4. **Pane context.** Expose daemon-owned pane context already collected by B10—cwd, git root/branch/dirty, foreground process/PID/exit/timestamp—through a bounded immutable pane inspect/client projection. Do not add UI-local discovery.

5. **Workspace split tree.** Expose the authoritative workspace pane tree with stable pane/surface IDs, orientation, nesting, ratios, focus and active surface/order so T5 can render real geometry. This is read-only projection of existing domain state, not a second layout model.

Use additive protocol evolution, immutable RPC types, versioned golden vectors, shared client methods, production daemon wiring, and deterministic fake/client fixtures. Add black-box or wire-level tests proving each projection comes from live backend authority, remains bounded, survives reconnect/boot change where applicable, and cannot mutate durable state. Keep trust/redaction contracts intact. Do not modify TUI packages beyond compile-facing fixture updates; do not touch ADR text, CI, packaging, research, or frozen securitytest sources.

Run gofmt, vet, all tests, full race, terminal fuzz/goldens, CLI E2E, attach stress, security conformance, dependency/license/cgo checks available locally, Linux amd64/arm64 no-cgo builds, module verify/tidy diff, and scope audit. Replace `.guild/runs/run-8c2926bb-2ebe-4166-8b77-84c0de3e12c0/handoffs/backend-T4-backend.md` with an accurate receipt covering the preserved B1-B12 checkpoint plus the completed T5 contract. Emit exactly one valid `guild.handoff.v2` (summary <=600, notes <=200).

When you finish, emit your result as a SINGLE fenced code block and nothing after it:
```guild.handoff.v2
{ ... a valid guild.handoff.v2 object ... }
```
The block MUST validate against the guild.handoff.v2 contract. Do not add prose after it.
