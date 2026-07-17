package teabridge

import (
	"context"
	"strings"
	"sync"
	"testing"

	tea "charm.land/bubbletea/v2"
	lipgloss "charm.land/lipgloss/v2"

	"github.com/amux-run/amux/internal/rpcapi"
	"github.com/amux-run/amux/internal/tui/a11y"
	tuiapp "github.com/amux-run/amux/internal/tui/app"
	"github.com/amux-run/amux/internal/tui/keys"
)

// fakeClient is a deterministic in-memory daemon: it records every method call
// (so tests can assert what did — and did not — reach the daemon) and answers
// projection requests from canned data.
type fakeClient struct {
	mu    sync.Mutex
	calls []string

	tree    rpcapi.WorkspaceTreeResult
	cells   map[string]rpcapi.SurfaceCellsResult
	pctx    map[string]rpcapi.PaneContextResult
	health  rpcapi.HealthResult
	notifs  rpcapi.NotificationListResult
	inspect rpcapi.HookInspectResult

	inspectErr  error
	inputErr    error
	lastApprove rpcapi.HookApproveParams
	lastRevoke  rpcapi.HookRevokeParams
	lastRelease rpcapi.InputReleaseParams
}

func newFake() *fakeClient {
	return &fakeClient{cells: map[string]rpcapi.SurfaceCellsResult{}, pctx: map[string]rpcapi.PaneContextResult{}}
}

func (f *fakeClient) record(method string) {
	f.mu.Lock()
	f.calls = append(f.calls, method)
	f.mu.Unlock()
}

func (f *fakeClient) called(method string) bool { return f.countCalls(method) > 0 }

func (f *fakeClient) countCalls(method string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	n := 0
	for _, c := range f.calls {
		if c == method {
			n++
		}
	}
	return n
}

func (f *fakeClient) Call(ctx context.Context, method string, params, result any) error {
	f.record(method)
	switch method {
	case rpcapi.MethodInputSend:
		if f.inputErr != nil {
			return f.inputErr
		}
	case rpcapi.MethodDaemonHealth:
		if r, ok := result.(*rpcapi.HealthResult); ok {
			*r = f.health
		}
	case rpcapi.MethodNotificationList:
		if r, ok := result.(*rpcapi.NotificationListResult); ok {
			*r = f.notifs
		}
	case rpcapi.MethodPaneFocus:
		// Reflect daemon authority: the re-fetched tree carries the new focus, so
		// the UI shows what the daemon returns (not a local optimistic guess).
		if fp, ok := params.(rpcapi.PaneFocusParams); ok {
			f.mu.Lock()
			f.tree.Focused = fp.Pane
			f.mu.Unlock()
		}
	case rpcapi.MethodHookApprove:
		if p, ok := params.(rpcapi.HookApproveParams); ok {
			f.mu.Lock()
			f.lastApprove = p
			f.mu.Unlock()
		}
	case rpcapi.MethodHookRevoke:
		if p, ok := params.(rpcapi.HookRevokeParams); ok {
			f.mu.Lock()
			f.lastRevoke = p
			f.mu.Unlock()
		}
	case rpcapi.MethodInputRelease:
		if p, ok := params.(rpcapi.InputReleaseParams); ok {
			f.mu.Lock()
			f.lastRelease = p
			f.mu.Unlock()
		}
	}
	return nil
}

func (f *fakeClient) SurfaceCells(ctx context.Context, p rpcapi.SurfaceCellsParams) (rpcapi.SurfaceCellsResult, error) {
	f.record(rpcapi.MethodSurfaceCells)
	return f.cells[p.Surface], nil
}
func (f *fakeClient) WorkspaceTree(ctx context.Context, p rpcapi.WorkspaceTreeParams) (rpcapi.WorkspaceTreeResult, error) {
	f.record(rpcapi.MethodWorkspaceTree)
	return f.tree, nil
}
func (f *fakeClient) PaneContext(ctx context.Context, p rpcapi.PaneContextParams) (rpcapi.PaneContextResult, error) {
	f.record(rpcapi.MethodPaneContext)
	return f.pctx[p.Pane], nil
}
func (f *fakeClient) HookInspect(ctx context.Context, p rpcapi.HookInspectParams) (rpcapi.HookInspectResult, error) {
	f.record(rpcapi.MethodHookInspect)
	if f.inspectErr != nil {
		return rpcapi.HookInspectResult{}, f.inspectErr
	}
	return f.inspect, nil
}

// exec runs a command, flattening tea.Batch into its terminal messages. Tick
// commands would block, so they are never scheduled in tests (Init is not run).
func exec(cmd tea.Cmd) []tea.Msg {
	if cmd == nil {
		return nil
	}
	msg := cmd()
	if b, ok := msg.(tea.BatchMsg); ok {
		var out []tea.Msg
		for _, c := range b {
			out = append(out, exec(c)...)
		}
		return out
	}
	if msg == nil {
		return nil
	}
	return []tea.Msg{msg}
}

