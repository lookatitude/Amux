Adopt `.guild/agents/backend.md` and close exactly the two blocking findings in
`.guild/runs/run-8c2926bb-2ebe-4166-8b77-84c0de3e12c0/review/G-lane:T4-backend/result-5.json`.
Use Sol/Fable and TDD-first. Preserve the accepted bounded replay, structured
details, resource-exhaustion, and `--version` behavior.

F5 — make page selection and the replay result's `latest_seq` one atomic ring
snapshot. The current Engine calls `ReplayLimitBytes` and then `LatestSeq()`
under a second lock; if the page is empty and a chunk appends between calls,
the response can advertise `next_seq` past unseen data. Add a ring page/snapshot
API (or extend the existing bounded replay result) that returns selected whole
chunks plus the exact latest sequence observed under the same lock. Engine
must derive `LatestSeq` and empty-page `NextSeq` only from that snapshot.
Preserve semantics for current, ahead-of-latest, partial, gap, tiny-bound, and
concurrent-eviction cursors. Add deterministic barrier tests proving an append
immediately after an empty-page snapshot is returned on the next page and is
never skipped; include race/stress/property coverage for monotonic contiguous
pagination under concurrent append/eviction.

F6 — remove the timing heuristic from `spawnAndQuiesce`. The fake PTY's Wait
currently returns before its 17 MiB output is drained, and three unchanged
20 ms polls can falsely declare quiescence under `-race`. Give the fake handle
an explicit one-shot EOF/drained signal and make the test wait on that exact
barrier (and, if needed, make fake `Wait` model process/output lifecycle
coherently). Do not fix the test by sleeping longer or retrying. Prove the
gap test deterministically observes eviction with repeated and Linux-race
runs, including under parallel load.

Run focused ring/engine/RPC/client/CLI tests, at least 50 repeated executions
of the formerly flaky test under `-race`, full host race, the real Linux
arm64/amd64 race receipt/harness if available, ResourceExhaustion, 20-flow E2E,
make verify, vet/tidy, and Linux no-cgo builds. Do not touch trust atomicity,
second-UID receipts, GoReleaser/CI, TUI, research, or protocol compatibility.
Replace the T4 handoff with exact evidence and emit one valid
`guild.handoff.v2` object.
