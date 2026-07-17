// Package domain is the frozen, implementation-neutral heart of Amux: the
// workspace object model, its immutable command inputs, the explicit
// state-transition results, and the invariants every transition preserves.
//
// domain owns the object model that ADR-0002 freezes. It has NO knowledge of
// transport, persistence, PTY, terminal rendering, TUI, or provider adapters,
// and the dependency-rule test in internal/archtest enforces that inbound-only
// import direction (ADR-0001). Everything here is deterministic: given the same
// prior State and the same Command drawn from the same IDSource, Apply produces
// byte-for-byte the same result. That determinism is what the property and
// replay suites downstream stand on.
package domain

// ID kinds are deliberately distinct named types rather than a single string
// alias. The compiler then rejects passing a SurfaceID where a PaneID is
// required, which removes an entire class of graph-wiring bugs before any test
// runs. All IDs are opaque, stable, and sortable; ADR-0002 forbids any code
// from parsing structure out of them.
type (
	// SessionID identifies a session — the top-level container a daemon owns.
	SessionID string
	// WorkspaceID identifies a workspace within a session.
	WorkspaceID string
	// PaneID identifies a leaf pane within a workspace split tree.
	PaneID string
	// SurfaceID identifies an ordered surface within a pane.
	SurfaceID string
	// ProjectID is the SHA-256 project-identity digest (ADR-0005 / spec trust
	// boundary). The domain treats it as an opaque tag on a pane; it never
	// computes or validates it — that is the control actor's job.
	ProjectID string
)

// IDSource mints new opaque identifiers. Making it an interface is the single
// seam that lets the property and determinism suites replace UUIDv7 with a
// deterministic counter, so a command sequence replays identically. Production
// wires a UUIDv7 source (see NewUUIDv7Source); tests wire NewCountingSource.
//
// An IDSource must be safe to call from exactly one goroutine: the owning
// session's event-loop goroutine (ADR-0001). It is NOT required to be
// goroutine-safe on its own.
type IDSource interface {
	// NextSession returns a fresh, previously-unused SessionID.
	NextSession() SessionID
	// NextWorkspace returns a fresh, previously-unused WorkspaceID.
	NextWorkspace() WorkspaceID
	// NextPane returns a fresh, previously-unused PaneID.
	NextPane() PaneID
	// NextSurface returns a fresh, previously-unused SurfaceID.
	NextSurface() SurfaceID
}

// CountingSource is a deterministic IDSource used by tests. It emits
// monotonically increasing, prefixed identifiers so a replayed command sequence
// yields identical IDs. It is not exported for production use; production must
// use opaque, unguessable IDs (ADR-0002).
type CountingSource struct {
	session, workspace, pane, surface uint64
}

// NewCountingSource returns a deterministic IDSource seeded at zero.
func NewCountingSource() *CountingSource { return &CountingSource{} }

func (c *CountingSource) NextSession() SessionID {
	c.session++
	return SessionID(formatSeq("ses", c.session))
}

func (c *CountingSource) NextWorkspace() WorkspaceID {
	c.workspace++
	return WorkspaceID(formatSeq("wsp", c.workspace))
}

func (c *CountingSource) NextPane() PaneID {
	c.pane++
	return PaneID(formatSeq("pan", c.pane))
}

func (c *CountingSource) NextSurface() SurfaceID {
	c.surface++
	return SurfaceID(formatSeq("sur", c.surface))
}

func formatSeq(prefix string, n uint64) string {
	// Fixed-width so lexical order matches numeric order for the corpus sizes
	// the suites exercise; keeps golden dumps stable and diffable.
	const digits = "0123456789"
	var buf [20]byte
	i := len(buf)
	if n == 0 {
		i--
		buf[i] = '0'
	}
	for n > 0 {
		i--
		buf[i] = digits[n%10]
		n /= 10
	}
	// zero-pad to 6
	for len(buf)-i < 6 {
		i--
		buf[i] = '0'
	}
	return prefix + "-" + string(buf[i:])
}
