// dispatch.go is the bridge's I/O layer: the tea commands that fetch the four
// read-only projections and the commands that turn the pure core's intents into
// real daemon mutations. Every function here returns a tea.Cmd (a deferred
// side effect) — nothing here runs during Update, keeping the core deterministic.
package teabridge

import (
	"context"
	"encoding/base64"

	tea "charm.land/bubbletea/v2"

	"github.com/amux-run/amux/internal/rpcapi"
	tuiapp "github.com/amux-run/amux/internal/tui/app"
	"github.com/amux-run/amux/internal/tui/attachstate"
	"github.com/amux-run/amux/internal/tui/clientadapter"
	"github.com/amux-run/amux/internal/tui/geometry"
	"github.com/amux-run/amux/internal/tui/model"
	"github.com/amux-run/amux/internal/tui/notify"
)

// --- bridge message types ----------------------------------------------------

type treeMsg struct {
	res rpcapi.WorkspaceTreeResult
	err error
}
type cellsMsg struct {
	surface string
	res     rpcapi.SurfaceCellsResult
	err     error
}
type contextMsg struct {
	pane string
	res  rpcapi.PaneContextResult
	err  error
}
type healthMsg struct {
	res rpcapi.HealthResult
	err error
}
type notificationsMsg struct {
	items []model.Notification
	err   error
}
type dispatchErrMsg struct{ err error }
type leaseGrantedMsg struct{ pane string }
type leaseDeniedMsg struct{ pane string }
type hookInspectMsg struct {
	project string
	res     rpcapi.HookInspectResult
	err     error
}
type tickMsg struct{}

// --- projection fetch commands -----------------------------------------------

func (m *Model) fetchTree() tea.Cmd {
	cli, ctx, s, w := m.cli, m.ctx, m.session, m.workspace
	if w == "" {
		return nil
	}
	return func() tea.Msg {
		res, err := cli.WorkspaceTree(ctx, rpcapi.WorkspaceTreeParams{Session: s, Workspace: w})
		return treeMsg{res: res, err: err}
	}
}

func (m *Model) fetchCells(surface string, ifChanged uint64) tea.Cmd {
	cli, ctx, s := m.cli, m.ctx, m.session
	if surface == "" {
		return nil
	}
	return func() tea.Msg {
		res, err := cli.SurfaceCells(ctx, rpcapi.SurfaceCellsParams{Session: s, Surface: surface, IfChangedSince: ifChanged})
		return cellsMsg{surface: surface, res: res, err: err}
	}
}

func (m *Model) fetchContext(pane string) tea.Cmd {
	cli, ctx, s, w := m.cli, m.ctx, m.session, m.workspace
	if pane == "" {
		return nil
	}
	return func() tea.Msg {
		res, err := cli.PaneContext(ctx, rpcapi.PaneContextParams{Session: s, Workspace: w, Pane: pane})
		return contextMsg{pane: pane, res: res, err: err}
	}
}

func (m *Model) fetchHealth() tea.Cmd {
	cli, ctx := m.cli, m.ctx
	return func() tea.Msg {
		var res rpcapi.HealthResult
		err := cli.Call(ctx, rpcapi.MethodDaemonHealth, nil, &res)
		return healthMsg{res: res, err: err}
	}
}

func (m *Model) fetchNotifications() tea.Cmd {
	cli, ctx, s := m.cli, m.ctx, m.session
	if s == "" {
		return nil
	}
	return func() tea.Msg {
		var nl rpcapi.NotificationListResult
		if err := cli.Call(ctx, rpcapi.MethodNotificationList, rpcapi.NotificationListParams{Session: s}, &nl); err != nil {
			return notificationsMsg{err: err}
		}
		items := make([]model.Notification, 0, len(nl.Notifications))
		for _, n := range nl.Notifications {
			items = append(items, clientadapter.NotificationFromWire(n))
		}
		return notificationsMsg{items: items}
	}
}

// --- projection apply --------------------------------------------------------

