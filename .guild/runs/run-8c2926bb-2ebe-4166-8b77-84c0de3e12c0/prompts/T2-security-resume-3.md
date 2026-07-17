Adopt the project-local security specialist definition at `.guild/agents/security.md` and resume `T2-security` from the authoritative bundle `.guild/context/run-8c2926bb-2ebe-4166-8b77-84c0de3e12c0/security-T2-security.md`.

This is an operator-initiated R-016 resume with a fresh retry budget. Prior attempts produced substantial scoped artifacts but no receipt. The last failure was: "Attempt 2 crossed the 600000 ms liveness timeout, could not accept the required nudge because wrapper stdin was closed, remained stalled on the next deterministic sweep, and emitted no receipt."

Work narrowly from the preserved checkpoint. Inspect the current `docs/security/**`, `internal/securitytest/**`, `testdata/security/**`, and `.gitleaks.toml` artifacts; do not recreate correct work. Close only genuine gaps against the T2 success criteria, run the safe verification available on this host, perform the required scope audit, and write the durable receipt at `.guild/runs/run-8c2926bb-2ebe-4166-8b77-84c0de3e12c0/handoffs/security-T2-security.md` as your final filesystem action. Keep all writes within `.guild/runs/run-8c2926bb-2ebe-4166-8b77-84c0de3e12c0/scope/T2-security.json`.

Prioritize completion over another broad research pass: the existing security corpus is already extensive and the current repository suite is green. The receipt must honestly distinguish implementation-neutral fixture evidence from T4/T6 integrated prerequisites, report unavailable scanners as deferred rather than clean, contain concrete changed files/evidence/risks/followups, and embed exactly one valid `guild.handoff.v2` block with summary at most 600 characters and notes at most 200 characters. Do not implement backend production behavior, modify T1 contracts, execute real untrusted hooks, publish, or weaken any security guarantee.

When you finish, emit your result as a SINGLE fenced code block and nothing after it:
```guild.handoff.v2
{ ... a valid guild.handoff.v2 object ... }
```
The block MUST validate against the `guild.handoff.v2` contract. Do not add prose after it.
