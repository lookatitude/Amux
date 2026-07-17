Adopt the project-local backend specialist definition at `.guild/agents/backend.md` and implement `T4-backend` from the authoritative formal bundle `.guild/context/run-8c2926bb-2ebe-4166-8b77-84c0de3e12c0/backend-T4-backend.md`.

This is the first T4 implementation attempt. T1 architect, T2 security, and T3 DevOps are complete and independently reviewed. Consume their exact envelopes from the bundle and inspect their referenced live artifacts as needed. Privilege the bundle over ambient context. Do not redefine frozen ADR, protocol, persistence, trust, cgo, supported-platform, or acceptance contracts; if a real contradiction requires such a change, honor the bundle's ask-gate and return blocked rather than assuming approval.

Implement the full B1–B12 Go backend lane with TDD-first, bounded, fail-closed behavior. Work incrementally from failing tests to the smallest production code that satisfies them. The daemon and CLI must be real, not interface-only scaffolding. Register the T2 `securitytest` Factory from a T4-owned test package without modifying `internal/securitytest/**`; execute the implementation-neutral conformance corpus against real backend enforcement. Keep all writes inside `.guild/runs/run-8c2926bb-2ebe-4166-8b77-84c0de3e12c0/scope/T4-backend.json`. Do not touch CI, packaging, release/operations docs, security policy, ADRs, research, or TUI surfaces.

Prune the known stale `go.sum` hashes when the module graph is touched. Maintain the no-cgo policy. On this macOS author host, distinguish portable tests and Linux compile checks from Linux-only runtime evidence; do not claim Linux process/socket containment behavior without executing it. Run gofmt, vet, all repository tests, race tests, targeted security conformance, fuzz smoke where safe, Linux amd64/arm64 compile checks, module verification/tidy, and a scope audit. Record any genuinely Linux-only runtime check as a precise T6 prerequisite with its reproducible command.

Write the durable receipt at `.guild/runs/run-8c2926bb-2ebe-4166-8b77-84c0de3e12c0/handoffs/backend-T4-backend.md` as the final product write. It must identify B1–B12 coverage, changed files, tests/evidence, risks, exact deferrals, T5/T6 handoffs, and scope compliance, with exactly one valid `guild.handoff.v2` block. Keep the v2 summary at most 600 characters and notes at most 200 characters.

When you finish, emit your result as a SINGLE fenced code block and nothing after it:
```guild.handoff.v2
{ ... a valid guild.handoff.v2 object ... }
```
The block MUST validate against the `guild.handoff.v2` contract. Do not add prose after it.
