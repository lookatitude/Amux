# L2 Architect Handoff — Round 1

- run_id: `run-8c2926bb-2ebe-4166-8b77-84c0de3e12c0`
- loop: `L2`
- lane: `phase:plan`
- role: `architect`
- verdict: `revise`

## Plan defects

1. The plan names per-session actors but no daemon-global authority for the session registry and project-scoped trust epochs. A1/A4 and B2/B11 must define cross-session serialization, final pre-spawn validation, lock/actor ordering, and a same-project/two-session revocation test.
2. Whole-lane dependencies are too coarse and contradict handoffs. B11 needs S2–S5, S4 needs the backend pre-spawn/process seam, D3/D4 need backend release inputs, TUI packages have different backend readiness points, and Q1 must start after T1 rather than after every implementation lane.
3. PRD Wave 3 cannot complete all 20 CLI flows before Wave 4 implements snapshot/restore and notifications. Move those completion gates to Wave 4.
4. Process groups alone cannot satisfy zero unmanaged descendants after daemon `SIGKILL`. Add a Linux containment spike/ADR for supervised launcher or parent-death mechanisms and test shells that create grandchildren.
5. JSON snapshots, replay bytes, and SQLite overlap without a canonical-store/checkpoint/recovery precedence. ADR 0005 must define generation IDs, sidecar replay representation, cross-file crash ordering, global budgets, and restore precedence.

## Dismissed questions

- Go/Bubble Tea remains a coherent replaceable architecture with one authority.
- Provisional `x/ansi`, UUIDv7, and release-tool choices are valid because evidence-backed spikes gate adoption.
- Linux-only scope and TUI ownership boundaries are clear.
- The implementation model policy correctly maps powerful lanes to 5.6 Sol and bounded DevOps mechanics to Terra.

## Handoff receipt

```guild.handoff.v2
{
  "loop_id": "loop-plan-review",
  "lane_id": "phase:plan",
  "round": 1,
  "role": "architect",
  "status": "revise",
  "next": "security challenge",
  "dismissed_questions": [
    {"question": "Does Go plus Bubble Tea create a second authority?", "rationale": "No; the daemon remains authoritative and the client boundary is explicit."},
    {"question": "Must provisional library spikes block the plan?", "rationale": "No; selection is explicitly evidence-gated behind frozen interfaces."}
  ]
}
```