// applyTree folds the authoritative workspace tree into the core and fans out
// per-pane cell + context fetches. Fetch errors classify into the attach
// recovery state (gap/boot-change/slow-consumer) — the UI never stitches gaps.
func (m *Model) applyTree(t treeMsg) tea.Cmd {
	if t.err != nil {
		if kind := clientadapter.ClassifyErr(t.err); kind != attachstate.ErrNone {
			m.fold(tuiapp.AttachErrMsg{Kind: kind})
		}
		return nil
	}
	m.lastTree = t.res
	m.haveTree = true
	m.rebuildPanes()
	// Attach truth comes ONLY from the attach stream lifecycle (ensureAttach
	// below): a tree fetch alone never fabricates a live phase, and a pending
	// recovery (gap / boot-change / slow-consumer) stays visible until the
	// operator recovers — the daemon, not a poll, owns attachment state.

	var cmds []tea.Cmd
	for _, p := range m.panes {
		surf := p.Active
		if surf == "" {
			if s, ok := p.ActiveSurface(); ok {
				surf = s.ID
			}
		}
		if surf != "" {
			cmds = append(cmds, m.requestCells(surf, m.seqs[surf]))
		}
		cmds = append(cmds, m.fetchContext(p.ID))
	}
	cmds = append(cmds, m.ensureAttach())
	return tea.Batch(cmds...)
}

// rebuildPanes re-derives the pane view models from the last tree merged with
// per-pane context and folds a single PaneTreeMsg. The daemon tree is authority;
// this keeps no independent layout state.
func (m *Model) rebuildPanes() {
	root, focused, panes := clientadapter.TreeFromWire(m.lastTree)
	for i := range panes {
		if ctx, ok := m.ctxByPane[panes[i].ID]; ok {
			panes[i] = clientadapter.ApplyPaneContext(panes[i], ctx)
		}
	}
	m.panes = panes
	m.panesBySurface = map[string]paneRef{}
	for _, p := range panes {
		if s, ok := p.ActiveSurface(); ok {
			m.panesBySurface[s.ID] = paneRef{pane: p.ID, class: s.Class, exit: s.ExitReason, title: firstNonEmpty(s.Title, p.ID)}
		}
	}
	m.fold(tuiapp.PaneTreeMsg{Root: root, Focused: focused, Panes: panes})
}

// requestCells is the coalescing gate over fetchCells: at most one
// surface.cells request per surface is outstanding. A request raised while one
// is in flight (e.g. a burst of attach frames) marks the surface dirty and
// applyCells issues exactly one follow-up fetch.
func (m *Model) requestCells(surface string, ifChanged uint64) tea.Cmd {
	if surface == "" {
		return nil
	}
	if m.cellsInflight[surface] {
		m.cellsDirty[surface] = true
		return nil
	}
	m.cellsInflight[surface] = true
	return m.fetchCells(surface, ifChanged)
}

// applyCells folds a fresh surface.cells grid (or nothing, when the delta gate
// reported Unchanged) into the pane content. Cell widths are authoritative from
// the backend — no client-side recomputation.
func (m *Model) applyCells(t cellsMsg) tea.Cmd {
	delete(m.cellsInflight, t.surface)
	if t.err != nil {
		delete(m.cellsDirty, t.surface)
		if kind := clientadapter.ClassifyErr(t.err); kind != attachstate.ErrNone {
			m.fold(tuiapp.AttachErrMsg{Kind: kind})
		}
		return nil
	}
	if snap, ok := clientadapter.CellSnapshotFromResult(t.res); ok {
		m.seqs[t.surface] = snap.UpToSeq
		ref := m.panesBySurface[t.surface]
		m.fold(tuiapp.PaneContentMsg{
			Pane: ref.pane, Snapshot: snap,
			Class: ref.class, ExitReason: ref.exit, Title: ref.title,
		})
	}
	if m.cellsDirty[t.surface] {
		delete(m.cellsDirty, t.surface)
		return m.requestCells(t.surface, m.seqs[t.surface])
	}
	return nil
}

func (m *Model) applyContext(t contextMsg) tea.Cmd {
	if t.err != nil {
		return nil // fail closed: absent context leaves decorations zero
	}
	m.ctxByPane[t.pane] = t.res
	if m.haveTree {
		m.rebuildPanes()
	}
	return nil
}

