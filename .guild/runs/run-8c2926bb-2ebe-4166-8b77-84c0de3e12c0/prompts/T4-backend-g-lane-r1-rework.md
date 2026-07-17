Adopt `.guild/agents/backend.md` and execute the mandatory T4 G-lane round-1 rework. The exact independent finding is `.guild/runs/run-8c2926bb-2ebe-4166-8b77-84c0de3e12c0/review/G-lane:T4-backend/result-1.json`; the trail is beside it. Work autonomously and close only the real B8 blocker plus any tests/receipt facts required by that fix. Preserve the rest of the verified B1-B12 checkpoint.

The blocker is literal: the approved contract requires an in-daemon live-reconcile restore path, but `internal/daemon/snapshot.go` currently hard-codes `persist.RestoreContext{FreshDaemon: true}` in the only production restore entry point. This makes `ClassLive` unreachable even when the daemon still owns the same PTY/runtime. Unit-level classifier support for `FreshDaemon: false` is not sufficient; wire the actual production restore path to distinguish fresh-daemon restore from in-daemon reconcile, supply trustworthy `SamePTYIdentityOwned` evidence from current daemon runtime ownership, keep trust/security state excluded and monotonic, and preserve fail-closed stopped/restarted classification.

Implement TDD-first. Add production-path and E2E tests proving at minimum:

- in-daemon restore keeps a genuinely owned same-identity surface classified `live` without stopping/restarting it;
- a surface lacking same-identity ownership cannot be classified `live`;
- clean/fresh-daemon restore still never claims `live`;
- stopped/restarted reasons and restart-policy validation remain exact;
- stale security generation/grants remain excluded and fail closed;
- restored replay/snapshot/notification state remains correct, with no process resurrection claim.

Do not weaken ADR-0005, change persisted/protocol compatibility, or invent a fake live classification. If the frozen contract is genuinely impossible without an ADR change, stop at the ask-gate; otherwise implement it fully. Stay inside the existing T4 scope JSON and do not modify ADRs, securitytest sources, CI, packaging, research, or TUI.

Run focused restore tests, the 20-flow CLI E2E, gofmt, vet, all tests, race tests, attach stress, security conformance, Linux amd64/arm64 no-cgo compile checks, module verify/tidy diff, and scope audit. Replace `.guild/runs/run-8c2926bb-2ebe-4166-8b77-84c0de3e12c0/handoffs/backend-T4-backend.md` as the final product write. Remove the former live-reconcile deferral, describe the real production behavior and new evidence, update counts, and emit exactly one valid `guild.handoff.v2` (summary <=600, notes <=200).
