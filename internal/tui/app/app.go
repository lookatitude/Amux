// Package app is the terminal UI's integrated model (U1/U8): it composes the
// pure geometry, renderer, keymap/router, attach state machine, notification
// inbox, and accessibility profile into one runtime.Model with a deterministic
// Update. It owns NO durable state — the pane tree, cell snapshots, lease state,
// notifications, and trust grants are all daemon projections delivered as
// messages; every mutating decision leaves as an Intent in the Outbox for the
// production loop to dispatch through the client adapter. Update never performs
// I/O, so the whole UI is golden-testable by replaying recorded message streams.
package app

import (
	"github.com/amux-run/amux/internal/tui/a11y"
	"github.com/amux-run/amux/internal/tui/attachstate"
	"github.com/amux-run/amux/internal/tui/geometry"
	"github.com/amux-run/amux/internal/tui/keys"
	"github.com/amux-run/amux/internal/tui/model"
	"github.com/amux-run/amux/internal/tui/notify"
	"github.com/amux-run/amux/internal/tui/runtime"
)

// Model is the integrated application state.
type Model struct {
	cols, rows int

	tree    *geometry.Node
	focused string
	panes   map[string]model.Pane
	content map[string]PaneContentMsg

	mode   keys.Mode
	keymap keys.Keymap
	router keys.Router

	attach  *attachstate.Machine
	inbox   *notify.Inbox
	profile a11y.Profile
	health  model.Health

	confirm *confirmState
	trust   *trustState
	inspect *trustInspectState

	// Observable, pure outputs.
	Outbox          []Intent
	LastDisposition keys.Disposition
	Quitting        bool
}

type confirmState struct {
	prompt    []string
	onConfirm Intent
}

type trustState struct {
	grant  model.HookGrant
	action notify.TrustAction
	lines  []string
}

// trustInspectState is the open hook-trust inspection (Trust mode): the
// hook.inspect projection for the focused pane's project. Everything here is
// display of daemon state; the only decisions it enables are the confirmation
// requests a/d/r raise, which themselves fail closed without a grant to show.
type trustInspectState struct {
	project string
	state   string
	epoch   uint64
	grants  []model.HookGrant
	cursor  int
	loaded  bool
	err     string
}

// New builds a model sized cols×rows with the given keymap and a11y profile.
func New(cols, rows int, km keys.Keymap, profile a11y.Profile) *Model {
	return &Model{
		cols:    cols,
		rows:    rows,
		panes:   map[string]model.Pane{},
		content: map[string]PaneContentMsg{},
		mode:    keys.Passthrough,
		keymap:  km,
		router:  keys.NewRouter(km),
		attach:  attachstate.New(),
		inbox:   notify.NewInbox(nil),
		profile: profile,
	}
}

// Mode/Focused/Attach/Inbox accessors for tests and the production loop.
func (m *Model) Mode() keys.Mode                { return m.mode }
func (m *Model) Focused() string                { return m.focused }
func (m *Model) AttachState() attachstate.State { return m.attach.State() }
func (m *Model) Inbox() *notify.Inbox           { return m.inbox }
func (m *Model) DrainOutbox() []Intent          { out := m.Outbox; m.Outbox = nil; return out }

// Init satisfies runtime.Model; no startup command (the loop subscribes).
func (m *Model) Init() runtime.Cmd { return nil }

func (m *Model) emit(in Intent) { m.Outbox = append(m.Outbox, in) }

// Update folds one message into new state. It returns the same *Model (mutated
// in place) as a runtime.Model; commands are not used (I/O is Outbox-driven).
func (m *Model) Update(msg runtime.Msg) (runtime.Model, runtime.Cmd) {
	switch t := msg.(type) {
	case runtime.ResizeMsg:
		if t.Cols >= 0 {
			m.cols = t.Cols
		}
		if t.Rows >= 0 {
			m.rows = t.Rows
		}
	case runtime.KeyMsg:
		m.handleKey(t.Key)
	case runtime.PasteMsg:
		// Paste is delivered atomically; only forwarded in passthrough so it can
		// never be parsed as command keys (U4).
		if m.mode == keys.Passthrough && t.Text != "" {
			m.emit(Intent{Kind: IntentInput, Pane: m.focused, Data: []byte(t.Text)})
		}
	case runtime.MouseMsg:
		m.handleMouse(t)
	case runtime.QuitMsg:
		m.Quitting = true
		m.emit(Intent{Kind: IntentQuit})
	case PaneTreeMsg:
		m.tree = t.Root
		if t.Focused != "" {
			m.focused = t.Focused
		}
		m.panes = map[string]model.Pane{}
		for _, p := range t.Panes {
			m.panes[p.ID] = p
		}
		m.recomputeUnread()
	case PaneContentMsg:
		m.content[t.Pane] = t
	case LeaseMsg:
		if t.Pane == "" || t.Pane == m.focused {
			m.attach.Lease(t.State)
		}
		if p, ok := m.panes[t.Pane]; ok {
			p.Surfaces = withLease(p.Surfaces, p.Active, t.State)
			m.panes[t.Pane] = p
		}
	case AttachEventMsg:
		m.applyAttachPhase(t)
	case AttachErrMsg:
		m.attach.Error(t.Kind)
	case NotificationsMsg:
		m.inbox = notify.NewInbox(t.Items)
		m.recomputeUnread()
	case HealthMsg:
		m.health = t.Health
	case ConfirmRequestMsg:
		m.confirm = &confirmState{prompt: t.Prompt, onConfirm: t.OnConfirm}
		m.mode = keys.Confirmation
	case TrustPromptMsg:
		m.trust = &trustState{grant: t.Grant, action: t.Action, lines: notify.TrustCard(t.Grant, t.Action)}
		m.mode = keys.Confirmation
	case TrustInspectMsg:
		// Fold the hook.inspect projection into the open inspection only — a late
		// result after the operator closed Trust mode is dropped, never trusted.
		if m.inspect != nil {
			m.inspect.loaded = true
			if t.Err != "" {
				m.inspect.err = t.Err
			} else {
				m.inspect.project = t.Project
				m.inspect.state = t.State
				m.inspect.epoch = t.Epoch
				m.inspect.grants = t.Grants
				m.inspect.cursor = 0
				m.inspect.err = ""
			}
		}
	}
	return m, nil
}

