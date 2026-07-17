Adopt `.guild/agents/security.md` and close exactly the two blocking findings in
`.guild/runs/run-8c2926bb-2ebe-4166-8b77-84c0de3e12c0/review/G-lane:T2-security/result-2.json`.
Use Sol/Fable, TDD-first, and preserve the accepted replacement-discriminator
and manifest-binding design.

F1 — make every audited project transition, especially operator revocation and
replacement invalidation, durably atomic across:

- project state + monotonic epoch + replacement discriminator;
- deactivation of all currently active grants;
- the main project audit record and one grant-inactive record per affected
  grant.

Introduce one explicit `TrustStore` transaction/commit primitive rather than
sequencing `SaveProject`, `DeactivateGrants`, and ignored `AppendAudit` calls.
The SQLite adapter must execute the whole transition in one real SQL
transaction and return the exact inactive grant IDs only after commit. The
memory implementation must provide equivalent all-or-nothing semantics. The
actor must update in-memory project/grant state and notify revoke listeners
only after durable commit succeeds. On any failure, disk and memory must both
remain at the old approved epoch/state with grants active and no partial audit;
a retry must succeed without `ErrEpochNotMonotonic`. Audit failures must be
returned, never ignored for these transitions. Apply the same primitive to
approve/deny/revoke where the frozen audit contract requires durability; keep
`RecordAudit` behavior scoped and documented if it remains best-effort.

Add deterministic failure-injection tests for each transaction stage and both
memory/SQLite paths. Prove no observer can authorize from partially committed
state, retry safety, monotonic epochs, exact audit ordering/count, listener
notification only after commit, and restart rehydration after both failure and
success. Preserve SQLite history and forward-only migrations.

F2 — replace the stale frozen-path `integration-second-uid` evidence and
receipt using the already executed current Arch/Ubuntu integration logs or a
fresh exact manifest run. The receipt command must be byte-identical to the
manifest, name the actual Linux host/container, report substantive top-level
RUN/PASS/SKIP counts, contain zero reference to the retired stub, and satisfy
the amended receipt rules. Add/extend receipt validation so stale evidence
that names a nonexistent/retired test cannot pass.

Run focused atomicity/failure tests, control/store/daemon/hooks/security tests,
exact manifest commands, full tests, full race tests, vet, tidy/verify, Linux
no-cgo builds, and at least the focused Linux container trust + second-UID
gates. Do not alter backend replay/version, release tooling/CI, TUI, research,
or weaken the frozen key/discriminator policy. Replace the T2 handoff with an
exact receipt and emit exactly one valid `guild.handoff.v2` object.
