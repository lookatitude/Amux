package domain

// Events are the immutable, committed record of a state transition. A successful
// Apply returns one or more events describing exactly what changed; subscribers
// observe only committed events (ADR-0004). Sequence numbers, boot IDs, and
// timestamps are allocated by the session actor *after* commit and wrap these
// payloads at the protocol layer — the domain payload carries only the logical
// change plus the resulting workspace revision, so replay is deterministic.
//
// Event is a sealed interface: the unexported marker method means only this
// package can define event types, which lets the protocol codec exhaustively
// switch on the concrete set (ADR-0003).
type Event interface {
	isEvent()
	// Workspace is the workspace the event belongs to ("" for session-level
	// events such as workspace creation/removal which the switch handles).
	eventWorkspace() WorkspaceID
	// Rev is the workspace revision that resulted from the committing command.
	eventRev() uint64
}

type baseEvent struct {
	Workspace WorkspaceID
	Rev       uint64
}

func (b baseEvent) isEvent()                    {}
func (b baseEvent) eventWorkspace() WorkspaceID { return b.Workspace }
func (b baseEvent) eventRev() uint64            { return b.Rev }

// WorkspaceCreated is emitted when a workspace and its first pane/surface exist.
type WorkspaceCreated struct {
	baseEvent
	Name         string
	PrimaryRoot  string
	FirstPane    PaneID
	FirstSurface SurfaceID
}

// WorkspaceRenamed is emitted when a workspace name changes.
type WorkspaceRenamed struct {
	baseEvent
	Name string
}

// WorkspaceClosed is emitted when a workspace and its whole subtree are removed.
type WorkspaceClosed struct {
	baseEvent
}

// PaneSplit is emitted when a target pane is split, creating a new pane with its
// own first surface.
type PaneSplit struct {
	baseEvent
	Target      PaneID
	Orientation SplitOrientation
	Ratio       float64
	NewPane     PaneID
	NewSurface  SurfaceID
}

// PaneClosed is emitted when a pane (and its surfaces) is removed and the tree
// collapses around it.
type PaneClosed struct {
	baseEvent
	Pane PaneID
}

// PaneFocused is emitted when the focused pane changes.
type PaneFocused struct {
	baseEvent
	Pane PaneID
}

// PaneResized is emitted when a pane's parent split ratio changes.
type PaneResized struct {
	baseEvent
	Pane  PaneID
	Ratio float64
}

// WorkspaceEqualized is emitted when every split ratio is reset to balance.
type WorkspaceEqualized struct {
	baseEvent
}

// SurfaceSpawned is emitted when a new ordered surface is appended to a pane and
// becomes active.
type SurfaceSpawned struct {
	baseEvent
	Pane    PaneID
	Surface SurfaceID
	Title   string
}

// ActiveSurfaceChanged is emitted when a pane's active surface changes.
type ActiveSurfaceChanged struct {
	baseEvent
	Pane    PaneID
	Surface SurfaceID
}

// SurfaceClosed is emitted when a surface is removed from a pane.
type SurfaceClosed struct {
	baseEvent
	Pane      PaneID
	Surface   SurfaceID
	NewActive SurfaceID // active surface after removal (unchanged if the closed one was inactive)
}
