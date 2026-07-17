// Package teabridge is the PRODUCTION Bubble Tea v2 runtime for `amux tui`. It
// wraps the pure, deterministic app.Model (the Elm-shaped core the golden
// corpus pins) in a real bubbletea/v2 Model/Update/View, isolates ALL I/O in
// tea commands, consumes the four T4 read-only projections (surface.cells,
// workspace.tree, pane.context, hook.inspect) through the client adapter, and
// holds the REAL daemon attach stream for the focused surface (attach.go) —
// the TUI is itself an attached client per the spec's attach/detach contract.
// Every mutating workflow — focus, split, resize, equalize, surface selection,
// input, attach/lease, notification, and hook trust — is a real daemon
// command; UI confirmations only gate whether the command is sent (with the
// frozen confirmation token), never becoming authority themselves.
//
// Determinism is preserved: input and data events are translated into the pure
// core's messages and folded through its side-effect-free Update; the tea
// commands this file builds are the only place a syscall or socket write
// happens. That keeps the whole state machine unit- and golden-testable while
// the production shell drives real terminal I/O.
package teabridge

import (
	"context"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/amux-run/amux/internal/rpcapi"
	tuiapp "github.com/amux-run/amux/internal/tui/app"
	"github.com/amux-run/amux/internal/tui/clientadapter"
	"github.com/amux-run/amux/internal/tui/keys"
	"github.com/amux-run/amux/internal/tui/model"
	"github.com/amux-run/amux/internal/tui/runtime"
)

// Client is the subset of the shared protocol client the bridge needs. The
// bridge issues the SAME methods as the CLI — there is no TUI-only path. It is
// satisfied by *internal/client.Client.
type Client interface {
	Call(ctx context.Context, method string, params, result any) error
	SurfaceCells(ctx context.Context, p rpcapi.SurfaceCellsParams) (rpcapi.SurfaceCellsResult, error)
	WorkspaceTree(ctx context.Context, p rpcapi.WorkspaceTreeParams) (rpcapi.WorkspaceTreeResult, error)
	PaneContext(ctx context.Context, p rpcapi.PaneContextParams) (rpcapi.PaneContextResult, error)
	HookInspect(ctx context.Context, p rpcapi.HookInspectParams) (rpcapi.HookInspectResult, error)
}

// DefaultPollInterval is how often the bridge re-polls the read-only
// projections. Live cells are refreshed via the surface.cells delta gate
// (if_changed_since), so a poll that finds nothing new is cheap — the client
// never parses raw VT and never becomes sequence authority.
const DefaultPollInterval = 150 * time.Millisecond

// Model is the production bubbletea/v2 Model. It owns the pure core plus the
// I/O context and the small amount of projection bookkeeping (per-surface
// delta-gate cursors, attach-session state, last boot id) needed to poll and
// stream efficiently.
type Model struct {
	app *tuiapp.Model
	cli Client
	ctx context.Context

	session, workspace string
	leaseID            string
	poll               time.Duration

	// attachDial opens the dedicated per-session attach connection; att is the
	// single live attach session and attGen invalidates in-flight messages from
	// replaced sessions. attSeqs tracks the last DELIVERED raw sequence per
	// surface (daemon-declared; used only to resume strictly after it).
	attachDial        AttachDialFunc
	att               *attachSession
	attGen            int
	attPending        bool
	attPendingSurface string
	attSeqs           map[string]uint64

	// seqs is the per-surface up_to_seq the delta gate polls against.
	seqs map[string]uint64
	// cellsInflight/cellsDirty coalesce cell re-projections: at most one
	// surface.cells request per surface is outstanding; frames arriving
	// meanwhile mark it dirty and one follow-up fetch is issued on completion.
	cellsInflight map[string]bool
	cellsDirty    map[string]bool
	// panesBySurface maps a surface id to its pane id for cell routing.
	panesBySurface map[string]paneRef
	// lastTree + ctxByPane hold the most recent authoritative tree and the
	// per-pane context so decorations can be re-merged without a second layout
	// model (the daemon tree stays authority; these are its projection cache).
	lastTree  rpcapi.WorkspaceTreeResult
	haveTree  bool
	ctxByPane map[string]rpcapi.PaneContextResult
	// panes is the derived pane view list (tree + context) used for surface and
	// input routing; it is a projection cache, not a second authoritative model.
	panes    []model.Pane
	lastBoot string
}

type paneRef struct {
	pane  string
	class model.SurfaceClass
	exit  string
	title string
}