func (m *Model) applyAttachPhase(t AttachEventMsg) {
	switch t.Phase {
	case model.PhaseConnecting:
		m.attach.Connecting()
	case model.PhaseReplaying:
		m.attach.Snapshot(t.UpToSeq, t.Gap, false)
	case model.PhaseLive:
		m.attach.Live()
	case model.PhaseReadOnly:
		m.attach.Lease(model.LeaseReadOnly)
		m.attach.ReplayComplete()
	case model.PhaseStopped:
		m.attach.SurfaceStopped()
	}
}

func (m *Model) handleKey(k keys.Key) {
	res := m.router.Resolve(m.mode, k)
	m.LastDisposition = res.Disposition
	switch res.Disposition {
	case keys.ToPTY:
		m.emit(Intent{Kind: IntentInput, Pane: m.focused, Data: EncodeKey(k)})
	case keys.ToUI:
		m.applyAction(res.Action)
		m.mode = res.NextMode
		// An action that opened a modal (e.g. takeover/trust) overrides the
		// router's next mode: the UI stays in the fail-closed Confirmation mode
		// until the operator confirms or denies.
		if m.confirm != nil || m.trust != nil {
			m.mode = keys.Confirmation
		}
	case keys.Ignored:
		m.mode = res.NextMode
	}
}

func (m *Model) handleMouse(t runtime.MouseMsg) {
	if t.Button == runtime.MouseLeft && t.Press && m.mode == keys.Passthrough {
		if id, ok := m.paneAt(t.Col, t.Row); ok && id != m.focused {
			m.focused = id
			m.emit(Intent{Kind: IntentFocus, Pane: id})
		}
	}
}

func (m *Model) applyAction(a keys.Action) {
	switch a {
	case keys.ActFocusLeft:
		m.moveFocus(geometry.Left)
	case keys.ActFocusRight:
		m.moveFocus(geometry.Right)
	case keys.ActFocusUp:
		m.moveFocus(geometry.Up)
	case keys.ActFocusDown:
		m.moveFocus(geometry.Down)
	case keys.ActSplitHoriz:
		m.emit(Intent{Kind: IntentSplit, Pane: m.focused, Orientation: geometry.Horizontal})
	case keys.ActSplitVert:
		m.emit(Intent{Kind: IntentSplit, Pane: m.focused, Orientation: geometry.Vertical})
	case keys.ActEqualize:
		m.emit(Intent{Kind: IntentEqualize})
	case keys.ActGrow:
		m.emit(Intent{Kind: IntentResize, Pane: m.focused, Direction: geometry.Right})
	case keys.ActShrink:
		m.emit(Intent{Kind: IntentResize, Pane: m.focused, Direction: geometry.Left})
	case keys.ActNextSurface:
		m.emit(Intent{Kind: IntentNextSurface, Pane: m.focused})
	case keys.ActPrevSurface:
		m.emit(Intent{Kind: IntentPrevSurface, Pane: m.focused})
	case keys.ActEnterSurface:
		m.emit(Intent{Kind: IntentSelectSurface, Pane: m.focused})
	case keys.ActOpenNotifs:
		m.inbox.FocusLatestUnread()
	case keys.ActNextUnread:
		if m.inbox.FocusLatestUnread() {
			if pane, ok := m.inbox.RouteTarget(); ok {
				m.focused = pane
				m.emit(Intent{Kind: IntentFocus, Pane: pane})
			}
		}
	case keys.ActMarkRead:
		if cur, ok := m.inbox.Cursor(); ok {
			if intent, ok := m.inbox.MarkRead(cur.ID); ok {
				ri := intent
				m.emit(Intent{Kind: IntentMarkRead, Read: &ri})
			}
		}
	case keys.ActDismiss:
		if cur, ok := m.inbox.Cursor(); ok {
			m.inbox.Dismiss(cur.ID)
		}
	case keys.ActReleaseLease:
		m.emit(Intent{Kind: IntentReleaseLease, Pane: m.focused})
	case keys.ActRequestTakeove:
		// Fail-closed: takeover requires an explicit confirmation modal.
		m.confirm = &confirmState{
			prompt:    []string{"Take over the input lease?", "The current holder loses input. [y] confirm  [n] cancel."},
			onConfirm: Intent{Kind: IntentTakeover, Pane: m.focused},
		}
	case keys.ActDetach:
		m.emit(Intent{Kind: IntentDetach, Pane: m.focused})
	case keys.ActHookTrust:
		// Open the hook trust inspection for the focused pane's project. Without a
		// project there is nothing to inspect — the workflow fails closed to a
		// visible explanation (never a fabricated card).
		m.inspect = &trustInspectState{}
		if p, ok := m.panes[m.focused]; ok && p.Project != "" {
			m.inspect.project = p.Project
			m.emit(Intent{Kind: IntentHookInspect, Pane: m.focused})
		} else {
			m.inspect.loaded = true
			m.inspect.err = "focused pane has no project — use `amux hook list`"
		}
	case keys.ActTrustApprove:
		m.openTrustCard(notify.TrustApprove)
	case keys.ActTrustDeny:
		m.openTrustCard(notify.TrustDeny)
	case keys.ActTrustRevoke:
		m.openTrustCard(notify.TrustRevoke)
	case keys.ActNextGrant:
		m.moveGrantCursor(+1)
	case keys.ActPrevGrant:
		m.moveGrantCursor(-1)
	case keys.ActRecover:
		if m.attach.State().NeedsRecovery() {
			m.emit(Intent{Kind: IntentRecover, Pane: m.focused})
			m.attach.Recovered()
		}
	case keys.ActConfirm:
		m.resolveConfirmation(true)
	case keys.ActCancel:
		m.resolveConfirmation(false)
	}
}