func (m *Model) applyHealth(t healthMsg) tea.Cmd {
	if t.err != nil {
		if kind := clientadapter.ClassifyErr(t.err); kind != attachstate.ErrNone {
			m.fold(tuiapp.AttachErrMsg{Kind: kind})
		}
		return nil
	}
	var resync tea.Cmd
	if m.lastBoot != "" && t.res.BootID != "" && t.res.BootID != m.lastBoot {
		// Daemon boot id changed: present daemon-restarted recovery and re-sync
		// the authoritative tree (the client never assumes continuity).
		m.fold(tuiapp.AttachErrMsg{Kind: attachstate.ErrBootChanged})
		resync = m.fetchTree()
	}
	if t.res.BootID != "" {
		m.lastBoot = t.res.BootID
	}
	m.fold(tuiapp.HealthMsg{Health: clientadapter.HealthFromWire(t.res)})
	return resync
}

func (m *Model) applyNotifications(t notificationsMsg) {
	if t.err != nil {
		return
	}
	m.fold(tuiapp.NotificationsMsg{Items: t.items})
}

// --- intent dispatch (core → real daemon commands) ---------------------------

// dispatch maps one core intent onto a daemon command. It returns the command
// and whether the mutation is structural (changes the tree/focus/surface and so
// warrants a workspace.tree re-fetch). A nil command means the intent needs no
// daemon call (or lacks the ids to issue one — never fabricated).
func (m *Model) dispatch(in tuiapp.Intent) (tea.Cmd, bool) {
	cli, ctx, s, w, lease := m.cli, m.ctx, m.session, m.workspace, m.leaseID
	switch in.Kind {
	case tuiapp.IntentFocus:
		if w == "" || in.Pane == "" {
			return nil, false
		}
		return callCmd(ctx, cli, rpcapi.MethodPaneFocus, rpcapi.PaneFocusParams{Session: s, Workspace: w, Pane: in.Pane}, nil), true

	case tuiapp.IntentSplit:
		if w == "" || in.Pane == "" {
			return nil, false
		}
		return callCmd(ctx, cli, rpcapi.MethodPaneSplit, rpcapi.PaneSplitParams{
			Session: s, Workspace: w, Target: in.Pane, Orientation: wireOrient(in.Orientation),
		}, &rpcapi.PaneSplitResult{}), true

	case tuiapp.IntentResize:
		if w == "" || in.Pane == "" {
			return nil, false
		}
		return callCmd(ctx, cli, rpcapi.MethodPaneResize, rpcapi.PaneResizeParams{
			Session: s, Workspace: w, Pane: in.Pane, Ratio: resizeRatio(in.Direction),
		}, nil), true

	case tuiapp.IntentEqualize:
		// No dedicated daemon method exists; equalizing the focused pane's binary
		// split is a pane.resize to the even 0.5 ratio. The daemon re-lays-out and
		// the UI shows the re-fetched tree — the UI is not the layout authority.
		pane := m.app.Focused()
		if w == "" || pane == "" {
			return nil, false
		}
		return callCmd(ctx, cli, rpcapi.MethodPaneResize, rpcapi.PaneResizeParams{
			Session: s, Workspace: w, Pane: pane, Ratio: 0.5,
		}, nil), true

	case tuiapp.IntentSelectSurface:
		return m.selectSurfaceCmd(in.Pane, 0), true
	case tuiapp.IntentNextSurface:
		return m.selectSurfaceCmd(in.Pane, +1), true
	case tuiapp.IntentPrevSurface:
		return m.selectSurfaceCmd(in.Pane, -1), true

	case tuiapp.IntentInput:
		surf := m.activeSurface(in.Pane)
		if surf == "" {
			return nil, false
		}
		return m.inputCmd(surf, in.Pane, in.Data, false), false

	case tuiapp.IntentTakeover:
		surf := m.activeSurface(in.Pane)
		if surf == "" {
			return nil, false
		}
		return m.inputCmd(surf, in.Pane, nil, true), false

	case tuiapp.IntentReleaseLease:
		surf := m.activeSurface(in.Pane)
		if surf == "" {
			return nil, false
		}
		return callCmd(ctx, cli, rpcapi.MethodInputRelease, rpcapi.InputReleaseParams{Session: s, Surface: surf, LeaseID: lease}, nil), false

	case tuiapp.IntentRecover:
		// Backend recovery, never local stitching: re-fetch the authoritative
		// tree and a fresh full cell snapshot (IfChangedSince=0), and RE-OPEN the
		// attach stream from the last delivered sequence (an evicted cursor comes
		// back as the daemon's typed replay_gap boundary and stays visible).
		pane := m.app.Focused()
		surf := m.activeSurface(pane)
		cmds := []tea.Cmd{m.fetchTree(), m.fetchCells(surf, 0)}
		if m.attachDial != nil && surf != "" {
			m.closeAttach()
			cmds = append(cmds, m.openAttach(pane, surf, m.resumeFrom(surf)))
		}
		return tea.Batch(cmds...), false

	case tuiapp.IntentDetach:
		// Real detach (spec: "Detach closes the client's stream and releases any
		// input lease it owns; it never stops the process"): close the attach
		// session's dedicated connection (the daemon observes the stream end and
		// detaches this client), explicitly release our input lease, then quit.
		surf := ""
		if m.att != nil {
			surf = m.att.surface
		}
		if surf == "" {
			surf = m.activeSurface(in.Pane)
		}
		m.closeAttach()
		return func() tea.Msg {
			if surf != "" {
				// Best-effort explicit release: the daemon also releases on stream
				// end, and detach must complete even when the call fails.
				_ = cli.Call(ctx, rpcapi.MethodInputRelease,
					rpcapi.InputReleaseParams{Session: s, Surface: surf, LeaseID: lease}, nil)
			}
			return tea.QuitMsg{}
		}, false

	case tuiapp.IntentMarkRead:
		if in.Read == nil {
			return nil, false
		}
		return callCmd(ctx, cli, rpcapi.MethodNotificationRead, rpcapi.NotificationReadParams{ID: in.Read.ID}, &rpcapi.NotificationReadResult{}), false

	case tuiapp.IntentTrust:
		if in.Trust == nil {
			return nil, false
		}
		return m.trustCmd(*in.Trust), false

	case tuiapp.IntentHookInspect:
		return m.hookInspectCmd(m.paneProject(in.Pane)), false
	}
	return nil, false
}