// Config parameterises the bridge.
type Config struct {
	App       *tuiapp.Model
	Client    Client
	Ctx       context.Context
	Session   string
	Workspace string
	LeaseID   string
	// AttachDial dials the dedicated connection for the attach stream (flow
	// 12). When nil the bridge runs projection-polling only (tests/preview);
	// the production command always supplies it.
	AttachDial   AttachDialFunc
	PollInterval time.Duration
}

// New builds the production bridge model.
func New(cfg Config) *Model {
	poll := cfg.PollInterval
	if poll <= 0 {
		poll = DefaultPollInterval
	}
	lease := cfg.LeaseID
	if lease == "" {
		lease = "amux-tui"
	}
	ctx := cfg.Ctx
	if ctx == nil {
		ctx = context.Background()
	}
	return &Model{
		app:            cfg.App,
		cli:            cfg.Client,
		ctx:            ctx,
		session:        cfg.Session,
		workspace:      cfg.Workspace,
		leaseID:        lease,
		poll:           poll,
		attachDial:     cfg.AttachDial,
		attSeqs:        map[string]uint64{},
		seqs:           map[string]uint64{},
		cellsInflight:  map[string]bool{},
		cellsDirty:     map[string]bool{},
		panesBySurface: map[string]paneRef{},
		ctxByPane:      map[string]rpcapi.PaneContextResult{},
	}
}

// App exposes the pure core (tests and the host loop inspect it read-only).
func (m *Model) App() *tuiapp.Model { return m.app }

// --- tea.Model ---------------------------------------------------------------

// Init kicks off the initial projection fetches and starts the poll tick.
func (m *Model) Init() tea.Cmd {
	return tea.Batch(
		m.fetchHealth(),
		m.fetchNotifications(),
		m.fetchTree(),
		m.tick(),
	)
}

// Update translates every tea message into either a pure-core input message or
// a projection fold, runs the deterministic core Update, and turns the core's
// emitted intents into real daemon-command tea commands. No I/O happens here —
// only in the returned commands.
func (m *Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch t := msg.(type) {

	// ---- terminal input (the single input boundary) ----
	case tea.KeyPressMsg:
		if k, ok := mapKeyPress(t); ok {
			m.fold(runtime.KeyMsg{Key: k})
			return m, m.afterInput()
		}
		return m, nil
	case tea.PasteMsg:
		// Bracketed paste is delivered atomically; the core only forwards it in
		// passthrough so it is never parsed as command keys (U4).
		m.fold(runtime.PasteMsg{Text: t.Content})
		return m, m.afterInput()
	case tea.WindowSizeMsg:
		m.fold(runtime.ResizeMsg{Cols: t.Width, Rows: t.Height})
		return m, nil
	case tea.MouseClickMsg:
		mo := t.Mouse()
		if mo.Button == tea.MouseLeft {
			m.fold(runtime.MouseMsg{Col: mo.X, Row: mo.Y, Button: runtime.MouseLeft, Press: true})
			return m, m.afterInput()
		}
		return m, nil
	case tea.QuitMsg:
		m.closeAttach()
		return m, tea.Quit

	// ---- projection results (data → frame) ----
	case treeMsg:
		return m, m.applyTree(t)
	case cellsMsg:
		return m, m.applyCells(t)
	case contextMsg:
		return m, m.applyContext(t)
	case healthMsg:
		return m, m.applyHealth(t)
	case notificationsMsg:
		m.applyNotifications(t)
		return m, nil
	case dispatchErrMsg:
		if kind := clientadapter.ClassifyErr(t.err); kind != 0 {
			m.fold(tuiapp.AttachErrMsg{Kind: kind})
		}
		return m, nil
	case leaseGrantedMsg:
		// The daemon accepted our input under our lease id → we hold the writable
		// lease. This reflects daemon truth (accepted write ⇒ owned), not a UI
		// assumption of authority.
		m.fold(tuiapp.LeaseMsg{Pane: t.pane, State: model.LeaseOwned})
		return m, nil
	case leaseDeniedMsg:
		// The daemon rejected our write: another client holds the lease. Present
		// read-only state (daemon truth via the typed code, never invented).
		m.fold(tuiapp.LeaseMsg{Pane: t.pane, State: model.LeaseOther})
		return m, nil
	case hookInspectMsg:
		m.applyHookInspect(t)
		return m, nil

	// ---- attach stream lifecycle (flow 12) ----
	case attachOpenedMsg:
		return m, m.applyAttachOpened(t)
	case attachFrameMsg:
		return m, m.applyAttachFrame(t)
	case attachClosedMsg:
		return m, m.applyAttachClosed(t)

	// ---- poll cadence ----
	case tickMsg:
		return m, tea.Batch(m.fetchHealth(), m.fetchNotifications(), m.fetchTree(), m.tick())
	}
	return m, nil
}

