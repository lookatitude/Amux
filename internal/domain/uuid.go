package domain

import (
	"github.com/google/uuid"
)

// uuidSource is the production IDSource. It mints UUIDv7 values, which embed a
// Unix-millisecond timestamp in their high bits and are therefore
// lexicographically sortable by creation time — the property ADR-0002 relies on
// for opaque-but-orderable identifiers.
//
// Monotonicity guarantee. google/uuid v1.6 maintains a package-global,
// mutex-guarded monotonic timestamp: successive NewV7 calls never emit a lower
// timestamp than a previously emitted one, even under same-millisecond bursts
// (a 12-bit sub-millisecond counter increments) or a backward wall-clock jump
// (the last emitted time is used as a floor). The A6 UUIDv7 spike
// (spikes/uuidv7) proves both behaviours empirically, so Amux does not
// re-implement a clamp here; it delegates to the audited dependency and the
// spike documents the exact fallback design Amux would own if the dependency
// were ever swapped. Because that monotonic state is process-global, this source
// is safe to call from any goroutine, though ADR-0001 confines minting to the
// owning session actor.
type uuidSource struct {
	fallback IDSource
}

// NewUUIDv7Source returns the production IDSource.
func NewUUIDv7Source() IDSource {
	return &uuidSource{fallback: NewCountingSource()}
}

func (s *uuidSource) next() string {
	u, err := uuid.NewV7()
	if err != nil {
		// google/uuid only errors on entropy-read failure. Rather than panic in
		// a daemon, fall back to a process-unique counter-backed ID so the actor
		// keeps making progress; ADR-0002 permits any opaque unique ID.
		return "amux-fallback-" + string(s.fallback.NextPane())
	}
	return u.String()
}

func (s *uuidSource) NextSession() SessionID     { return SessionID(s.next()) }
func (s *uuidSource) NextWorkspace() WorkspaceID { return WorkspaceID(s.next()) }
func (s *uuidSource) NextPane() PaneID           { return PaneID(s.next()) }
func (s *uuidSource) NextSurface() SurfaceID     { return SurfaceID(s.next()) }
