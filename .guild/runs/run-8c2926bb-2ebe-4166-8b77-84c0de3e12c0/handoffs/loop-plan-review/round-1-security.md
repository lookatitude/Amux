# L2 Security Handoff — Round 1

- run_id: `run-8c2926bb-2ebe-4166-8b77-84c0de3e12c0`
- loop: `L2`
- lane: `phase:plan`
- role: `security`
- status: `questions`

## Plan-defect questions

1. Define one daemon-global authority for project trust epochs and hook authorization across sessions, with lock/actor ordering and same-project/two-session revoke barriers.
2. Replace contradictory whole-lane handoffs with an acyclic executable DAG; backend hook work must consume completed security contracts without security depending on backend completion.
3. Close executable/config/project TOCTOU races with an object-identity-bound or equivalently race-safe launch protocol and deterministic replacement/symlink tests.
4. Require Linux peer credentials and safe ownership/mode/symlink checks for every runtime/socket path component; diagnostics must remain owner-only and non-networked.
5. Prevent old snapshot/SQLite generations from decreasing trust epochs, reactivating grants, or erasing inactive audit history.
6. Add the two required 250 ms hook gates for absent trust and queued-work cancellation, including cross-session queues.
7. Gate implementation on a Linux containment mechanism that survives daemon `SIGKILL` and attempts by grandchildren to escape process groups.
8. Freeze an interactive/noninteractive confirmation matrix for destruction, process stop, lease takeover, hook approval, and trust revocation.
9. Freeze each selected agent adapter's transport, schema/size, filesystem/environment/process capabilities, failure isolation, and inability to mutate graph or launch project code outside approved daemon commands/hooks.

## Handoff receipt

```guild.handoff.v2
{
  "loop_id": "loop-plan-review",
  "lane_id": "phase:plan",
  "round": 1,
  "role": "security",
  "status": "questions",
  "next": "architect revision",
  "unresolved_questions": 9
}
```
