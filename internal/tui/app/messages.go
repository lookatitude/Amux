package app

import (
	"github.com/amux-run/amux/internal/tui/attachstate"
	"github.com/amux-run/amux/internal/tui/geometry"
	"github.com/amux-run/amux/internal/tui/model"
	"github.com/amux-run/amux/internal/tui/notify"
)

// The messages below are the backend→UI data projections the client adapter
// feeds into Update. Each is an immutable snapshot; the app stores it and
// re-renders. The app never fetches or mutates authoritative state directly.

// PaneTreeMsg delivers the authoritative pane tree layout + focus + pane list
// (from the daemon graph/inspect projection). The app lays this tree out; it
// does not own it.
type PaneTreeMsg struct {
	Root    *geometry.Node
	Focused string
	Panes   []model.Pane
}

// PaneContentMsg delivers a surface's cell snapshot + classification for a pane
// (from the attach snapshot / a cell refresh projection).
type PaneContentMsg struct {
	Pane       string
	Snapshot   model.CellSnapshot
	Class      model.SurfaceClass
	ExitReason string
	Title      string
}

// LeaseMsg delivers an input-lease transition for the focused surface.
type LeaseMsg struct {
	Pane  string
	State model.LeaseState
}

// AttachEventMsg delivers an attach lifecycle transition for the focused
// surface (connecting/snapshot/replay-complete/live/stopped) already classified.
// UpToSeq carries the daemon-declared cutover sequence for a snapshot
// (PhaseReplaying) transition — presentation only, never a client-owned cursor.
type AttachEventMsg struct {
	Phase   model.AttachPhase
	Gap     *attachstate.GapInfo
	UpToSeq uint64
}

// AttachErrMsg delivers a typed attach/stream boundary error.
type AttachErrMsg struct {
	Kind attachstate.ErrKind
}

// NotificationsMsg delivers the current inbox snapshot.
type NotificationsMsg struct {
	Items []model.Notification
}

// HealthMsg delivers the daemon health projection for the status/connection bar.
type HealthMsg struct {
	Health model.Health
}

// ConfirmRequestMsg opens the fail-closed confirmation modal with the given
// prompt lines; confirming emits OnConfirm (with Confirm forced true), denying
// discards it.
type ConfirmRequestMsg struct {
	Prompt    []string
	OnConfirm Intent
}

// TrustPromptMsg opens the hook trust confirmation card for a grant + action.
type TrustPromptMsg struct {
	Grant  model.HookGrant
	Action notify.TrustAction
}

// TrustInspectMsg delivers the hook.inspect trust projection for a project:
// its trust state/epoch and the full frozen grant details. Err carries a
// presentation-safe failure ("inspect unavailable") — absence fails closed and
// the workflow can only display, never assume, trust state.
type TrustInspectMsg struct {
	Project string
	State   string
	Epoch   uint64
	Grants  []model.HookGrant
	Err     string
}
