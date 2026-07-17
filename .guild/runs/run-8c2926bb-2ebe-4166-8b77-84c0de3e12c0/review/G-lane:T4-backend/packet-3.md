---
packet_id: G-lane-T4-backend-r3
gate: G-lane:T4-backend
run_id: run-8c2926bb-2ebe-4166-8b77-84c0de3e12c0
author_host: claude-code-cli
reviewer_host: codex-cli
artifact_path: .guild/runs/run-8c2926bb-2ebe-4166-8b77-84c0de3e12c0/handoffs/backend-T4-backend.md
artifact_sha256: 2c2be5a3b524502bc6ce9a8132212c16df67cb6201fcd02416d06ff4008ad7c7
independence: strong
prior_round: 2
---

# G-lane review packet — T4 Backend — round 3

Perform a skeptical, repository-grounded re-review of the exact current T4 receipt and live implementation. Round 1 F1 (unreachable in-daemon live reconcile) was confirmed closed in round 2. Round 2 then found F2: automatic-policy restore reported `restarted` without launching a replacement PTY. Review the current files and exact receipt hash, not either archived artifact or the rework summary.

Primary gate question: is F2 now genuinely closed while F1 remains closed? Verify literally that:

1. `RestoreSnapshot` reports `restarted` only after a successful production supervisor spawn of a replacement PTY.
2. The replacement preserves the checkpoint surface identity and intended executable/argv/cwd/env/geometry; output enters the restored replay/VT runtime after checkpoint history and normal exit/input/attach callbacks remain wired.
3. Any unvouched predecessor is stopped before replacement; asynchronous retirement/conflict handling is bounded and cannot duplicate processes, hang indefinitely, or misclassify a failure.
4. Launch failure produces a truthful fail-closed stopped result/reason with no live owner, no false `restarted`, and no partially installed authority.
5. Same-checkpoint/same-spawn in-daemon ownership remains `live` without restart; manual policy remains stopped without launch; fresh-daemon never claims the original process live, though automatic policy may launch and truthfully report a new replacement.
6. Trust/grant/audit state remains excluded and monotonic, no original-process resurrection is claimed, and persisted/protocol compatibility is unchanged.
7. Production-path tests and the real CLI E2E distinguish restore relaunch from the separate explicit restart command and cover success/failure/manual/live/fresh paths meaningfully.

Inspect the reported changes (`internal/daemon/snapshot.go`, `internal/daemon/restore_test.go`, `cmd/amux/e2e_test.go`) for races and lifecycle regressions. Pay special attention to the bounded 5-second conflict retry, supervisor ownership maps, stop/exit callbacks, restored output ordering, fresh-daemon behavior, error typing, and result patching. Do not reopen settled B1-B12 areas without a concrete regression.

Independent orchestrator verification for this exact artifact was green: exactly one strict handoff envelope with empty schema errors (summary 597, notes 177); focused daemon+CLI 21 pass; full 596/33 normal and 596/33 race; attach 300 normal + 75 race; security/hooks 32 pass; vet, module verify/tidy diff, Linux amd64/arm64 no-cgo builds clean. These counts are not semantic proof; reproduce focused checks where useful.

Return only one valid `review_result.v1` JSON object for packet `G-lane-T4-backend-r3`, reviewed SHA `2c2be5a3b524502bc6ce9a8132212c16df67cb6201fcd02416d06ff4008ad7c7`, round `3`, reviewer host `codex`. A satisfied result must have empty `findings` and `blocking_findings`. Every issue must cite exact current evidence and a violated acceptance contract.
