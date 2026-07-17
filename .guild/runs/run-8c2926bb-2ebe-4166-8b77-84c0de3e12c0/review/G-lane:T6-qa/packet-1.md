---
packet_id: G-lane-T6-qa-r1
gate: G-lane:T6-qa
run_id: run-8c2926bb-2ebe-4166-8b77-84c0de3e12c0
author_host: claude-code-cli
reviewer_host: codex-cli
artifact_path: .guild/runs/run-8c2926bb-2ebe-4166-8b77-84c0de3e12c0/handoffs/qa-T6-qa.md
artifact_sha256: 0000879b88ff27158f23a949e67e3c8487bf6e62f0eabcaed6740726ab5c3064
independence: strong
prior_round: 0
---

# G-lane review packet — T6 QA — round 1

Perform a skeptical, repository-grounded review of the exact blocked T6
receipt, current live implementation, and evidence artifacts. The review must
validate the truthfulness and completeness of the receipt; it must not treat a
truthful blocked receipt as a satisfied release gate.

## Required checks

1. Confirm the claimed 30-minute Arch x86_64 soak actually ran for at least
   30 minutes with 20 PTYs, and that its gap/orphan/trend/result claims are
   supported by the raw log and summary rather than inferred.
2. Distinguish findings in the fresh security review that were closed by later
   T6 evidence: check-receipt creation (review F-7), completed 30-minute soak
   (F-9), and clean fuzz rerun (F-10). Do not report these as current blockers
   if the later artifacts support closure.
3. Independently validate each current pinned blocker and severity:
   - ReplayRead ignoring MaxBytes and the resulting oversized unary response;
   - overlayfs root replacement preserving the durable identity tuple;
   - GoReleaser pin/config incompatibility and before-hook output behavior;
   - vacuous readiness-manifest test patterns;
   - missing `amux --version`;
   - missing owner-safe TMPDIR in hosted CI and unignored generated artifacts.
4. Confirm the receipt does not fabricate reference-profile performance,
   eight-hour soak, AUR clean-chroot, hosted CI, interactive TTY, history-mode
   secrets scanning, or commit-bound provenance evidence.
5. Inspect artifact paths and working-tree residue, including whether the old
   package-relative nested `.amux-artifacts` directory still exists.
6. Verify the QA additions are test/evidence infrastructure and do not silently
   redefine the frozen product contract.

## Independent orchestrator reproduction already completed

- `go test -count=1 -tags integration -run ResourceExhaustion ./internal/daemon`
  fails at `TestResourceExhaustionOutputFloodAndClientBurst`.
- `make release-check` under the pinned tool fails on archive `ids`, `formats`,
  and Linux/Darwin `default_file_info` fields.
- `go run ./cmd/amux --version` exits with an unknown flag error.
- `go test -count=1 -tags integration -run TrustMatrixReplay ./...` exits zero
  while reporting no matching tests.
- Ubuntu 24.04 overlayfs reproduction fails
  `TestReplacedRootChangesIdentity` because the replacement receives the same
  project key.

Return one valid `review_result.v1` object for packet `G-lane-T6-qa-r1`,
reviewed SHA `0000879b88ff27158f23a949e67e3c8487bf6e62f0eabcaed6740726ab5c3064`,
round 1, reviewer host `codex`. Use verdict `issues` when current release
blockers remain. Cite exact files, tests, commands, or evidence paths for every
finding. Do not mutate the repository.
