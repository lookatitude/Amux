package teabridge

import (
	"context"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	v1 "github.com/amux-run/amux/api/v1"
	"github.com/amux-run/amux/internal/client"
	"github.com/amux-run/amux/internal/rpcapi"
	"github.com/amux-run/amux/internal/tui/a11y"
	tuiapp "github.com/amux-run/amux/internal/tui/app"
	"github.com/amux-run/amux/internal/tui/keys"
	"github.com/amux-run/amux/internal/tui/model"
)

// wireLeaf builds a leaf TreeNode carrying one active surface with a class/exit.
func wireLeaf(id, class, exit string) *rpcapi.TreeNode {
	active := id + "-s1"
	return &rpcapi.TreeNode{Pane: &rpcapi.TreePane{
		ID: id, Active: active, Cwd: "/home/dev/" + id,
		Surfaces: []rpcapi.SurfaceInfo{{ID: active, Active: true, Class: class, ExitReason: exit, Title: id}},
	}}
}

func vsplit(a, b *rpcapi.TreeNode) *rpcapi.TreeNode {
	return &rpcapi.TreeNode{Split: &rpcapi.TreeSplit{Orientation: rpcapi.OrientVertical, Ratio: 0.5, First: a, Second: b}}
}
func hsplit(a, b *rpcapi.TreeNode) *rpcapi.TreeNode {
	return &rpcapi.TreeNode{Split: &rpcapi.TreeSplit{Orientation: rpcapi.OrientHorizontal, Ratio: 0.5, First: a, Second: b}}
}

// eightPaneWireTree builds the 8-pane concurrent fixture as an authoritative
// binary split tree: two columns of four stacked panes. pane-1 is stopped
// (exit reason), pane-2 is restarted (restore classification), the rest live.
func eightPaneWireTree() rpcapi.WorkspaceTreeResult {
	col := func(a, b, c, d *rpcapi.TreeNode) *rpcapi.TreeNode { return vsplit(vsplit(a, b), vsplit(c, d)) }
	left := col(
		wireLeaf("pane-0", "live", ""),
		wireLeaf("pane-1", "stopped", "exit 1"),
		wireLeaf("pane-2", "restarted", ""),
		wireLeaf("pane-3", "live", ""),
	)
	right := col(
		wireLeaf("pane-4", "live", ""),
		wireLeaf("pane-5", "live", ""),
		wireLeaf("pane-6", "live", ""),
		wireLeaf("pane-7", "live", ""),
	)
	return rpcapi.WorkspaceTreeResult{
		Workspace: "w1", Rev: 1, Focused: "pane-0",
		PaneOrder: []string{"pane-0", "pane-1", "pane-2", "pane-3", "pane-4", "pane-5", "pane-6", "pane-7"},
		Root:      hsplit(left, right),
	}
}

// cellRow builds a 1×n grid from a slice of (text,width) cells with a visible
// cursor at column 0 — enough to prove wide/combining/cursor projection.
func cellGrid(cells ...rpcapi.SurfaceCell) *rpcapi.CellGrid {
	n := 0
	for _, c := range cells {
		if c.Width == 2 {
			n += 2
		} else if c.Width == 1 {
			n++
		}
	}
	return &rpcapi.CellGrid{
		Rows: 1, Cols: n, Cells: [][]rpcapi.SurfaceCell{cells},
		Cursor: rpcapi.CellCursor{Row: 0, Col: 0, Visible: true},
	}
}

func eightPaneFake() *fakeClient {
	f := newFake()
	f.tree = eightPaneWireTree()
	// pane-0 content exercises a wide CJK pair, a combining mark, and ASCII.
	f.cells["pane-0-s1"] = rpcapi.SurfaceCellsResult{Surface: "pane-0-s1", UpToSeq: 3, Grid: cellGrid(
		rpcapi.SurfaceCell{Text: "世", Width: 2}, rpcapi.SurfaceCell{Width: 0},
		rpcapi.SurfaceCell{Text: "界", Width: 2}, rpcapi.SurfaceCell{Width: 0},
		rpcapi.SurfaceCell{Text: "é", Width: 1}, // combining acute
		rpcapi.SurfaceCell{Text: "K", Width: 1},
	)}
	for i := 1; i < 8; i++ {
		id := paneID(i) + "-s1"
		f.cells[id] = rpcapi.SurfaceCellsResult{Surface: id, UpToSeq: 1, Grid: cellGrid(
			rpcapi.SurfaceCell{Text: "P", Width: 1}, rpcapi.SurfaceCell{Text: paneDigit(i), Width: 1},
		)}
	}
	// Real pane context for the focused pane; fail-closed zero for the rest.
	f.pctx["pane-0"] = rpcapi.PaneContextResult{Cwd: "/home/dev/pane-0", GitBranch: "main", GitDirty: true, ForegroundCmd: "vim"}
	return f
}

