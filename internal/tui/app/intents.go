package app

import (
	"github.com/amux-run/amux/internal/tui/geometry"
	"github.com/amux-run/amux/internal/tui/notify"
)

// IntentKind is a backend command the app decided to issue. The app is pure: it
// APPENDS intents to its Outbox rather than performing I/O, and the production
// loop (cmd/amux) drains the Outbox and dispatches each intent through the
// client adapter to the daemon (input.send, pane.focus, pane.split, pane.resize,
// surface.select, notification.read, hook.approve/deny/revoke, attach recovery).
// This keeps Update deterministic and makes "what command did this keystroke
// issue" unit-assertable.
type IntentKind int

const (
	IntentNone          IntentKind = iota
	IntentInput                    // forward bytes to the focused surface's PTY (lease-gated)
	IntentFocus                    // pane.focus
	IntentSplit                    // pane.split
	IntentResize                   // pane.resize
	IntentEqualize                 // equalize (client-side re-request of even ratios)
	IntentSelectSurface            // surface.select
	IntentNextSurface              // surface.select (next)
	IntentPrevSurface              // surface.select (prev)
	IntentReleaseLease             // input.release
	IntentTakeover                 // input.send with takeover+confirm
	IntentDetach                   // detach the attach stream (not stop)
	IntentRecover                  // invoke the recommended attach recovery API
	IntentMarkRead                 // notification.read
	IntentTrust                    // hook.approve/deny/revoke
	IntentHookInspect              // hook.inspect (read-only trust projection fetch)
	IntentQuit                     // exit the program
)

// Intent is one backend command request emitted by the app.
type Intent struct {
	Kind        IntentKind
	Pane        string
	Surface     string
	Direction   geometry.Direction
	Orientation geometry.Orientation
	Data        []byte
	// Trust carries the hook trust decision for IntentTrust.
	Trust *notify.TrustDecision
	// Read carries the notification read intent for IntentMarkRead.
	Read *notify.ReadIntent
}
