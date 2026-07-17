// Package control implements the daemon-global control actor (ADR-0001): the
// single owner of the session registry, project identities, project trust
// epochs, hook grants, and launch authorization. It runs one goroutine and
// serializes every trust/registry transition; peripheral producers submit
// immutable messages and never mutate owned state directly.
//
// The package splits into three layers:
//
//   - decide.go — the PURE authorization decision function. Given fully
//     resolved ActivationFacts it returns the frozen trust-matrix outcome
//     (testdata/security/trust-matrix.json); the matrix replay test pins every
//     row against it. No I/O, no clock, no state.
//   - actor.go — the single-goroutine actor: registry + trust + grant state,
//     bounded mailbox, the AuthorizeLaunch linearization point (ADR-0004: a
//     launch authorized here can never have been ordered after a revoke), and
//     revocation listeners for in-flight cancellation.
//   - store.go — the TrustStore write-through seam. Trust epochs, grants, and
//     audit are durable in SQLite ONLY (ADR-0005); the actor owns the live
//     state and writes through. Every audited project transition commits as
//     ONE ApplyTransition unit — state + epoch + discriminator, grant
//     deactivation, and all audit records land together or not at all, and
//     the actor mutates memory / notifies listeners only after that durable
//     commit (G-lane F1). An in-memory store backs tests.
//
// Ordering rules honored here (ADR-0001 §Cross-actor ordering): the control
// actor never waits on a session actor while a trust transition is open;
// revocation listeners are invoked on the actor goroutine after the transition
// commits and MUST NOT call back into the actor synchronously.
package control
