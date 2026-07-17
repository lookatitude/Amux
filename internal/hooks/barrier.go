package hooks

import "sync"

// SyncPoint names a deterministic park location in the launch pipeline. The
// securitytest conformance fixtures use these to force both linearization
// orderings and the check-to-exec race window (contract.go SyncPoint). The
// values match the frozen strings so the conformance adapter maps 1:1.
type SyncPoint string

const (
	// SyncBeforeFinalValidation parks immediately before the final pre-spawn
	// authorization point (HA-14). Revoke-first / revoke-cancel fixtures
	// revoke while an activation is parked here.
	SyncBeforeFinalValidation SyncPoint = "before-final-validation"
	// SyncAfterObjectOpen parks after OpenBound + digest capture but before
	// final validation/exec — the window the races.* fixtures attack.
	SyncAfterObjectOpen SyncPoint = "after-object-open"
	// SyncAfterSpawn parks after the child exists but before the activation is
	// reported complete, so launch-first orderings are deterministic.
	SyncAfterSpawn SyncPoint = "after-spawn"
)

// barriers coordinates deterministic parking. It is a test/conformance
// affordance with zero cost on the production path: when no point is Held,
// reach() returns immediately. Only the conformance wiring calls Hold.
type barriers struct {
	mu     sync.Mutex
	held   map[SyncPoint]bool
	gate   map[SyncPoint]chan struct{}            // closed on Release
	parked map[SyncPoint]map[string]chan struct{} // per-activation parked signals
}

func newBarriers() *barriers {
	return &barriers{
		held:   map[SyncPoint]bool{},
		gate:   map[SyncPoint]chan struct{}{},
		parked: map[SyncPoint]map[string]chan struct{}{},
	}
}

// Hold makes every activation reaching point park until Release.
func (b *barriers) Hold(point SyncPoint) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.held[point] = true
	if b.gate[point] == nil {
		b.gate[point] = make(chan struct{})
	}
	if b.parked[point] == nil {
		b.parked[point] = map[string]chan struct{}{}
	}
}

// Release unblocks everyone parked at point and stops future parking.
func (b *barriers) Release(point SyncPoint) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.held[point] = false
	if g := b.gate[point]; g != nil {
		close(g)
		b.gate[point] = nil
	}
}

// AwaitParked blocks until activation a has parked at point.
func (b *barriers) AwaitParked(point SyncPoint, a string) chan struct{} {
	b.mu.Lock()
	if b.parked[point] == nil {
		b.parked[point] = map[string]chan struct{}{}
	}
	ch := b.parked[point][a]
	if ch == nil {
		ch = make(chan struct{})
		b.parked[point][a] = ch
	}
	b.mu.Unlock()
	return ch
}

// reach parks activation a at point if it is Held, signaling AwaitParked
// waiters and then blocking on the release gate. If the point is not Held it
// returns immediately (the production hot path).
func (b *barriers) reach(point SyncPoint, a string) {
	b.mu.Lock()
	if !b.held[point] {
		b.mu.Unlock()
		return
	}
	gate := b.gate[point]
	if b.parked[point] == nil {
		b.parked[point] = map[string]chan struct{}{}
	}
	signal := b.parked[point][a]
	if signal == nil {
		signal = make(chan struct{})
		b.parked[point][a] = signal
	}
	b.mu.Unlock()

	// Announce we are parked, then wait for release.
	select {
	case <-signal:
		// already closed by a prior reach? create-once means not closed yet.
	default:
		close(signal)
	}
	if gate != nil {
		<-gate
	}
}
