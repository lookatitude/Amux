// Package ordering is an EXECUTABLE MODEL of the ADR-0004 concurrency and
// ordering contract. It is deliberately not the production runtime: T4 backend
// implements the real control actor, session actors, attach manager, and hook
// launch path. This package exists so the architect can PROVE the load-bearing
// happens-before properties with `go test -race` before any of that code is
// written, and so the proofs remain regression-guarded as the design evolves.
//
// The properties proved here:
//
//   - Event sequences are allocated only after a command commits, and are
//     strictly monotonic and gap-free per session even under concurrent
//     submission (single owning goroutine serializes).
//   - The daemon-global control actor and per-session actors communicate by
//     one-way messages with revision/epoch checks; no actor holds mutable state
//     while blocked on another (no nested waits, no deadlock under -race).
//   - Hook launch authorization linearizes at a single point in the control
//     actor. Across two sessions sharing one project: a revoke that linearizes
//     first creates zero children; a launch that linearizes first proceeds and
//     the later revoke drives terminate. No launch linearizes after a completed
//     revoke of the same project epoch.
//   - The attach replay/live cutover produces a contiguous, duplicate-free and
//     gap-free stream across the snapshot boundary.
//   - The input-lease state machine admits exactly one holder and rejects writes
//     from non-holders.
package ordering
