---
packet_id: G-lane-T4-backend-r5
gate: G-lane:T4-backend
run_id: run-8c2926bb-2ebe-4166-8b77-84c0de3e12c0
author_host: claude-code-cli
reviewer_host: codex-cli
artifact_path: .guild/runs/run-8c2926bb-2ebe-4166-8b77-84c0de3e12c0/handoffs/backend-T4-backend.md
artifact_sha256: 3e0bed05936a77a588a99cd12766dbe41e3341ed102246325a96fd9844947f5c
independence: strong
prior_round: 4
---

# G-lane review packet — reopened T4 backend — round 5

Skeptically review the exact receipt and live implementation that claim to
close T6 findings F1 (bounded replay) and F4 (`amux --version`).

Verify:

1. Every ReplayRead path validates/enforces MaxBytes before materializing the
   full ring, preserves the 16 MiB retention floor, and keeps the encoded unary
   response safely below `v1.MaxHeaderBytes` under worst-case chunk counts,
   JSON/base64 overhead, long sequence numbers, and envelope framing.
2. Exact zero/negative/oversized/tiny-bound semantics are typed and stable.
   Whole-chunk paging must never skip, duplicate, split, or loop; NextSeq and
   LatestSeq must remain truthful under partial pages and concurrent eviction.
3. Structured replay-gap/bound details survive engine → server → wire → client
   without message parsing or protocol/schema incompatibility.
4. Resource-exhaustion coverage really drives the production assembly and
   proves the connection and unrelated clients remain healthy with bounded
   allocations.
5. `amux --version` exits zero without daemon access, prints the same stamped
   identity as the subcommand/daemon, preserves machine JSON behavior, and is
   covered in release-shaped smoke.
6. Review the new tests for determinism. Independent evidence found one
   full-suite `-race` failure in
   `TestEngineReplayReadGapStructuredDetails` (`evicted cursor: got <nil>`) that
   passed in isolation and on rerun. Determine whether `spawnAndQuiesce` can
   declare output stable before all 17 MiB reaches the ring under slow/race
   scheduling. A flaky acceptance test is a finding even if reruns pass.
7. Preserve prior accepted T4/T5 authority, restore, attach, security, and API
   contracts; no hidden scope expansion.

Independent orchestrator reproduction: four focused packages passed 115
tests; production-tagged ResourceExhaustion passed; root `amux --version`
printed the expected stamped dev identity. The receipt's initial race claim is
not accepted because the separate security run observed the timing failure.

Return one valid `review_result.v1` for packet `G-lane-T4-backend-r5`, SHA
`3e0bed05936a77a588a99cd12766dbe41e3341ed102246325a96fd9844947f5c`, round 5,
reviewer host `codex`. Cite exact evidence. Do not mutate files.