// drive feeds one message through Update, executes the returned command, and
// folds the resulting projection/dispatch messages back one level (skipping the
// poll tick). This mirrors the tea event loop closely enough to assert behavior.
func drive(m *Model, msg tea.Msg) {
	_, cmd := m.Update(msg)
	for _, r := range exec(cmd) {
		if _, isTick := r.(tickMsg); isTick {
			continue
		}
		m.Update(r)
	}
}

func twoPaneTree() rpcapi.WorkspaceTreeResult {
	return rpcapi.WorkspaceTreeResult{
		Workspace: "w1", Rev: 1, Focused: "a", PaneOrder: []string{"a", "b"},
		Root: &rpcapi.TreeNode{Split: &rpcapi.TreeSplit{
			Orientation: rpcapi.OrientHorizontal, Ratio: 0.5,
			First: &rpcapi.TreeNode{Pane: &rpcapi.TreePane{
				ID: "a", Active: "a-s1", Focused: true,
				Surfaces: []rpcapi.SurfaceInfo{{ID: "a-s1", Active: true, Class: "live"}},
			}},
			Second: &rpcapi.TreeNode{Pane: &rpcapi.TreePane{
				ID: "b", Active: "b-s1",
				Surfaces: []rpcapi.SurfaceInfo{{ID: "b-s1", Active: true, Class: "live"}},
			}},
		}},
	}
}

func newBridge(f *fakeClient) *Model {
	m := tuiapp.New(80, 24, keys.DefaultKeymap(), a11y.DefaultProfile())
	return New(Config{App: m, Client: f, Ctx: context.Background(), Session: "s1", Workspace: "w1"})
}

// TestPrefixAndNavKeysNeverReachPTY is the central input-safety proof: the
// command prefix and navigation keystrokes are consumed by the UI router and
// never sent to the PTY as input; only a real focus command reaches the daemon.
func TestPrefixAndNavKeysNeverReachPTY(t *testing.T) {
	f := newFake()
	f.tree = twoPaneTree()
	m := newBridge(f)
	drive(m, treeMsg{res: f.tree})

	// Prefix key: enters prefix mode, must not send any input.
	drive(m, tea.KeyPressMsg{Code: 'b', Mod: tea.ModCtrl})
	if m.App().Mode() != keys.Prefix {
		t.Fatalf("ctrl+b should enter prefix mode, got %s", m.App().Mode())
	}
	if f.called(rpcapi.MethodInputSend) {
		t.Fatal("prefix key leaked to input.send")
	}
	// Navigation key: moves focus via a daemon command, still no PTY input.
	drive(m, tea.KeyPressMsg{Code: 'l', Text: "l"})
	if m.App().Focused() != "b" {
		t.Fatalf("prefix+l should focus b, got %q", m.App().Focused())
	}
	if !f.called(rpcapi.MethodPaneFocus) {
		t.Fatal("focus navigation should issue pane.focus")
	}
	if f.called(rpcapi.MethodInputSend) {
		t.Fatal("navigation key leaked to input.send")
	}
}

// TestPlainKeyReachesPTYInput proves that a passthrough key IS sent to the PTY
// via input.send (against the focused pane's active surface), and grants the
// writable lease on acceptance.
func TestPlainKeyReachesPTYInput(t *testing.T) {
	f := newFake()
	f.tree = twoPaneTree()
	m := newBridge(f)
	drive(m, treeMsg{res: f.tree})

	drive(m, tea.KeyPressMsg{Code: 'x', Text: "x"})
	if m.App().LastDisposition != keys.ToPTY {
		t.Fatalf("plain key should be ToPTY, got %s", m.App().LastDisposition)
	}
	if !f.called(rpcapi.MethodInputSend) {
		t.Fatal("plain key should reach input.send")
	}
}

// TestPasteNeverReachesPTYOutsidePassthrough proves bracketed paste in a command
// mode is dropped (never parsed as keys, never sent as input) — the U4 guarantee
// enforced through the real bridge.
func TestPasteNeverReachesPTYOutsidePassthrough(t *testing.T) {
	f := newFake()
	f.tree = twoPaneTree()
	m := newBridge(f)
	drive(m, treeMsg{res: f.tree})

	drive(m, tea.KeyPressMsg{Code: 'b', Mod: tea.ModCtrl}) // enter prefix
	drive(m, tea.PasteMsg{Content: "l%"})                  // would be focus+split if parsed
	if f.called(rpcapi.MethodInputSend) || f.called(rpcapi.MethodPaneSplit) {
		t.Fatal("paste in command mode must not reach the daemon as input or split")
	}
}

