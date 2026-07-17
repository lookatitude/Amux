// Package attach implements the per-surface attach manager and input-lease
// controller (T4 work package B7) enforcing the ordering contract frozen in
// ADR-0004 ("Event, attach, and launch/revoke ordering").
//
// A Surface binds three things for one terminal surface: the raw-output
// authority Ring (internal/terminal), a source of the current derived cell
// snapshot, and a single per-surface input sink into the PTY supervisor. All
// raw output flows through Surface.OnOutput, which appends to the ring and fans
// the new chunk out to attached observers under ONE lock — the linearization
// point the whole package is built around.
//
// Attach cutover (ADR-0004 §"Attach replay/live cutover"). Attach delivers, in
// this exact order: (1) an atomic AttachSnapshot (cell snapshot + pane metadata)
// captured at output sequence N under the surface lock, (2) bounded raw replay
// from the ring ending EXACTLY at N, then (3) live raw output strictly greater
// than N. Because the snapshot read, the replay bounds, and the observer
// registration all happen under the single OnOutput lock, a concurrent output
// chunk is either in the replay (seq <= N) or in the live feed (seq > N) —
// never both (a duplicate) and never neither (a gap). This is the invariant
// internal/ordering.AttachCutover proves; the tests here assert it end to end
// under -race. When the ring has already evicted the client's requested floor,
// replay starts at OldestRetainedSeq and the AttachSnapshot carries an explicit
// ReplayGap boundary — the stream is never silently started mid-history
// (ADR-0004: gaps are explicit, typed, never a silent bridge).
//
// Slow consumers (ADR-0004 §slow-consumer boundary). Each attachment has a
// bounded live buffer; an attachment that fails to drain is disconnected with
// Attachment.Err() == ErrSlowConsumer and a LastDelivered receipt, while every
// other attachment and the surface itself stay healthy.
//
// Input leases (ADR-0004 §"Input-lease state machine"). Output is shared; input
// requires the single per-surface lease modeled by internal/ordering.LeaseState.
// AcquireInput never implicitly takes over another holder; TakeoverInput is
// deliberate and evented; a non-holder Write is rejected with ErrNotLeaseHolder
// (mapped to v1.ErrNotInputLeaseHolder) BEFORE any byte reaches the sink. Detach
// and slow-consumer disconnect release the lease but NEVER stop the surface or
// its PTY sink.
package attach
