// Package runtime is the terminal UI's Elm-architecture core: a Model with a
// deterministic Update, message and command types, the generic input messages,
// and a pure Drive harness that replays a recorded message stream and records a
// frame after each step (the "deterministic Update under recorded messages"
// contract, U1/U8). It is dependency-free and shaped 1:1 on Bubble Tea's
// Model/Msg/Cmd so the production shell is a thin adapter.
//
// WHY not Bubble Tea directly: Bubble Tea v2 / Lip Gloss v2 (the PRD-approved
// toolkit) transitively require github.com/charmbracelet/x/ansi >= v0.11.7,
// which was API-incompatible with the then-pinned x/ansi v0.4.5 the backend's
// internal/terminal VT engine depended on. The T4 contract-completion rework
// migrated the backend engine to x/ansi v0.11.7 (co-resolution with Bubble Tea
// v2.0.8 / Lip Gloss v2.0.5 verified — see docs/dependencies.md), so the pin
// conflict is RESOLVED: adopting the toolkit is now the mechanical shell swap
// this core was shaped for (T5 owns the UI imports).
package runtime

import "github.com/amux-run/amux/internal/tui/keys"

// Msg is any event delivered to Update (input, backend data, ticks).
type Msg interface{}

// Cmd is a deferred side effect that yields a Msg (I/O lives here, never in
// Update — Update stays pure and deterministic). A nil Cmd is a no-op.
type Cmd func() Msg

// Model is the application state machine.
type Model interface {
	// Init returns an optional startup command.
	Init() Cmd
	// Update folds a message into new state and an optional command. It MUST be
	// deterministic: no clocks, no randomness, no I/O.
	Update(Msg) (Model, Cmd)
	// View renders the current state to a frame string.
	View() string
}

// --- generic input messages (the single input boundary) ---------------------

// KeyMsg is one decoded keypress.
type KeyMsg struct{ Key keys.Key }

// ResizeMsg is a terminal size change (SIGWINCH / initial size).
type ResizeMsg struct{ Cols, Rows int }

// PasteMsg is a bracketed-paste payload delivered as one unit so pasted text is
// never interpreted as command keys.
type PasteMsg struct{ Text string }

// MouseButton enumerates the mouse buttons the UI reacts to.
type MouseButton int

const (
	MouseNone MouseButton = iota
	MouseLeft
	MouseWheelUp
	MouseWheelDown
)

// MouseMsg is a mouse event at a cell coordinate.
type MouseMsg struct {
	Col, Row int
	Button   MouseButton
	Press    bool
}

// QuitMsg requests the program end.
type QuitMsg struct{}

// Quit is a command that asks the program to exit.
func Quit() Msg { return QuitMsg{} }

// Batch combines commands; nils are dropped. The returned command runs each in
// order and returns the first non-nil message (sufficient for this UI's needs).
func Batch(cmds ...Cmd) Cmd {
	var live []Cmd
	for _, c := range cmds {
		if c != nil {
			live = append(live, c)
		}
	}
	if len(live) == 0 {
		return nil
	}
	return func() Msg {
		for _, c := range live {
			if m := c(); m != nil {
				return m
			}
		}
		return nil
	}
}

// Drive replays msgs through the model, returning the final model and the frame
// rendered after each message (plus the initial frame at index 0). Commands
// returned by Update are NOT executed (I/O is out of scope for the deterministic
// harness) — tests inject the resulting messages explicitly. This is the golden
// / regression driver for the whole UI.
func Drive(m Model, msgs []Msg) (Model, []string) {
	frames := []string{m.View()}
	for _, msg := range msgs {
		m, _ = m.Update(msg)
		frames = append(frames, m.View())
	}
	return m, frames
}