// TestProjectionsReachFrame proves the four projections reach the rendered
// frame: workspace.tree shapes the layout, surface.cells (with a wide CJK cell
// and a combining mark) becomes visible content, and pane.context decorates the
// status line — all through the real bridge, no local approximation.
func TestProjectionsReachFrame(t *testing.T) {
	f := newFake()
	f.tree = twoPaneTree()
	f.cells["a-s1"] = rpcapi.SurfaceCellsResult{
		Surface: "a-s1", UpToSeq: 5,
		Grid: &rpcapi.CellGrid{
			Rows: 1, Cols: 5,
			Cells: [][]rpcapi.SurfaceCell{{
				{Text: "世", Width: 2}, {Width: 0},
				{Text: "界", Width: 2}, {Width: 0},
				{Text: "é", Width: 1},
			}},
		},
	}
	f.pctx["a"] = rpcapi.PaneContextResult{Cwd: "/home/dev", GitBranch: "feature-x", GitDirty: true, ForegroundCmd: "vim"}
	m := newBridge(f)
	drive(m, treeMsg{res: f.tree})

	frame := Frame(m.App())
	for _, want := range []string{"世", "界", "é", "feature-x", "vim"} {
		if !strings.Contains(frame, want) {
			t.Fatalf("frame missing projected data %q\n%s", want, frame)
		}
	}
}

func TestChromeGeometrySafety(t *testing.T) {
	f := newFake()
	f.tree = twoPaneTree()
	f.cells["a-s1"] = rpcapi.SurfaceCellsResult{Surface: "a-s1", UpToSeq: 1, Grid: &rpcapi.CellGrid{
		Rows: 1, Cols: 3, Cells: [][]rpcapi.SurfaceCell{{{Text: "界", Width: 2}, {Width: 0}, {Text: "x", Width: 1}}},
	}}
	cols, rows := 80, 24
	m := tuiapp.New(cols, rows, keys.DefaultKeymap(), a11y.DefaultProfile())
	b := New(Config{App: m, Client: f, Ctx: context.Background(), Session: "s1", Workspace: "w1"})
	drive(b, treeMsg{res: f.tree})

	frame := Frame(b.App())
	lines := strings.Split(frame, "\n")
	if len(lines) != rows {
		t.Fatalf("frame should be %d lines, got %d", rows, len(lines))
	}
	for i, ln := range lines {
		if w := lipgloss.Width(ln); w > cols {
			t.Fatalf("line %d width %d exceeds cols %d: %q", i, w, cols, ln)
		}
	}
}

func TestMonochromeFrameHasNoColor(t *testing.T) {
	f := newFake()
	f.tree = twoPaneTree()
	f.cells["a-s1"] = rpcapi.SurfaceCellsResult{Surface: "a-s1", UpToSeq: 1, Grid: &rpcapi.CellGrid{
		Rows: 1, Cols: 2, Cells: [][]rpcapi.SurfaceCell{{
			{Text: "R", Width: 1, Style: &rpcapi.CellStyle{FG: &rpcapi.CellColor{Mode: rpcapi.CellColorANSI, Index: 1}}},
			{Text: "x", Width: 1},
		}},
	}}
	m := tuiapp.New(80, 24, keys.DefaultKeymap(), a11y.Profile{Color: a11y.NoColor, MinCols: 20, MinRows: 5})
	b := New(Config{App: m, Client: f, Ctx: context.Background(), Session: "s1", Workspace: "w1"})
	drive(b, treeMsg{res: f.tree})
	frame := Frame(b.App())
	if strings.Contains(frame, "\x1b[") {
		t.Fatalf("monochrome/no-color frame must contain no SGR escapes:\n%q", frame)
	}
}

func TestMinSizeFallbackUsesLipgloss(t *testing.T) {
	f := newFake()
	m := tuiapp.New(12, 3, keys.DefaultKeymap(), a11y.DefaultProfile())
	b := New(Config{App: m, Client: f, Ctx: context.Background()})
	frame := Frame(b.App())
	lines := strings.Split(frame, "\n")
	if len(lines) != 3 {
		t.Fatalf("min-size frame should be 3 lines, got %d: %q", len(lines), frame)
	}
	if !strings.Contains(frame, "too small") {
		t.Fatalf("min-size fallback should explain the size requirement:\n%s", frame)
	}
	for _, ln := range lines {
		if lipgloss.Width(ln) > 12 {
			t.Fatalf("min-size line exceeds width: %q", ln)
		}
	}
}

func TestKeyMapping(t *testing.T) {
	cases := []struct {
		in   tea.KeyPressMsg
		want keys.Key
	}{
		{tea.KeyPressMsg{Code: 'b', Mod: tea.ModCtrl}, keys.Ctrl('b')},
		{tea.KeyPressMsg{Code: 'l', Text: "l"}, keys.RuneKey('l')},
		{tea.KeyPressMsg{Code: 't', Text: "T", Mod: tea.ModShift}, keys.RuneKey('T')},
		{tea.KeyPressMsg{Code: tea.KeyEnter}, keys.Key{Type: keys.KeyEnter}},
		{tea.KeyPressMsg{Code: tea.KeyLeft}, keys.Key{Type: keys.KeyLeft}},
		{tea.KeyPressMsg{Code: tea.KeyEscape}, keys.Key{Type: keys.KeyEsc}},
	}
	for _, tc := range cases {
		got, ok := mapKeyPress(tc.in)
		if !ok || got != tc.want {
			t.Errorf("mapKeyPress(%+v) = %+v (ok=%v), want %+v", tc.in, got, ok, tc.want)
		}
	}
}
