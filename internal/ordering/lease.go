package ordering

// ClientID identifies an attached client in the lease model.
type ClientID string

// LeaseState is the input-lease state machine for one surface (ADR-0004 / spec
// "Client attach and detach contract"). Output is always shared; input requires
// the single lease. It is a pure value type: every transition returns a new
// state plus whether an event should be emitted, so it is trivially testable and
// has no hidden concurrency (the owning surface serializes calls).
type LeaseState struct {
	held   bool
	holder ClientID
}

// LeaseEvent names an observable lease transition (all are evented per spec).
type LeaseEvent string

const (
	LeaseNone      LeaseEvent = ""
	LeaseAcquired  LeaseEvent = "lease_acquired"
	LeaseTakenOver LeaseEvent = "lease_taken_over"
	LeaseReleased  LeaseEvent = "lease_released"
	InputRejected  LeaseEvent = "input_rejected"
)

// Acquire grants the lease if free. If already held by another client it does
// NOT implicitly take over — the caller must call Takeover deliberately.
func (s LeaseState) Acquire(c ClientID) (LeaseState, LeaseEvent, bool) {
	if s.held && s.holder != c {
		return s, LeaseNone, false
	}
	return LeaseState{held: true, holder: c}, LeaseAcquired, true
}

// Takeover forcibly transfers the lease to c and emits a takeover event.
func (s LeaseState) Takeover(c ClientID) (LeaseState, LeaseEvent) {
	return LeaseState{held: true, holder: c}, LeaseTakenOver
}

// Release frees the lease if c holds it; otherwise it is a no-op (a non-holder
// releasing is not an error, it simply owns nothing).
func (s LeaseState) Release(c ClientID) (LeaseState, LeaseEvent) {
	if s.held && s.holder == c {
		return LeaseState{}, LeaseReleased
	}
	return s, LeaseNone
}

// Disconnect frees the lease if c held it (detach releases the lease but never
// stops the PTY — that invariant lives in the attach/supervisor code, not here).
func (s LeaseState) Disconnect(c ClientID) (LeaseState, LeaseEvent) {
	return s.Release(c)
}

// WriteAllowed reports whether c may send input. Input from a non-holder is
// rejected before reaching the PTY.
func (s LeaseState) WriteAllowed(c ClientID) (bool, LeaseEvent) {
	if s.held && s.holder == c {
		return true, LeaseNone
	}
	return false, InputRejected
}

// Holder returns the current holder and whether the lease is held.
func (s LeaseState) Holder() (ClientID, bool) { return s.holder, s.held }