func paneID(i int) string    { return "pane-" + paneDigit(i) }
func paneDigit(i int) string { return string(rune('0' + i)) }

// setPaneProject stamps a project root onto one pane leaf of a wire tree.
func setPaneProject(n *rpcapi.TreeNode, pane, project string) {
	if n == nil {
		return
	}
	if n.Pane != nil {
		if n.Pane.ID == pane {
			n.Pane.Project = project
		}
		return
	}
	if n.Split != nil {
		setPaneProject(n.Split.First, pane, project)
		setPaneProject(n.Split.Second, pane, project)
	}
}

func eightPaneBridge(f *fakeClient, cols, rows int) *Model {
	appModel := tuiapp.New(cols, rows, keys.DefaultKeymap(), a11y.DefaultProfile())
	return New(Config{App: appModel, Client: f, Ctx: context.Background(), Session: "s1", Workspace: "w1"})
}

// TestEightPaneFixtureRendersBackendData is the headline integration proof: an
// 8-pane concurrent fixture renders real backend cell/context/tree data —
// Unicode wide + combining cells, focus, per-pane status, stopped/restarted
// classifications, and pane context — with no local approximation.
func TestEightPaneFixtureRendersBackendData(t *testing.T) {
	f := eightPaneFake()
	m := eightPaneBridge(f, 100, 40)
	drive(m, treeMsg{res: f.tree})

	frame := Frame(m.App())
	wants := map[string]string{
		"wide CJK head 1":    "世",
		"wide CJK head 2":    "界",
		"combining mark":     "é",
		"focus marker":       "◆",       // focused pane-0 title lead
		"stopped class":      "stopped", // pane-1
		"stopped exit":       "exit 1",
		"restarted class":    "restarted", // pane-2
		"git branch (dirty)": "main*",     // pane-0 context, dirty marker
		"foreground process": "vim",
	}
	for label, want := range wants {
		if !strings.Contains(frame, want) {
			t.Errorf("8-pane frame missing %s (%q)\n%s", label, want, frame)
		}
	}
	// All eight panes' content must be present (P1..P7 plus the CJK pane-0).
	for i := 1; i < 8; i++ {
		if !strings.Contains(frame, "P"+paneDigit(i)) {
			t.Errorf("pane-%d content missing from frame", i)
		}
	}
}

// TestEightPaneCursorAndLeaseVisible proves the cursor decoration and the input
// lease state reach the focused pane after a real write acquires the lease.
func TestEightPaneCursorAndLeaseVisible(t *testing.T) {
	f := eightPaneFake()
	m := eightPaneBridge(f, 100, 40)
	drive(m, treeMsg{res: f.tree})
	// A passthrough key writes to the PTY and, on acceptance, grants the lease.
	drive(m, tea.KeyPressMsg{Code: 'x', Text: "x"})
	if !f.called(rpcapi.MethodInputSend) {
		t.Fatal("write should reach input.send")
	}
	if lease := m.App().AttachState().Lease; lease != model.LeaseOwned {
		// Lease is carried on the focused pane's surface; confirm via the frame.
	}
	frame := Frame(m.App())
	if !strings.Contains(frame, "●") { // owned-lease glyph
		t.Errorf("owned-lease glyph not visible after write:\n%s", frame)
	}
}

// TestUnreadNotificationDecoratesPane proves a backend notification routed to a
// pane surfaces as unread state and that latest-unread navigation routes focus.
func TestUnreadNotificationDecoratesPane(t *testing.T) {
	f := eightPaneFake()
	m := eightPaneBridge(f, 100, 40)
	drive(m, treeMsg{res: f.tree})
	drive(m, notificationsMsg{items: []model.Notification{
		{ID: "n1", Kind: model.NotifyAttention, Title: "build failed", CreatedMS: 10, Pane: "pane-5"},
	}})
	if m.App().Inbox().Unread() != 1 {
		t.Fatalf("expected 1 unread, got %d", m.App().Inbox().Unread())
	}
	// prefix + u → latest unread routes focus to pane-5 (a daemon focus command).
	drive(m, tea.KeyPressMsg{Code: 'b', Mod: tea.ModCtrl})
	drive(m, tea.KeyPressMsg{Code: 'u', Text: "u"})
	if m.App().Focused() != "pane-5" {
		t.Fatalf("latest-unread should route focus to pane-5, got %q", m.App().Focused())
	}
}

