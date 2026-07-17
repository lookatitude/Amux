package model

// The view models below are immutable projections the TUI renders. Field names
// and JSON-free shapes mirror internal/rpcapi result types so the client
// adapter is a straight copy; anything the wire does not deliver is left zero
// (a snapshot never guesses — mirrors internal/context.PaneContext discipline).

// Health is the daemon liveness/identity projection (daemon.health), shown in
// the status bar and the connection banner.
type Health struct {
	BootID   string
	Version  string
	Protocol string
	Sessions int
	UptimeMS int64
}

// Surface is one terminal surface projection (rpcapi.SurfaceInfo). Class/Exit
// come straight from the daemon; the UI never re-derives them.
type Surface struct {
	ID         string
	Title      string
	Active     bool
	Class      SurfaceClass
	ExitReason string
	// Lease is the client-facing lease ownership for this surface (LeaseNone
	// until an attach/lease notice sets it).
	Lease LeaseState
}

// Stopped reports whether the surface's process is not running.
func (s Surface) Stopped() bool { return s.Class == ClassStopped }

// Pane is one pane projection (rpcapi.PaneInspectResult) plus the pane-level
// context the UI decorates status with. Cwd/Project come from the wire;
// GitBranch/GitDirty/ForegroundCmd are populated only when a future pane
// context projection delivers them (see clientadapter ask-gate) and are
// otherwise zero.
type Pane struct {
	ID        string
	Cwd       string
	Project   string
	Focused   bool
	Surfaces  []Surface
	Active    string // active surface id
	LatestSeq uint64

	// Optional pane-context decorations (zero when the wire omits them).
	GitBranch     string
	GitDirty      bool
	ForegroundCmd string

	// Unread is the count of unread notifications routed to this pane (derived
	// from the notification inbox, presentation-only).
	Unread int
}

// ActiveSurface returns the pane's active surface and whether it was found.
func (p Pane) ActiveSurface() (Surface, bool) {
	for _, s := range p.Surfaces {
		if s.ID == p.Active {
			return s, true
		}
	}
	if len(p.Surfaces) > 0 && p.Active == "" {
		return p.Surfaces[0], true
	}
	return Surface{}, false
}

// Workspace is one workspace projection (rpcapi.WorkspaceInfo).
type Workspace struct {
	ID          string
	Name        string
	PrimaryRoot string
	Rev         uint64
	PaneCount   int
	Focused     string // focused pane id
}

// Session is one session registry projection (rpcapi.SessionInfo).
type Session struct {
	ID   string
	Name string
}

// Notification is one inbox entry projection (rpcapi.NotificationInfo). Read is
// authoritative daemon state; the UI toggles it via notification.read and
// reflects the result, never inventing read/unread authority.
type Notification struct {
	ID        string
	Kind      NotificationKind
	Title     string
	Body      string
	CreatedMS int64
	Read      bool
	// Pane is the routing target when the daemon associates the notification
	// with a pane (presentation routing hint; empty when unrouted).
	Pane string
	// Delivery reflects OS-notifier delivery health for this entry.
	Delivery DeliveryState
}

// HookGrant is a hook trust projection (rpcapi.HookGrantInfo) EXTENDED with the
// full frozen trust fields a confirmation must display (U6): project identity,
// executable + digest, cwd scope, env-key allowlist, timeout, and output cap.
// The wire's hook.list currently delivers only ID/HookID/Events/Scope/Active/
// BoundEpoch; the extended fields are populated only when a hook detail
// projection provides them and are otherwise zero. The UI DISPLAYS these fields
// and never decides authorization — approve/deny/revoke are daemon calls gated
// by the frozen confirmation matrix. See clientadapter ask-gate note.
type HookGrant struct {
	ID         string
	HookID     string
	Project    string
	Events     []string
	Scope      string
	Active     bool
	BoundEpoch uint64

	// Extended trust fields (zero until a detail projection delivers them).
	Executable string
	Digest     string
	CwdScope   string
	EnvKeys    []string
	TimeoutMS  int64
	OutputCapB int64
}

// TrustComplete reports whether every extended trust field needed for an
// informed approval is present. When false the confirmation view must mark the
// missing fields as UNAVAILABLE rather than implying an empty allowlist.
func (g HookGrant) TrustComplete() bool {
	return g.Executable != "" && g.Digest != "" && g.TimeoutMS > 0
}