// View renders the production frame (Lip Gloss chrome over the pure Screen) in
// the alternate screen buffer.
func (m *Model) View() tea.View {
	v := tea.NewView(Frame(m.app))
	v.AltScreen = true
	// Cell-motion mouse reporting drives click-to-focus (handled by the pure
	// core in passthrough only). The pure core owns its own cursor decoration
	// inside pane content, so the terminal cursor stays hidden (Cursor left nil).
	v.MouseMode = tea.MouseModeCellMotion
	return v
}

// --- core folding & post-input dispatch --------------------------------------

// fold runs one message through the deterministic core Update.
func (m *Model) fold(msg runtime.Msg) {
	var mm runtime.Model = m.app
	mm, _ = mm.Update(msg)
	m.app = mm.(*tuiapp.Model)
}

// afterInput drains the core's outbox into daemon-command commands. When a
// structural mutation was issued it also refreshes the workspace tree so the
// authoritative layout re-reaches the frame (the daemon, not the UI, is
// authority: the UI shows what the re-fetch returns).
func (m *Model) afterInput() tea.Cmd {
	var cmds []tea.Cmd
	refresh := false
	for _, in := range m.app.DrainOutbox() {
		if in.Kind == tuiapp.IntentQuit {
			m.closeAttach()
			return tea.Quit
		}
		if c, structural := m.dispatch(in); c != nil {
			cmds = append(cmds, c)
			if structural {
				refresh = true
			}
		}
	}
	if refresh {
		cmds = append(cmds, m.fetchTree())
	}
	return tea.Batch(cmds...)
}

func (m *Model) tick() tea.Cmd {
	return tea.Tick(m.poll, func(time.Time) tea.Msg { return tickMsg{} })
}

// mapKeyPress maps a Bubble Tea v2 key press onto the toolkit-neutral keys.Key
// the pure router consumes. Control combinations take the base rune from Code
// (Text is empty for non-printable control chars); printable keys take the
// actual character from Text so shifted/layout-specific glyphs (e.g. 'T', '?')
// match the keymap. This is the single place native key events cross into the
// core — everything downstream routes on keys.Key, so the router alone decides
// what reaches the PTY.
func mapKeyPress(kp tea.KeyPressMsg) (keys.Key, bool) {
	k := kp.Key()
	var out keys.Key
	if k.Mod.Contains(tea.ModCtrl) {
		out.Ctrl = true
	}
	if k.Mod.Contains(tea.ModAlt) {
		out.Alt = true
	}
	switch k.Code {
	case tea.KeyEnter:
		out.Type = keys.KeyEnter
		return out, true
	case tea.KeyTab:
		out.Type = keys.KeyTab
		return out, true
	case tea.KeyEscape:
		out.Type = keys.KeyEsc
		return out, true
	case tea.KeyBackspace:
		out.Type = keys.KeyBackspace
		return out, true
	case tea.KeySpace:
		out.Type = keys.KeySpace
		return out, true
	case tea.KeyUp:
		out.Type = keys.KeyUp
		return out, true
	case tea.KeyDown:
		out.Type = keys.KeyDown
		return out, true
	case tea.KeyLeft:
		out.Type = keys.KeyLeft
		return out, true
	case tea.KeyRight:
		out.Type = keys.KeyRight
		return out, true
	case tea.KeyHome:
		out.Type = keys.KeyHome
		return out, true
	case tea.KeyEnd:
		out.Type = keys.KeyEnd
		return out, true
	case tea.KeyPgUp:
		out.Type = keys.KeyPgUp
		return out, true
	case tea.KeyPgDown:
		out.Type = keys.KeyPgDn
		return out, true
	case tea.KeyDelete:
		out.Type = keys.KeyDelete
		return out, true
	}
	// Printable rune. Prefer the actual character (Text) for un-modified and
	// shifted keys; fall back to the base Code for control/alt combinations.
	r := k.Code
	if !out.Ctrl && k.Text != "" {
		rs := []rune(k.Text)
		if len(rs) >= 1 {
			r = rs[0]
		}
	}
	if r == 0 {
		return keys.Key{}, false
	}
	out.Type = keys.KeyRune
	out.Rune = r
	return out, true
}