func (m *Model) inputCmd(surface, pane string, data []byte, takeover bool) tea.Cmd {
	cli, ctx, s, lease := m.cli, m.ctx, m.session, m.leaseID
	p := rpcapi.InputSendParams{
		Session: s, Surface: surface, LeaseID: lease,
		DataB64: base64.StdEncoding.EncodeToString(data),
	}
	if takeover {
		p.Takeover = true
		p.Confirm = true // the operator already confirmed via the fail-closed modal
	}
	return func() tea.Msg {
		if err := cli.Call(ctx, rpcapi.MethodInputSend, p, &rpcapi.InputSendResult{}); err != nil {
			if clientadapter.IsLeaseDenied(err) {
				return leaseDeniedMsg{pane: pane}
			}
			return dispatchErrMsg{err: err}
		}
		return leaseGrantedMsg{pane: pane}
	}
}

func (m *Model) selectSurfaceCmd(pane string, delta int) tea.Cmd {
	cli, ctx, s, w := m.cli, m.ctx, m.session, m.workspace
	if w == "" || pane == "" {
		return nil
	}
	surf := m.neighborSurface(pane, delta)
	if surf == "" {
		return nil
	}
	return callCmd(ctx, cli, rpcapi.MethodSurfaceSelect, rpcapi.SurfaceSelectParams{
		Session: s, Workspace: w, Pane: pane, Surface: surf,
	}, nil)
}

