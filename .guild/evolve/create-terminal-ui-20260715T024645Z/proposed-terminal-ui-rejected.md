---
type: specialist-creation-gate-result
run_id: create-terminal-ui-20260715T024645Z
role: terminal-ui
outcome: rejected
gate_failed: extraction-signals
proposed_path: .guild/agents/proposed/terminal-ui.md
live_path: null
created_at: 2026-07-15T02:46:45Z
---

# Proposed terminal-ui specialist — extraction gate result

The interview and incubation draft completed. The proposal contains one agent definition, four focused skill definitions, five specialist positive routing cases, six specialist negative cases, and twelve positive plus twelve negative skill-routing cases.

The specialist cannot be promoted because Guild requires all five extraction signals and only two pass:

- Failed: recurring cluster across three unrelated tasks (`1/3`).
- Failed: an adjacent specialist above the deterministic boundary threshold (`0` at or above `0.35`).
- Passed: context-isolation payoff (the proposed agent/skills alone exceed approximately 2,000 tokens).
- Failed: three prior reflections or team-compose gap records (`0/3`).
- Passed: sufficient positive and negative evaluation cases.

Boundary-edit evolve gates, paired specialist evaluation, historical shadow mode, and registration were not run because the extraction gate failed first.

## Refinement options

1. Keep the proposal incubating and collect `terminal-ui` gap evidence in at least three unrelated future Guild runs, then reopen this creation run.
2. Re-interview and broaden or narrow the trigger boundary only if future routing evidence shows a real adjacent collision.
3. Abandon the novel role and use the standard `frontend` specialist with an explicit terminal-only scope override for the current plan.
4. Omit a dedicated terminal-UI lane and assign its scope to `backend`, accepting reduced context isolation.

The proposal remains under `.guild/agents/proposed/` and `.guild/skills/proposed-terminal-ui-*`; it is not enumerated by team composition and must not be dispatched as a live specialist.
