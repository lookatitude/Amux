Adopt `.guild/agents/backend.md` and resume T4 from `.guild/context/run-8c2926bb-2ebe-4166-8b77-84c0de3e12c0/backend-T4-backend.md`. This is a fresh R-016 budget after the prior Fable session stalled without a write. Work autonomously to completion; do not restart or re-create the substantial checkpoint.

Begin execution immediately. The latest live audit is more specific than the old checkpoint:

- `go test -count=1 ./internal/attach` passed all 13 tests.
- `go test -count=20 ./internal/attach` failed intermittently only at `TestSlowConsumerDisconnectedWithReceipt` (`fast got 4 frames, want 300`).
- `go test -race -count=5 ./internal/attach` failed intermittently only at the same test (`fast got 32 frames, want 300`).

Make a TDD-scoped fix for the unfair/non-deterministic observer fan-out or backpressure behavior without weakening ADR-0004, removing assertions, hiding the slow-consumer receipt, using unbounded memory, or inflating timeouts. Prove it with repeated and race-focused attach tests.

Then audit and finish every B1-B12 requirement in the formal bundle. Complete the real daemon, shared client, all 20 CLI flows, PTY/VT, persistence/restore, local protocol/transport, attach/leases, notifications, adapters, hooks/redaction, context extraction, diagnostics, observability, and failure paths. Run the T2 security conformance Factory from T4-owned code without editing `internal/securitytest/**`.

Stay strictly inside `.guild/runs/run-8c2926bb-2ebe-4166-8b77-84c0de3e12c0/scope/T4-backend.json`. Do not change ADRs, security policy/conformance sources, CI, packaging, release/operations docs, research, or TUI. Maintain no-cgo and distinguish macOS portable evidence from Linux-only runtime evidence.

Before the receipt, run gofmt, vet, all tests with `-count=1`, race tests, repeated attach tests, targeted security conformance, safe bounded fuzz smoke, Linux amd64/arm64 compile checks, module verify/tidy diff, and scope audit. Report skips and host limits honestly.

Write `.guild/runs/run-8c2926bb-2ebe-4166-8b77-84c0de3e12c0/handoffs/backend-T4-backend.md` as the final product write with B1-B12 coverage, files, evidence, risks, exact deferrals, T5/T6 handoffs, scope compliance, and exactly one valid `guild.handoff.v2`. Summary <=600 characters; notes <=200 characters.

Return only one fenced `guild.handoff.v2` block and nothing after it.