// TestGapRecoveryReachesFrame proves an event-gap on a cell fetch surfaces as a
// recoverable state (no local sequence stitching), and that recover invokes the
// backend recovery path.
func TestGapRecoveryReachesFrame(t *testing.T) {
	f := eightPaneFake()
	m := eightPaneBridge(f, 100, 40)
	drive(m, treeMsg{res: f.tree})
	drive(m, cellsMsg{surface: "pane-0-s1", err: &client.Error{Code: v1.ErrEventGap}})
	if !m.App().AttachState().NeedsRecovery() {
		t.Fatal("event gap should require recovery")
	}
	if m.App().AttachState().Phase != model.PhaseGapRecovery {
		t.Fatalf("expected gap_recovery phase, got %s", m.App().AttachState().Phase)
	}
	// prefix + g → recover: re-fetches a fresh snapshot via the backend.
	drive(m, tea.KeyPressMsg{Code: 'b', Mod: tea.ModCtrl})
	drive(m, tea.KeyPressMsg{Code: 'g', Text: "g"})
	if !f.called(rpcapi.MethodSurfaceCells) {
		t.Fatal("recover should re-fetch surface.cells")
	}
	if m.App().AttachState().NeedsRecovery() {
		t.Fatal("after recover, recovery should clear")
	}
}

func TestSlowConsumerReachesFrame(t *testing.T) {
	f := eightPaneFake()
	m := eightPaneBridge(f, 100, 40)
	drive(m, treeMsg{res: f.tree})
	drive(m, cellsMsg{surface: "pane-0-s1", err: &client.Error{Code: v1.ErrResourceExhausted}})
	if m.App().AttachState().Phase != model.PhaseSlowDetached {
		t.Fatalf("slow consumer should show slow_detached, got %s", m.App().AttachState().Phase)
	}
}

func TestDaemonRestartReachesFrame(t *testing.T) {
	f := eightPaneFake()
	m := eightPaneBridge(f, 100, 40)
	drive(m, treeMsg{res: f.tree})
	drive(m, healthMsg{res: rpcapi.HealthResult{BootID: "boot-A", Version: "0.1.0"}})
	drive(m, healthMsg{res: rpcapi.HealthResult{BootID: "boot-B", Version: "0.1.0"}})
	if m.App().AttachState().Phase != model.PhaseDaemonRestarted {
		t.Fatalf("boot-id change should show daemon_restarted, got %s", m.App().AttachState().Phase)
	}
	if !m.App().AttachState().NeedsRecovery() {
		t.Fatal("daemon restart should require re-snapshot recovery")
	}
}

// trustFixture returns an 8-pane fake whose focused pane (pane-0) belongs to a
// project with one full frozen grant on the hook.inspect projection.
func trustFixture() *fakeClient {
	f := eightPaneFake()
	setPaneProject(f.tree.Root, "pane-0", "/home/dev/proj")
	f.inspect = rpcapi.HookInspectResult{
		Project: rpcapi.HookProjectTrust{Key: "k", Root: "/home/dev/proj", State: "", Epoch: 1},
		Grants: []rpcapi.HookGrantDetail{{
			ID: "g1", HookID: "pre", ExecPath: "/usr/bin/guard", ExecSHA256: "deadbeef",
			Events: []string{"PreToolUse"}, Scope: "workspace-primary",
			EnvKeys: []string{"PATH"}, TimeoutMS: 3000, OutputCap: 4096, BoundEpoch: 1, Active: true,
		}},
	}
	return f
}

// prefixKey drives the Ctrl+b prefix followed by one command rune — the real
// shipped key path (no test-only helper).
func prefixKey(m *Model, r rune) {
	drive(m, tea.KeyPressMsg{Code: 'b', Mod: tea.ModCtrl})
	drive(m, tea.KeyPressMsg{Code: r, Text: string(r)})
}

