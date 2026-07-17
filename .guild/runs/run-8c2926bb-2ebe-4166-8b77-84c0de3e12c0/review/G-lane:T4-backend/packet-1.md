---
packet_id: G-lane-T4-backend-r1
gate: G-lane:T4-backend
run_id: run-8c2926bb-2ebe-4166-8b77-84c0de3e12c0
author_host: claude-code-cli
reviewer_host: codex-cli
artifact_path: .guild/runs/run-8c2926bb-2ebe-4166-8b77-84c0de3e12c0/handoffs/backend-T4-backend.md
artifact_sha256: 13003652210bacb8d838e6194ad44feeb2d22fea7f8cc2b4b5f2d992436b25ce
independence: strong
---

# G-lane review packet — T4 Backend — round 1

Perform a skeptical, repository-grounded review of the exact T4 backend receipt and the current T4-owned implementation it cites. Do not accept counts or completion claims from the receipt without reopening live files and running focused checks where useful. The author was Claude Fable 5; you are the independent Codex reviewer.

Review against the approved plan, PRD, formal backend bundle, frozen T1 ADR/interface contracts, T2 security contract, and T3 build/verification contract. In particular:

1. Determine whether every B1-B12 work package is actually implemented end to end rather than scaffolded, especially the production daemon/shared client and all 20 required CLI flows.
2. Treat acceptance language literally. The formal bundle explicitly requires clean-daemon restore, in-daemon live reconcile, stopped/restarted classification, and stale-security-generation behavior. The receipt simultaneously claims B8 `done` and admits that finer in-daemon live reconcile is not implemented. Decide whether that is an honest nonblocking refinement or a blocking scope contradiction; cite exact current evidence.
3. Verify the new attach slow-consumer mechanism preserves deterministic bounded behavior, cutover ordering, receipts, and detach-without-stop. Challenge reliance on scheduler timing or wall-clock grace if it can hide a real slow consumer or make behavior flaky.
4. Audit persistence changes: per-surface sidecar offsets/lengths, corruption checks, next-sequence restoration, notification export/import, previous-known-good, migrations, trust epoch/grant isolation, and no resurrection. Look for compatibility or partial-write defects.
5. Audit daemon/RPC/CLI behavior and black-box E2E assertions. Confirm the 20 flows are distinct meaningful flows against the real assembly; validate JSON schemas, typed exits, confirmation fail-closed behavior, event gaps, lease takeover/release, socket ownership, version/boot behavior, and daemon shutdown.
6. Audit B5/B11 Linux-only deferrals. Accept compile-only evidence only where the plan permits T6 runtime validation; reject any portable claim that silently substitutes for required T4 implementation.
7. Verify T2 security conformance uses real backend enforcement from T4-owned registration without modifying the frozen securitytest corpus. Check the reported 26/26 count and race/timing meanings.
8. Reproduce or inspect evidence for gofmt, vet, 589 tests, full race suite, attach stress, fuzz smoke, Linux amd64/arm64 and no-cgo builds, module verify/tidy, and scope compliance. Scanner absence must remain an honest deferral, not a pass.
9. Validate the receipt itself: exactly one strict `guild.handoff.v2`, accurate paths/counts/risks, no overclaim, and T5/T6 handoffs sufficient for downstream work.

Return only one valid `review_result.v1` JSON object for packet `G-lane-T4-backend-r1`, exact reviewed SHA-256 `13003652210bacb8d838e6194ad44feeb2d22fea7f8cc2b4b5f2d992436b25ce`, round `1`, reviewer host `codex`. A satisfied result must have empty `findings` and `blocking_findings`. Every issue must cite exact current file evidence and identify the violated B1-B12 or acceptance contract; blocking findings must be truly gate-blocking, not stylistic.
