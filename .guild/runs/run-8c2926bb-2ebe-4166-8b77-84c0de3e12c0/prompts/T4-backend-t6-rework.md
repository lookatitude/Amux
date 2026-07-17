Adopt `.guild/agents/backend.md` and reopen T4-backend for the two product
findings validated by the mandatory T6 G-lane review at
`.guild/runs/run-8c2926bb-2ebe-4166-8b77-84c0de3e12c0/review/G-lane:T6-qa/result-1.json`.
Use TDD and preserve all accepted T4/T5 behavior.

1. Fix production `Engine.ReplayRead` so `ReplayReadParams.MaxBytes` is
   validated and enforced before constructing/encoding the unary response.
   The returned raw decoded payload must never exceed the caller bound and the
   encoded response must remain below `v1.MaxHeaderBytes` with conservative
   framing/base64 overhead. Define exact zero/negative/oversized semantics,
   sequence/NextSeq behavior for a partial page, and make continuation safe and
   gap-aware. Do not split a replay chunk in a way that breaks sequence truth;
   if chunk slicing is required, extend the contract only through an already
   compatible representation or return a typed bound error. Preserve the 16
   MiB retention floor. Add structured replay-gap details carrying at least the
   oldest retained and latest sequence so automation never parses a message.

   Prove with engine, RPC, client, CLI flow-14, and resource-exhaustion tests:
   flooded replay stays connected; decoded bytes honor MaxBytes; continuation
   is contiguous/no-duplicate; tiny/invalid bounds fail typed; gap details are
   structured; other clients remain healthy; allocations stay bounded.

2. Add the root `amux --version` flag with the same stamped version/protocol
   truth as `amux version` and `amuxd --version`. It must exit zero without
   dialing the daemon, work with stdout capture, have deterministic human
   output, and preserve the existing `version` subcommand and `--json`
   semantics. Add CLI tests and make the frozen install smoke pass.

Run focused replay/RPC/client/CLI/integration-resource-exhaustion tests, the
20-flow E2E, full tests, full race tests, vet, tidy/verify, attach stress,
security conformance, and Linux amd64+arm64 no-cgo builds. Do not change the
project-identity/security fix, GoReleaser/CI files, TUI, research, or fabricate
release-container evidence. Replace
`.guild/runs/run-8c2926bb-2ebe-4166-8b77-84c0de3e12c0/handoffs/backend-T4-backend.md`
with an exact receipt and emit exactly one valid `guild.handoff.v2` object.