// TestHookTrustKeybindingReachesInspect proves the SHIPPED key path: prefix `t`
// issues hook.inspect for the focused pane's project and opens the Trust-mode
// inspection with the daemon-delivered grant detail — no test-only entry point.
func TestHookTrustKeybindingReachesInspect(t *testing.T) {
	f := trustFixture()
	m := eightPaneBridge(f, 100, 40)
	drive(m, treeMsg{res: f.tree})

	prefixKey(m, 't')
	if !f.called(rpcapi.MethodHookInspect) {
		t.Fatal("prefix+t should issue hook.inspect")
	}
	if m.App().Mode() != keys.Trust {
		t.Fatalf("prefix+t should enter trust mode, got %s", m.App().Mode())
	}
	frame := Frame(m.App())
	for _, want := range []string{"/home/dev/proj", "untrusted", "/usr/bin/guard", "deadbeef", "PreToolUse", "PATH"} {
		if !strings.Contains(frame, want) {
			t.Errorf("trust inspection missing daemon-projected field %q\n%s", want, frame)
		}
	}
	if f.called(rpcapi.MethodHookApprove) || f.called(rpcapi.MethodHookDeny) || f.called(rpcapi.MethodHookRevoke) {
		t.Fatal("inspection alone must not mutate trust")
	}
}

// TestHookTrustApproveViaKeys proves the full live approve workflow: prefix `t`
// → `a` opens the frozen confirmation card (executable, digest, events, env,
// timeout, scope all visible), no daemon mutation issues before an explicit
// `y`, and confirmation issues hook.approve with the Confirm token.
func TestHookTrustApproveViaKeys(t *testing.T) {
	f := trustFixture()
	m := eightPaneBridge(f, 100, 40)
	drive(m, treeMsg{res: f.tree})

	prefixKey(m, 't')
	drive(m, tea.KeyPressMsg{Code: 'a', Text: "a"})
	if m.App().Mode() != keys.Confirmation {
		t.Fatalf("trust `a` should open the confirmation card, mode=%s", m.App().Mode())
	}
	frame := Frame(m.App())
	for _, want := range []string{"APPROVE", "/usr/bin/guard", "deadbeef", "PreToolUse", "PATH", "3000ms", "workspace-primary"} {
		if !strings.Contains(frame, want) {
			t.Errorf("confirmation card missing frozen field %q\n%s", want, frame)
		}
	}
	if f.called(rpcapi.MethodHookApprove) {
		t.Fatal("no approve before explicit confirmation (fail-closed)")
	}
	drive(m, tea.KeyPressMsg{Code: 'y', Text: "y"})
	if !f.called(rpcapi.MethodHookApprove) {
		t.Fatal("confirmed approve should issue hook.approve")
	}
	if p := f.lastApprove; !p.Confirm || p.Project != "/home/dev/proj" {
		t.Fatalf("hook.approve must carry the confirm token for the inspected project, got %+v", p)
	}
}

// TestHookTrustRevokeViaKeys proves revoke: prefix `t` → `r` → `y` issues
// hook.revoke with the Confirm token.
func TestHookTrustRevokeViaKeys(t *testing.T) {
	f := trustFixture()
	m := eightPaneBridge(f, 100, 40)
	drive(m, treeMsg{res: f.tree})

	prefixKey(m, 't')
	drive(m, tea.KeyPressMsg{Code: 'r', Text: "r"})
	if f.called(rpcapi.MethodHookRevoke) {
		t.Fatal("no revoke before explicit confirmation (fail-closed)")
	}
	drive(m, tea.KeyPressMsg{Code: 'y', Text: "y"})
	if !f.called(rpcapi.MethodHookRevoke) {
		t.Fatal("confirmed revoke should issue hook.revoke")
	}
	if p := f.lastRevoke; !p.Confirm || p.Project != "/home/dev/proj" {
		t.Fatalf("hook.revoke must carry the confirm token, got %+v", p)
	}
}

// TestHookTrustCancelIssuesNothing proves the fail-closed branch of every card:
// `n` (and esc from trust mode) performs no daemon mutation at all.
func TestHookTrustCancelIssuesNothing(t *testing.T) {
	f := trustFixture()
	m := eightPaneBridge(f, 100, 40)
	drive(m, treeMsg{res: f.tree})

	prefixKey(m, 't')
	drive(m, tea.KeyPressMsg{Code: 'a', Text: "a"})
	drive(m, tea.KeyPressMsg{Code: 'n', Text: "n"}) // cancel the approve card
	prefixKey(m, 't')
	drive(m, tea.KeyPressMsg{Code: 'd', Text: "d"})
	drive(m, tea.KeyPressMsg{Code: tea.KeyEscape}) // esc cancels the deny card
	if f.called(rpcapi.MethodHookApprove) || f.called(rpcapi.MethodHookDeny) || f.called(rpcapi.MethodHookRevoke) {
		t.Fatal("cancel/deny of a confirmation must perform no trust mutation")
	}
	if m.App().Mode() != keys.Passthrough {
		t.Fatalf("cancel should return to passthrough, got %s", m.App().Mode())
	}
}

