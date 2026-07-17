package observability

import "log/slog"

// Frozen correlation attribute keys. Diagnostic consumers (grep, jq, support
// tooling, test assertions) key on these exact strings; changing a value is a
// breaking diagnostic-contract change (pinned by TestCorrelationAttrKeysFrozen).
const (
	// AttrBootID correlates records to one daemon boot (ADR-0004 boot_id).
	AttrBootID = "boot_id"
	// AttrSession correlates records to one workspace session.
	AttrSession = "session"
	// AttrConn correlates records to one accepted control connection.
	AttrConn = "conn"
	// AttrSurface correlates records to one surface (always alongside AttrSession).
	AttrSurface = "surface"
	// AttrActivation correlates records to one hook activation.
	AttrActivation = "activation"
)

// WithBoot returns a child of l whose records carry the boot_id attribute.
// The daemon derives this once at startup; every subsystem logger descends
// from it so a support bundle can be filtered to a single boot. l must be
// non-nil (as must it be for every helper below).
func WithBoot(l *slog.Logger, bootID string) *slog.Logger {
	return l.With(AttrBootID, bootID)
}

// WithSession returns a child of l whose records carry the session attribute.
func WithSession(l *slog.Logger, sessionID string) *slog.Logger {
	return l.With(AttrSession, sessionID)
}

// WithConn returns a child of l whose records carry the conn attribute for one
// accepted control connection.
func WithConn(l *slog.Logger, connID string) *slog.Logger {
	return l.With(AttrConn, connID)
}

// WithSurface returns a child of l whose records carry both the session and
// surface attributes: a surface is only meaningful within its owning session,
// so the pair is always emitted together.
func WithSurface(l *slog.Logger, sessionID, surfaceID string) *slog.Logger {
	return l.With(AttrSession, sessionID, AttrSurface, surfaceID)
}

// WithActivation returns a child of l whose records carry the activation
// attribute for one hook activation.
func WithActivation(l *slog.Logger, activationID string) *slog.Logger {
	return l.With(AttrActivation, activationID)
}
