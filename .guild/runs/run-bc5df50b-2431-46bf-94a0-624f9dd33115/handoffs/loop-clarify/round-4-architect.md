# L1 Architect Handoff — Round 4

- run_id: `run-bc5df50b-2431-46bf-94a0-624f9dd33115`
- loop: `L1`
- lane: `phase:brainstorm`
- role: `architect`
- status: `amended`

## Project trust acceptance amendment

The release suite must prove all of the following in one end-to-end test:

1. With project trust absent, triggering a configured hook returns `project_trust_required` within 250 ms, starts zero hook processes, and does not activate project hook configuration into the runnable set.
2. After project trust and a valid per-hook grant are both approved, exactly one requested hook process starts and its audit row references both grants.
3. Revoking project trust cancels all queued hook invocations within 250 ms, prevents future launches, and marks retained per-hook grants inactive without deleting audit history.
4. Regranting only project trust fails with `hook_grant_required`; regranting only the hook fails with `project_trust_required`; execution resumes only after both are freshly approved.
5. An in-flight hook receives one termination signal on revocation and, if still alive after a fixed 2-second grace period, one kill signal. The audit outcome records whether it exited during grace or required force-kill.

## Handoff receipt

- loop_id: `loop-clarify`
- lane_id: `phase:brainstorm`
- round: 4
- role: `architect`
- status: `amended`
- open_issues: 0
- next: `researcher challenge`