// resolveConfirmation applies or denies whichever modal is open (fail-closed:
// denial discards the intent; confirmation forces Confirm=true on the emitted
// command).
func (m *Model) resolveConfirmation(confirmed bool) {
	if m.trust != nil {
		if confirmed {
			dec := notify.TrustDecision{Project: m.trust.grant.Project, Action: m.trust.action, Confirm: m.trust.action.NeedsConfirm()}
			m.emit(Intent{Kind: IntentTrust, Trust: &dec})
		}
		m.trust = nil
	}
	if m.confirm != nil {
		if confirmed {
			// The intent kind (e.g. IntentTakeover) tells the adapter to attach the
			// daemon confirmation token; denial simply discards it (fail-closed).
			m.emit(m.confirm.onConfirm)
		}
		m.confirm = nil
	}
	m.inspect = nil
	m.mode = keys.Passthrough
}

// openTrustCard raises the confirmation card for the selected grant of the
// open inspection. Without a loaded grant there is no frozen detail to show,
// so the request fails closed: no card, no daemon call — only a visible note.
func (m *Model) openTrustCard(a notify.TrustAction) {
	if m.inspect == nil {
		return
	}
	if len(m.inspect.grants) == 0 {
		m.inspect.err = "no grant detail to display — nothing to confirm (see `amux hook`)"
		return
	}
	if m.inspect.cursor >= len(m.inspect.grants) {
		m.inspect.cursor = 0
	}
	g := m.inspect.grants[m.inspect.cursor]
	m.trust = &trustState{grant: g, action: a, lines: notify.TrustCard(g, a)}
}

func (m *Model) moveGrantCursor(delta int) {
	if m.inspect == nil || len(m.inspect.grants) == 0 {
		return
	}
	n := len(m.inspect.grants)
	m.inspect.cursor = ((m.inspect.cursor+delta)%n + n) % n
}

func (m *Model) moveFocus(d geometry.Direction) {
	if m.tree == nil || m.focused == "" {
		return
	}
	l := m.layout()
	if id, ok := l.Neighbour(m.focused, d); ok {
		m.focused = id
		m.emit(Intent{Kind: IntentFocus, Pane: id})
	}
}

// layout computes the current pane layout for the terminal size.
func (m *Model) layout() geometry.Layout {
	return geometry.Compute(m.tree, m.cols, m.rows, m.profile.LayoutConfig())
}

// paneAt returns the pane whose outer rect contains (col,row).
func (m *Model) paneAt(col, row int) (string, bool) {
	l := m.layout()
	for _, p := range l.Panes {
		r := p.Outer
		if col >= r.X && col < r.Right() && row >= r.Y && row < r.Bottom() {
			return p.PaneID, true
		}
	}
	return "", false
}

func (m *Model) recomputeUnread() {
	if m.inbox == nil {
		return
	}
	for id, p := range m.panes {
		p.Unread = m.inbox.UnreadForPane(id)
		m.panes[id] = p
	}
}

// withLease sets the lease state on the active surface of a pane's surface list.
func withLease(surfaces []model.Surface, active string, l model.LeaseState) []model.Surface {
	out := append([]model.Surface(nil), surfaces...)
	for i := range out {
		if out[i].ID == active || (active == "" && i == 0) {
			out[i].Lease = l
		}
	}
	return out
}
