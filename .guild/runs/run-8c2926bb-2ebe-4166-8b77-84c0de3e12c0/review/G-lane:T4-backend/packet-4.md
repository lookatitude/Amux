---
packet_id: G-lane-T4-backend-r4
gate: G-lane:T4-backend
run_id: run-8c2926bb-2ebe-4166-8b77-84c0de3e12c0
author_host: claude-code-cli
reviewer_host: codex-cli
artifact_path: .guild/runs/run-8c2926bb-2ebe-4166-8b77-84c0de3e12c0/handoffs/backend-T4-backend.md
artifact_sha256: 5956114089e1c9f09047573d7b9aeb6f03380020645fdc5e0499051712858651
independence: strong
prior_round: 3
---

# G-lane review packet — T4 Backend — round 4

Perform a skeptical, repository-grounded review of the exact receipt and live
implementation. Rounds 1–3 accepted B1–B12 after restore/reconcile rework. T5's
first attempt then exposed five missing upstream integration contracts, so T4
was reopened. This round reviews that contract-completion delta while ensuring
the previously accepted backend remains intact.

Primary gate questions:

1. Does the x/ansi v0.11.7 migration preserve streaming terminal semantics,
   including arbitrary UTF-8 chunk boundaries, grapheme widths, CSI overflow
   safety, unsupported sequences, replay determinism, and unchanged goldens?
2. Are Bubble Tea v2.0.8 and Lip Gloss v2.0.5 now co-resolvable without a
   dependency conflict, with truthful frozen module/license manifests and no
   hidden cgo or platform regression?
3. Does `surface.cells` expose a daemon-owned, bounded, immutable cell snapshot
   and delta gate without making the TUI parse raw VT? Is optional attach cell
   data backward-compatible and read-only?
4. Does `hook.inspect` expose all frozen trust/grant/runtime detail needed by
   the TUI without leaking secrets or mutating trust/audit state?
5. Does `pane.context` use the production B10 collector seams for cwd,
   foreground process, and repository context, and fail closed when unavailable?
6. Does `workspace.tree` faithfully project the authoritative split tree,
   geometry, focus, surface identity, and revision without creating a second
   mutable authority?
7. Are all four projection methods additive protocol-minor evolution with
   strict old-client compatibility, deterministic golden vectors, typed client
   methods, real daemon/socket tests, reconnect behavior, and correct errors?
8. Did the rework preserve the already accepted PTY, attach, restore, lease,
   persistence, hook, security, CLI, and Linux no-cgo behavior?

Inspect at minimum `internal/terminal`, `internal/rpcapi/projections.go`,
`internal/daemon/projections.go`, `internal/client/methods.go`,
`internal/domain/treeview.go`, the production wiring, tests/goldens, `go.mod`,
dependency manifests, and the exact receipt. Do not accept claims based only on
the handoff. Do not reopen unrelated settled areas without concrete regression
evidence.

Independent orchestrator evidence for this exact workspace is green: focused
projection/terminal/backend packages 169 tests; full suite 690 tests across 44
packages; vet; module verification; frozen dependency manifest; license gate;
Linux amd64 no-cgo build. These counts are not semantic proof; reproduce focused
checks where useful.

Return only one valid `review_result.v1` JSON object for packet
`G-lane-T4-backend-r4`, reviewed SHA
`5956114089e1c9f09047573d7b9aeb6f03380020645fdc5e0499051712858651`, round 4,
reviewer host `codex`. A satisfied result must have empty `findings` and
`blocking_findings`; every issue must cite exact live evidence and a violated
acceptance contract.
