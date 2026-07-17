Adopt the project-local QA specialist definition at `.guild/agents/qa.md` and
execute T6-qa from the formally assembled bundle
`.guild/context/run-8c2926bb-2ebe-4166-8b77-84c0de3e12c0/qa-T6-qa.md`.

Implement and execute Q1-Q8 autonomously within the frozen contracts. Reopen
the live source and reviewed upstream receipts; do not trust old counts. Build
the durable traceability, test, fuzz/property, fault, acceptance, performance,
soak, package, and release-candidate evidence system. Run every gate the current
environment can genuinely execute. Detect available Linux/container/CI tooling
before deferring it. Never fabricate native Linux, Arch, clean-chroot, scanner,
elapsed-soak, or performance evidence, and never weaken/reclassify a blocking
gate without approval.

If a production defect prevents a required gate, pin it with the narrowest
reproduction and report it as a blocking issue rather than changing application
semantics outside QA ownership. Test-only fixtures and harnesses are in scope.
Do not publish releases, push AUR, commit, or change external state.

Finish with current reproducible commands, environment metadata, residual risks,
and a single valid `guild.handoff.v2` receipt at
`.guild/runs/run-8c2926bb-2ebe-4166-8b77-84c0de3e12c0/handoffs/qa-T6-qa.md`.
Emit exactly one valid `guild.handoff.v2` fenced object with summary <=600
characters and notes <=200, and no prose after it.
