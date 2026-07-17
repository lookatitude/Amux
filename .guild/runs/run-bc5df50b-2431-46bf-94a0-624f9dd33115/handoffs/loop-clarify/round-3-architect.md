# L1 Architect Handoff — Round 3

- run_id: `run-bc5df50b-2431-46bf-94a0-624f9dd33115`
- loop: `L1`
- lane: `phase:brainstorm`
- role: `architect`
- dispatch: Guild Codex host adapter, read-only, model `gpt-5.4`
- status: `amended`

## Hook CWD capability

Hook current-working-directory access is a distinct capability and is denied by default. Without a CWD grant, hooks execute in an Amux-owned scratch directory with no implicit access to the invoking pane or project path. A grant selects exactly one scope: `fixed`, `workspace-primary`, or `pane`. The daemon resolves and validates the runtime path against the approved scope before launch; validation failure denies execution.

## Two-layer trust and grant binding

The operator first opts a project into reading hook configuration. Each hook then requires a grant bound to its executable absolute path, content/config digest, event set, CWD scope, environment-key allowlist, timeout, and output cap. Any change invalidates the grant and requires reapproval. CLI and TUI expose inspect, approve, deny, and revoke. Noninteractive first use without a valid grant fails closed and prints actionable recovery instructions.

## Acceptance tests

1. A `workspace-primary` hook invoked with a resolved cwd outside that root is denied before process launch; the same hook inside the root executes.
2. Changing any grant-bound field makes the next noninteractive invocation fail without launching the hook and directs the operator to inspect and approve the new grant.

## Handoff receipt

- loop_id: `loop-clarify`
- lane_id: `phase:brainstorm`
- round: 3
- role: `architect`
- status: `amended`
- next: `researcher challenge`
