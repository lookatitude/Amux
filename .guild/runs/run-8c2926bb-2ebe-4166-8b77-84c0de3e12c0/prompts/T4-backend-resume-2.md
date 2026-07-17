Adopt the project-local backend specialist definition at `.guild/agents/backend.md` and resume `T4-backend` from the authoritative formal bundle `.guild/context/run-8c2926bb-2ebe-4166-8b77-84c0de3e12c0/backend-T4-backend.md`.

This is the R-016 checkpoint-resume attempt after attempt 1 ended only because the Claude Fable 5 provider session quota was exhausted. Preserve and audit the existing scoped implementation; do not recreate correct work. T1 architect, T2 security, and T3 DevOps are complete, independently reviewed, and represented by their exact `guild.handoff.v2` envelopes in the bundle. Privilege the bundle over ambient context and inspect the referenced live artifacts where needed. Do not redefine frozen ADR, protocol, persistence, trust, cgo, supported-platform, or acceptance contracts. If a real contradiction requires a frozen-contract change, honor the bundle's ask-gate and return blocked rather than assuming approval.

Start TDD-first with the live checkpoint failures, which were reproduced immediately before this dispatch with `go test -count=1 ./internal/attach`:

- `TestDetachReleasesLeaseButSurfaceAndSinkStayLive`: re-attach receives stale `"live"` instead of new `"again"`.
- `TestSlowConsumerDisconnectedWithReceipt`: the healthy drained observer receives only 4 of 300 frames.
- `TestAttachCutoverContiguousUnderRace`: the concurrently drained observer is disconnected as a slow consumer.

Fix the underlying replay-default and bounded fan-out/cutover behavior without weakening the ADR-0004 invariants, deleting assertions, inflating timeouts, or converting deterministic semantics into timing luck. Run the focused attach package repeatedly and under `-race` before widening verification.

Then audit the preserved checkpoint against every B1-B12 acceptance requirement in the bundle and complete all missing behavior. The daemon and CLI must be real end-to-end implementations, not interface-only scaffolding. Exercise all 20 required CLI flows against the shared client/daemon path. Register the T2 `securitytest` Factory from a T4-owned test package without modifying `internal/securitytest/**`, and run the implementation-neutral conformance corpus against real backend enforcement. Close any remaining gaps in PTY/VT, persistence/restore, local transport/protocol, attach/lease, notification, agent adapters, hooks/redaction, context extraction, diagnostics/observability, lifecycle, and failure-path behavior.

Keep every write inside `.guild/runs/run-8c2926bb-2ebe-4166-8b77-84c0de3e12c0/scope/T4-backend.json`. Do not touch ADRs, security policy or conformance sources, CI, packaging, release/operations docs, research, or TUI surfaces. Maintain no-cgo and the Linux-first contract. On this macOS author host, distinguish portable tests and Linux compile checks from Linux-only runtime evidence; never claim Linux process/socket/containment runtime behavior without executing it. Record any genuine Linux-only runtime check as a precise T6 prerequisite with a reproducible command.

Before producing the receipt, run and report: gofmt cleanliness; `go vet ./...`; all repository tests with `-count=1`; repository race tests; focused attach race tests; targeted security conformance; bounded fuzz smoke where safe; Linux amd64 and arm64 compile checks for relevant binaries/packages; `go mod verify`; `go mod tidy -diff`; and a scope audit against the T4 scope JSON. Do not hide skips or platform limitations.

Write the durable receipt at `.guild/runs/run-8c2926bb-2ebe-4166-8b77-84c0de3e12c0/handoffs/backend-T4-backend.md` as the final product write. It must identify B1-B12 coverage, changed files, tests and exact evidence, risks, honest deferrals, T5/T6 handoffs, and scope compliance. Include exactly one valid `guild.handoff.v2` block. Keep its summary at most 600 characters and notes at most 200 characters.

When finished, emit the result as a SINGLE fenced code block and nothing after it:
```guild.handoff.v2
{ ... a valid guild.handoff.v2 object ... }
```
The block MUST validate against the `guild.handoff.v2` contract. Do not add prose after it.
