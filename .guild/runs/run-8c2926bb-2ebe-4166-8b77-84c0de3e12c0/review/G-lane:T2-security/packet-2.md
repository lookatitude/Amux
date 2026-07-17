---
packet_id: G-lane-T2-security-r2
gate: G-lane:T2-security
run_id: run-8c2926bb-2ebe-4166-8b77-84c0de3e12c0
author_host: claude-code-cli
reviewer_host: codex-cli
artifact_path: .guild/runs/run-8c2926bb-2ebe-4166-8b77-84c0de3e12c0/handoffs/security-T2-security.md
artifact_sha256: c5a32cc281050917706bdfa14a6b75be0b8f77efa7b84ee74b224b1620e32312
independence: strong
prior_round: 1
---

# G-lane review packet — reopened T2 security — round 2

Skeptically review the exact receipt and current implementation that claim to
close T6 findings F2 (overlayfs identity reuse) and F5 (vacuous manifest
bindings). This is a semantic review, not a test-count check.

Verify:

1. The public durable key is still exactly SHA-256(realpath, dev, ino); the
   new discriminator is separately persisted and never silently changes graph
   identity or protocol compatibility.
2. Linux `statx` birth-time validation actually distinguishes remove/recreate
   under overlayfs while remaining stable across ordinary child and content
   changes. Inspect masked/zero/unsupported handling, time-resolution limits,
   copy-up behavior, symlink/canonical-path behavior, mount/reboot behavior,
   and macOS/non-Linux compilation. Any ambiguity must fail closed.
3. Replacement validation is enforced before trust reuse at registration,
   durable restart rehydration, and every production hook-launch path. No
   alternate authorization path may bypass it.
4. Invalidation is transactionally/fail-closed correct: monotonic epoch,
   project state, grant deactivation, audit, listeners/child containment, and
   in-memory vs persisted state remain safe when any store write fails.
5. Migration from an absent discriminator cannot preserve ambiguous approved
   trust. Fresh/unchanged persisted trust rehydrates without epoch rollback.
6. `TrustMatrixReplay` now binds to substantive production-integrated tests,
   not merely a renamed unit SUT or a test that injects the expected decision.
   Inspect all row classes and confirm the test can catch real control/store/
   runtime divergence.
7. The self-gate correctly models `go test -run`, packages, build tags, and
   skips; it fails on zero matches. The second-UID manifest binds only to the
   real Linux/root harness and receipt evidence proves substantive executions.
8. Receipt claims match raw Arch/Ubuntu/host evidence and do not overclaim the
   unrelated backend/devops/QA gates.

Independent orchestrator reproduction: host focused packages passed 152 tests;
the exact integration command passed 42 substantive tests across 47 packages;
the security self-gates passed 9 tests. Ubuntu evidence shows the previously
red replacement test and all focused identity cases passing, plus an integrated
41-row matrix and real second-UID cases.

Return one valid `review_result.v1` for packet `G-lane-T2-security-r2`, SHA
`c5a32cc281050917706bdfa14a6b75be0b8f77efa7b84ee74b224b1620e32312`, round 2,
reviewer host `codex`. A satisfied result has no findings. Do not mutate files.

