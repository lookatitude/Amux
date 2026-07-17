// Package attachstate is the pure client-side attachment/recovery state machine
// (U5). It projects the daemon's attach lifecycle — snapshot → replay → live,
// input-lease transitions, and the typed boundary errors (replay_gap,
// event_gap, slow-consumer detach, connection loss, daemon restart) — onto the
// model.AttachPhase / model.LeaseState the UI presents, and it names WHICH
// backend recovery API the caller must invoke. It never stitches sequence gaps,
// never re-orders frames, and never owns attach sequencing: on any gap it fails
// to a visible recovery boundary and defers to the daemon's recovery APIs. The
// machine is deterministic, so recovery UX is golden-testable.
package attachstate

import "github.com/amux-run/amux/internal/tui/model"

// ErrKind is a transport/stream boundary the machine reacts to. The client
// adapter maps api/v1 error codes and client.ErrBootChanged onto these so this
// package stays free of the wire types.
type ErrKind int

const (
	ErrNone         ErrKind = iota
	ErrReplayGap            // v1.ErrReplayGap: requested replay cursor evicted
	ErrEventGap             // v1.ErrEventGap: event cursor past retained window
	ErrSlowConsumer         // v1.ErrResourceExhausted on attach: we lagged
	ErrConnLost             // retryable connection-lost (transport failure)
	ErrBootChanged          // client.ErrBootChanged: daemon restarted
)

// Recovery names the backend recovery action the caller must perform. The
// machine only recommends; the caller invokes the actual daemon API.
type Recovery int

const (
	RecNone       Recovery = iota
	RecRedial              // client.Redial (connection lost)
	RecReSnapshot          // snapshot + re-subscribe (event gap / daemon restart)
	RecReattach            // re-open attach from the latest cutover (slow detach / replay gap)
)

func (r Recovery) String() string {
	switch r {
	case RecRedial:
		return "redial"
	case RecReSnapshot:
		return "snapshot_and_resubscribe"
	case RecReattach:
		return "reattach_from_latest"
	default:
		return "none"
	}
}

// GapInfo carries the visible boundary numbers for a replay gap, straight from
// the daemon's AttachSnapshot.ReplayGap — presentation only.
type GapInfo struct {
	RequestedFrom  uint64
	OldestRetained uint64
	LatestSeq      uint64
}

// State is the full attachment presentation state.
type State struct {
	Phase    model.AttachPhase
	Lease    model.LeaseState
	Recovery Recovery
	// Gap is set when a replay gap boundary is present (informational; live
	// continues). Nil otherwise.
	Gap *GapInfo
	// UpToSeq is the last cutover the machine observed (presentation only; not a
	// client-owned cursor — the daemon owns ordering).
	UpToSeq uint64
}

// Machine is the attachment state machine. The zero value is Idle.
type Machine struct {
	st State
}

// New returns a machine in the Idle phase.
func New() *Machine { return &Machine{st: State{Phase: model.PhaseIdle, Lease: model.LeaseNone}} }

// State returns the current state.
func (m *Machine) State() State { return m.st }

// Connecting marks the start of an attach (dial/negotiate).
func (m *Machine) Connecting() State {
	m.st = State{Phase: model.PhaseConnecting, Lease: m.st.Lease}
	return m.st
}

// Snapshot records receipt of the attach snapshot at cutover upToSeq. gap is
// non-nil when the daemon reported a replay_gap boundary; readOnly marks an
// attach that did not (yet) hold the input lease. After the snapshot the client
// drains replay up to the cutover, so the phase becomes Replaying.
func (m *Machine) Snapshot(upToSeq uint64, gap *GapInfo, readOnly bool) State {
	m.st.Phase = model.PhaseReplaying
	m.st.UpToSeq = upToSeq
	m.st.Gap = gap
	m.st.Recovery = RecNone
	if readOnly {
		m.st.Lease = model.LeaseReadOnly
	}
	return m.st
}

// ReplayComplete marks reaching the cutover: live output follows. The phase
// becomes Live, or ReadOnly when this client does not hold the input lease.
func (m *Machine) ReplayComplete() State {
	if m.st.Lease == model.LeaseReadOnly || m.st.Lease == model.LeaseOther || m.st.Lease == model.LeaseLost {
		m.st.Phase = model.PhaseReadOnly
	} else {
		m.st.Phase = model.PhaseLive
	}
	return m.st
}

// Live marks live output (used when the stream goes live without a distinct
// replay phase, e.g. FromSeq at the cutover).
func (m *Machine) Live() State {
	m.st.Gap = nil
	return m.ReplayComplete()
}

// Lease applies an input-lease transition (mapped from a daemon LeaseNotice).
// Ownership drives whether the phase is writable Live or ReadOnly.
func (m *Machine) Lease(l model.LeaseState) State {
	m.st.Lease = l
	switch l {
	case model.LeaseOwned:
		if m.st.Phase == model.PhaseReadOnly {
			m.st.Phase = model.PhaseLive
		}
	case model.LeaseOther, model.LeaseLost, model.LeaseReadOnly:
		if m.st.Phase == model.PhaseLive {
			m.st.Phase = model.PhaseReadOnly
		}
	}
	return m.st
}

// SurfaceStopped marks the surface's process as stopped (no live output).
func (m *Machine) SurfaceStopped() State {
	m.st.Phase = model.PhaseStopped
	m.st.Recovery = RecNone
	return m.st
}

// Error applies a boundary error, moving to the matching recovery phase and
// recommending the daemon recovery API. The machine never auto-recovers.
func (m *Machine) Error(k ErrKind) State {
	switch k {
	case ErrConnLost:
		m.st.Phase = model.PhaseDisconnected
		m.st.Recovery = RecRedial
	case ErrBootChanged:
		m.st.Phase = model.PhaseDaemonRestarted
		m.st.Recovery = RecReSnapshot
	case ErrEventGap:
		m.st.Phase = model.PhaseGapRecovery
		m.st.Recovery = RecReSnapshot
	case ErrReplayGap:
		m.st.Phase = model.PhaseGapRecovery
		m.st.Recovery = RecReattach
	case ErrSlowConsumer:
		m.st.Phase = model.PhaseSlowDetached
		m.st.Recovery = RecReattach
	}
	return m.st
}

// Recovered clears the recovery recommendation and returns to Connecting, to be
// followed by a fresh Snapshot once the caller re-attaches/re-subscribes.
func (m *Machine) Recovered() State {
	m.st.Phase = model.PhaseConnecting
	m.st.Recovery = RecNone
	m.st.Gap = nil
	return m.st
}

// NeedsRecovery reports whether the caller must invoke a recovery API.
func (s State) NeedsRecovery() bool { return s.Recovery != RecNone }