// hookInspectCmd fetches the read-only hook.inspect trust projection for a
// project. This is the production path behind the prefix `t` keybinding: the
// core emitted IntentHookInspect, the projection comes back as hookInspectMsg,
// and applyHookInspect folds it into the Trust-mode inspection. The UI never
// decides authorization — it presents hook.inspect and gates the daemon
// approve/deny/revoke commands behind the fail-closed confirmation card.
func (m *Model) hookInspectCmd(project string) tea.Cmd {
	cli, ctx := m.cli, m.ctx
	if project == "" {
		return nil // the core already failed closed with a visible explanation
	}
	return func() tea.Msg {
		res, err := cli.HookInspect(ctx, rpcapi.HookInspectParams{Project: project})
		return hookInspectMsg{project: project, res: res, err: err}
	}
}

// applyHookInspect folds a hook.inspect result (or its failure) into the core's
// trust inspection. Errors present as an explicit unavailable state — the card
// can only ever show daemon-delivered trust detail, never a fabricated grant.
func (m *Model) applyHookInspect(t hookInspectMsg) {
	if t.err != nil {
		m.fold(tuiapp.TrustInspectMsg{Project: t.project, Err: "hook.inspect unavailable: " + t.err.Error()})
		return
	}
	project := t.res.Project.Root
	if project == "" {
		project = t.project
	}
	m.fold(tuiapp.TrustInspectMsg{
		Project: project,
		State:   t.res.Project.State,
		Epoch:   t.res.Project.Epoch,
		Grants:  clientadapter.GrantsFromInspect(t.res),
	})
}

// paneProject returns the daemon-reported project root of a pane ("" when the
// projection carries none — never guessed from local state).
func (m *Model) paneProject(pane string) string {
	for _, p := range m.panes {
		if p.ID == pane {
			return p.Project
		}
	}
	return ""
}

func (m *Model) trustCmd(dec notify.TrustDecision) tea.Cmd {
	cli, ctx := m.cli, m.ctx
	switch dec.Action {
	case notify.TrustApprove:
		return callCmd(ctx, cli, rpcapi.MethodHookApprove, rpcapi.HookApproveParams{Project: dec.Project, Confirm: dec.Confirm}, &rpcapi.EpochResult{})
	case notify.TrustDeny:
		return callCmd(ctx, cli, rpcapi.MethodHookDeny, rpcapi.HookDenyParams{Project: dec.Project}, &rpcapi.EpochResult{})
	case notify.TrustRevoke:
		return callCmd(ctx, cli, rpcapi.MethodHookRevoke, rpcapi.HookRevokeParams{Project: dec.Project, Confirm: dec.Confirm}, &rpcapi.EpochResult{})
	}
	return nil
}

// callCmd issues one daemon call and reports any error for attach-recovery
// classification. Errors are surfaced as a frame state, never a crash.
func callCmd(ctx context.Context, cli Client, method string, params, result any) tea.Cmd {
	return func() tea.Msg {
		if err := cli.Call(ctx, method, params, result); err != nil {
			return dispatchErrMsg{err: err}
		}
		return nil
	}
}

// --- helpers -----------------------------------------------------------------

func (m *Model) activeSurface(pane string) string {
	for _, p := range m.panes {
		if p.ID == pane {
			if p.Active != "" {
				return p.Active
			}
			if s, ok := p.ActiveSurface(); ok {
				return s.ID
			}
		}
	}
	return ""
}

func (m *Model) neighborSurface(pane string, delta int) string {
	for _, p := range m.panes {
		if p.ID != pane {
			continue
		}
		if len(p.Surfaces) == 0 {
			return ""
		}
		if delta == 0 {
			return p.Active
		}
		cur := 0
		for i, s := range p.Surfaces {
			if s.ID == p.Active {
				cur = i
			}
		}
		n := ((cur+delta)%len(p.Surfaces) + len(p.Surfaces)) % len(p.Surfaces)
		return p.Surfaces[n].ID
	}
	return ""
}

func firstNonEmpty(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

func wireOrient(o geometry.Orientation) rpcapi.Orientation {
	if o == geometry.Vertical {
		return rpcapi.OrientVertical
	}
	return rpcapi.OrientHorizontal
}

// resizeRatio maps a grow/shrink direction to a ratio nudge. The daemon owns
// the authoritative ratio; this is a bounded presentation-driven adjustment.
func resizeRatio(d geometry.Direction) float64 {
	switch d {
	case geometry.Left, geometry.Up:
		return 0.45
	default:
		return 0.55
	}
}