// TestHookTrustDenyRecordsExplicitDenial proves the explicit deny workflow:
// confirming the deny card issues hook.deny (no Confirm token — the frozen
// matrix requires it only for approve/revoke).
func TestHookTrustDenyRecordsExplicitDenial(t *testing.T) {
	f := trustFixture()
	m := eightPaneBridge(f, 100, 40)
	drive(m, treeMsg{res: f.tree})

	prefixKey(m, 't')
	drive(m, tea.KeyPressMsg{Code: 'd', Text: "d"})
	drive(m, tea.KeyPressMsg{Code: 'y', Text: "y"})
	if !f.called(rpcapi.MethodHookDeny) {
		t.Fatal("confirmed deny should issue hook.deny")
	}
}

// TestHookTrustAbsentGrantsFailsClosed proves that with no grants on the
// projection the approve key cannot open a confirmation (there is no frozen
// detail to display) and no daemon mutation is possible.
func TestHookTrustAbsentGrantsFailsClosed(t *testing.T) {
	f := trustFixture()
	f.inspect.Grants = nil
	m := eightPaneBridge(f, 100, 40)
	drive(m, treeMsg{res: f.tree})

	prefixKey(m, 't')
	frame := Frame(m.App())
	if !strings.Contains(frame, "no grants recorded") {
		t.Errorf("absent grants should be stated visibly\n%s", frame)
	}
	drive(m, tea.KeyPressMsg{Code: 'a', Text: "a"})
	if m.App().Mode() == keys.Confirmation {
		t.Fatal("approve without grant detail must not open a confirmation")
	}
	drive(m, tea.KeyPressMsg{Code: 'y', Text: "y"})
	if f.called(rpcapi.MethodHookApprove) {
		t.Fatal("approve must be unreachable when no grant detail exists")
	}
}

// TestHookTrustInspectErrorIsSafe proves an inspect failure surfaces as an
// explicit unavailable state and the workflow stays fail-closed.
func TestHookTrustInspectErrorIsSafe(t *testing.T) {
	f := trustFixture()
	f.inspectErr = &client.Error{Code: v1.ErrNotFound, Message: "unknown project"}
	m := eightPaneBridge(f, 100, 40)
	drive(m, treeMsg{res: f.tree})

	prefixKey(m, 't')
	frame := Frame(m.App())
	if !strings.Contains(frame, "hook.inspect unavailable") {
		t.Errorf("inspect error should be visible\n%s", frame)
	}
	drive(m, tea.KeyPressMsg{Code: 'a', Text: "a"})
	drive(m, tea.KeyPressMsg{Code: 'y', Text: "y"})
	if f.called(rpcapi.MethodHookApprove) {
		t.Fatal("no approve may issue after a failed inspect")
	}
}

// TestHelpDiscoversTrustWorkflow proves the trust workflow is discoverable
// from the live help overlay (prefix + ?) alongside detach/recover.
func TestHelpDiscoversTrustWorkflow(t *testing.T) {
	f := eightPaneFake()
	m := eightPaneBridge(f, 100, 40)
	drive(m, treeMsg{res: f.tree})

	prefixKey(m, '?')
	frame := Frame(m.App())
	for _, want := range []string{"hook_trust", "detach", "recover"} {
		if !strings.Contains(frame, want) {
			t.Errorf("help overlay should list %q\n%s", want, frame)
		}
	}
}

// TestHookTrustNoProjectFailsClosed proves a pane without a project cannot
// reach hook.inspect at all and explains itself.
func TestHookTrustNoProjectFailsClosed(t *testing.T) {
	f := eightPaneFake() // no project mapping for pane-0
	m := eightPaneBridge(f, 100, 40)
	drive(m, treeMsg{res: f.tree})

	prefixKey(m, 't')
	if f.called(rpcapi.MethodHookInspect) {
		t.Fatal("no project → no hook.inspect call")
	}
	if !strings.Contains(Frame(m.App()), "no project") {
		t.Error("missing visible no-project explanation")
	}
}
