Adopt `.guild/agents/backend.md` and continue T4 from `.guild/context/run-8c2926bb-2ebe-4166-8b77-84c0de3e12c0/backend-T4-backend.md`. Work autonomously until the complete B1-B12 backend receipt exists. Do not restart or recreate correct work.

The prior Fable session was terminated by an app/wrapper interruption after real scoped progress, not a code failure. Preserve and audit its checkpoint:

- Ring-backed attach delivery now has new drained-tiny-buffer and wedged-consumer regression tests plus lifecycle cleanup.
- Independent post-interruption gates pass: `go test -count=1 ./...` = 577 passes / 33 packages; `go vet ./...` passes; `go mod verify` passes.
- Independent attach stress before the interruption passed `go test -count=20 ./internal/attach` (300 tests) and `go test -race -count=5 ./internal/attach` (75 tests).
- New/changed checkpoint surfaces include `internal/rpcapi/rpcapi.go`, `internal/daemon/{server,run,util,wire,snapshot,surface,truststore}.go`, and `cmd/amuxd/main.go`.

Begin by auditing those partial daemon/RPC edits for correctness and missing tests. Then finish every remaining B1-B12 acceptance item in the bundle, including the real daemon/shared client, all required CLI command families and 20 black-box flows, PTY/VT, persistence/restore, protocol/transport, attach/leases, notifications/adapters, hooks/redaction, context, diagnostics/metrics, and T2 security conformance through a T4-owned Factory registration.

Stay inside `.guild/runs/run-8c2926bb-2ebe-4166-8b77-84c0de3e12c0/scope/T4-backend.json`. Do not modify frozen ADRs, T2 security policy/conformance sources, CI, packaging, release/operations docs, research, or TUI. Maintain no-cgo. Distinguish portable/macOS evidence from Linux-only runtime evidence and hand exact Linux runtime prerequisites to T6.

Before the receipt, run gofmt, vet, all tests, race tests, attach stress, security conformance, safe bounded fuzz smoke, Linux amd64/arm64 compile checks, module verify/tidy diff, and scope audit. Do not hide skips or platform limitations.

Write `.guild/runs/run-8c2926bb-2ebe-4166-8b77-84c0de3e12c0/handoffs/backend-T4-backend.md` as the final product write with B1-B12 coverage, changed files, exact evidence, risks, honest deferrals, T5/T6 handoffs, scope compliance, and exactly one valid `guild.handoff.v2`. Summary <=600 characters and notes <=200 characters. Return only that fenced envelope.
