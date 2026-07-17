package model

// This file freezes the presentation enums the TUI routes on. Every value is a
// projection of daemon/client state — the TUI classifies for DISPLAY only and
// never decides authority (lease ownership, trust, restore class, and event
// ordering are all the daemon's). The string forms are stable so golden frames
// and the accessible CLI alternative agree.

// SurfaceClass is the restore/exit classification a surface reports. It mirrors
// the daemon's SurfaceInfo.Class / restore RestoredSurface.Class vocabulary so
// the UI never invents process resurrection (spec success criterion 5).
type SurfaceClass string

const (
	ClassUnknown   SurfaceClass = ""          // not yet classified
	ClassLive      SurfaceClass = "live"      // process is running
	ClassRestarted SurfaceClass = "restarted" // relaunched on restore (completed behavior, not intent)
	ClassStopped   SurfaceClass = "stopped"   // process exited / not relaunched
)

// Label is a human-facing, stable label for a surface class.
func (c SurfaceClass) Label() string {
	switch c {
	case ClassLive:
		return "live"
	case ClassRestarted:
		return "restarted"
	case ClassStopped:
		return "stopped"
	default:
		return "unknown"
	}
}

// AttachPhase is the client-facing attachment lifecycle the recovery UX
// presents (U5). It is derived from the shared client's typed results and error
// codes (client.ErrBootChanged, v1.ErrEventGap, v1.ErrReplayGap,
// v1.ErrResourceExhausted, connection-lost) and the attach snapshot — the TUI
// never stitches sequence gaps or owns attach ordering.
type AttachPhase string

const (
	PhaseIdle            AttachPhase = "idle"             // no attach requested
	PhaseConnecting      AttachPhase = "connecting"       // dialing / negotiating
	PhaseReplaying       AttachPhase = "replaying"        // draining replay up to cutover N
	PhaseLive            AttachPhase = "live"             // live output after cutover
	PhaseReadOnly        AttachPhase = "read_only"        // attached without the input lease
	PhaseGapRecovery     AttachPhase = "gap_recovery"     // replay_gap/event_gap: re-snapshot required
	PhaseSlowDetached    AttachPhase = "slow_detached"    // daemon detached us as a slow consumer
	PhaseDisconnected    AttachPhase = "disconnected"     // connection lost; Redial pending
	PhaseDaemonRestarted AttachPhase = "daemon_restarted" // boot id changed; snapshot + re-subscribe
	PhaseStopped         AttachPhase = "stopped"          // surface process is stopped
)

// Recoverable reports whether the phase is a recoverable boundary the operator
// (or an auto-recovery command) can act on rather than a steady state.
func (p AttachPhase) Recoverable() bool {
	switch p {
	case PhaseGapRecovery, PhaseSlowDetached, PhaseDisconnected, PhaseDaemonRestarted:
		return true
	default:
		return false
	}
}

// LeaseState is the client-facing input-lease ownership the UI shows (U4/U5).
// It is derived from LeaseNotice transitions and input.send results — the
// daemon owns the lease state machine; the UI only reflects it.
type LeaseState string

const (
	LeaseNone     LeaseState = "none"      // no lease held anywhere we can see
	LeaseOwned    LeaseState = "owned"     // this client holds the lease (writable)
	LeaseOther    LeaseState = "other"     // another client holds it (read-only)
	LeaseReadOnly LeaseState = "read_only" // this attach is explicitly read-only
	LeaseLost     LeaseState = "lost"      // we were taken over / disconnected
)

// Writable reports whether input may be sent under this lease state.
func (l LeaseState) Writable() bool { return l == LeaseOwned }

// NotificationKind mirrors the daemon notification kind vocabulary; the TUI
// routes and styles on it but does not define delivery semantics.
type NotificationKind string

const (
	NotifyInfo      NotificationKind = "info"
	NotifyAttention NotificationKind = "attention"
	NotifyExit      NotificationKind = "exit"
	NotifyHook      NotificationKind = "hook"
	NotifyError     NotificationKind = "error"
)

// DeliveryState presents notification delivery health (U6). Delivery is the
// daemon/notifier's advisory concern (ADR-0005 store authority is preserved on
// notifier error); the UI only surfaces the reported state.
type DeliveryState string

const (
	DeliveryOK     DeliveryState = "ok"
	DeliveryFailed DeliveryState = "failed" // OS notifier failed; inbox remains authoritative
)
