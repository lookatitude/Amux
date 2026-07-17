package clientadapter

import (
	"fmt"
	"testing"

	v1 "github.com/amux-run/amux/api/v1"
	"github.com/amux-run/amux/internal/client"
	"github.com/amux-run/amux/internal/rpcapi"
	"github.com/amux-run/amux/internal/tui/attachstate"
	"github.com/amux-run/amux/internal/tui/model"
)

func TestClassifyErrMapsFrozenCodes(t *testing.T) {
	cases := []struct {
		err  error
		want attachstate.ErrKind
	}{
		{nil, attachstate.ErrNone},
		{client.ErrBootChanged, attachstate.ErrBootChanged},
		{&client.Error{Code: v1.ErrReplayGap}, attachstate.ErrReplayGap},
		{&client.Error{Code: v1.ErrEventGap}, attachstate.ErrEventGap},
		{&client.Error{Code: v1.ErrResourceExhausted}, attachstate.ErrSlowConsumer},
		{&client.Error{Code: v1.ErrInternal, Retryable: true}, attachstate.ErrConnLost},
		{&client.Error{Code: v1.ErrInternal, Retryable: false}, attachstate.ErrNone},
		{&client.Error{Code: v1.ErrNotFound}, attachstate.ErrNone},
		{fmt.Errorf("plain error"), attachstate.ErrNone},
	}
	for _, tc := range cases {
		if got := ClassifyErr(tc.err); got != tc.want {
			t.Errorf("ClassifyErr(%v) = %v, want %v", tc.err, got, tc.want)
		}
	}
}

// TestCellGridProjectionPreservesWidths proves the surface.cells wire grid maps
// onto the model snapshot with authoritative widths, styles, and colors — and
// that wide heads/spacers and combining marks are copied verbatim (no
// client-side width recomputation).
func TestCellGridProjectionPreservesWidths(t *testing.T) {
	grid := &rpcapi.CellGrid{
		Rows: 1, Cols: 4, Title: "t", Autowrap: true,
		Cursor: rpcapi.CellCursor{Row: 0, Col: 1, Visible: true},
		Cells: [][]rpcapi.SurfaceCell{{
			{Text: "世", Width: 2, Style: &rpcapi.CellStyle{
				Attrs: rpcapi.CellAttrBold,
				FG:    &rpcapi.CellColor{Mode: rpcapi.CellColorANSI, Index: 3},
			}},
			{Width: 0},
			{Text: "x", Width: 1},
			{Text: "é", Width: 1}, // combining acute accent
		}},
	}
	out := CellGridFromWire(grid, 42)
	if out.Rows != 1 || out.Cols != 4 || out.Title != "t" || out.UpToSeq != 42 {
		t.Fatalf("header mismatch: %+v", out)
	}
	if out.Cells[0][0].Content != "世" || out.Cells[0][0].Width != 2 {
		t.Fatalf("wide head not preserved: %+v", out.Cells[0][0])
	}
	if out.Cells[0][1].Width != 0 {
		t.Fatalf("spacer width not preserved: %+v", out.Cells[0][1])
	}
	if out.Cells[0][3].Content != "é" || out.Cells[0][3].Width != 1 {
		t.Fatalf("combining mark not preserved verbatim: %q", out.Cells[0][3].Content)
	}
	if !out.Cells[0][0].Style.Attrs.Has(model.AttrBold) {
		t.Fatalf("style attrs not mapped: %+v", out.Cells[0][0].Style)
	}
	if out.Cells[0][0].Style.FG.Mode != model.ColorANSI || out.Cells[0][0].Style.FG.Index != 3 {
		t.Fatalf("color not mapped: %+v", out.Cells[0][0].Style.FG)
	}
	if !out.Cursor.Visible || out.Cursor.Col != 1 {
		t.Fatalf("cursor not mapped: %+v", out.Cursor)
	}
}

func TestCellSnapshotFromResultDeltaGate(t *testing.T) {
	// Unchanged (delta gate): no grid, caller keeps prior snapshot.
	if _, ok := CellSnapshotFromResult(rpcapi.SurfaceCellsResult{Unchanged: true}); ok {
		t.Fatal("Unchanged result must not deliver a fresh snapshot")
	}
	grid := &rpcapi.CellGrid{Rows: 1, Cols: 1, Cells: [][]rpcapi.SurfaceCell{{{Text: "a", Width: 1}}}}
	snap, ok := CellSnapshotFromResult(rpcapi.SurfaceCellsResult{UpToSeq: 7, Grid: grid})
	if !ok || snap.UpToSeq != 7 || snap.Cells[0][0].Content != "a" {
		t.Fatalf("fresh grid not delivered: ok=%v snap=%+v", ok, snap)
	}
}

func TestCellSnapshotFromAttach(t *testing.T) {
	a := rpcapi.AttachSnapshotCells{
		UpToSeq: 9,
		Grid:    rpcapi.CellGrid{Rows: 1, Cols: 1, Cells: [][]rpcapi.SurfaceCell{{{Text: "z", Width: 1}}}},
	}
	snap := CellSnapshotFromAttach(a)
	if snap.UpToSeq != 9 || snap.Cells[0][0].Content != "z" {
		t.Fatalf("attach cells not mapped: %+v", snap)
	}
}

// TestTreeFromWire proves workspace.tree becomes a geometry tree, focused id,
// and pane view models — no second authoritative layout model, ratios carried.
func TestTreeFromWire(t *testing.T) {
	res := rpcapi.WorkspaceTreeResult{
		Workspace: "w1", Rev: 5, Focused: "b",
		PaneOrder: []string{"a", "b"},
		Root: &rpcapi.TreeNode{Split: &rpcapi.TreeSplit{
			Orientation: rpcapi.OrientHorizontal, Ratio: 0.6,
			First: &rpcapi.TreeNode{Pane: &rpcapi.TreePane{
				ID: "a", Cwd: "/a", Active: "a-s1",
				Surfaces: []rpcapi.SurfaceInfo{{ID: "a-s1", Active: true, Class: "live"}},
			}},
			Second: &rpcapi.TreeNode{Pane: &rpcapi.TreePane{
				ID: "b", Cwd: "/b", Focused: true, Active: "b-s1",
				Surfaces: []rpcapi.SurfaceInfo{{ID: "b-s1", Active: true, Class: "stopped", ExitReason: "exit 1"}},
			}},
		}},
	}
	root, focused, panes := TreeFromWire(res)
	if focused != "b" {
		t.Fatalf("focused = %q, want b", focused)
	}
	if root == nil || root.IsLeaf() || len(root.Children) != 2 {
		t.Fatalf("root split not built: %+v", root)
	}
	if len(root.Ratios) != 2 || root.Ratios[0] != 0.6 {
		t.Fatalf("ratio not carried: %+v", root.Ratios)
	}
	if root.Leaves()[0] != "a" || root.Leaves()[1] != "b" {
		t.Fatalf("leaf order wrong: %+v", root.Leaves())
	}
	if len(panes) != 2 {
		t.Fatalf("want 2 panes, got %d", len(panes))
	}
	var b model.Pane
	for _, p := range panes {
		if p.ID == "b" {
			b = p
		}
	}
	if !b.Focused || b.Cwd != "/b" {
		t.Fatalf("pane b not focused/cwd: %+v", b)
	}
	if s, ok := b.ActiveSurface(); !ok || s.Class != model.ClassStopped || s.ExitReason != "exit 1" {
		t.Fatalf("pane b active surface class: %+v", b.Surfaces)
	}
}

func TestApplyPaneContextFailsClosed(t *testing.T) {
	p := model.Pane{ID: "p1"}
	// Populated context decorates the pane.
	p = ApplyPaneContext(p, rpcapi.PaneContextResult{
		Cwd: "/w", GitBranch: "main", GitDirty: true, ForegroundCmd: "vim", UpdatedMS: 1,
	})
	if p.Cwd != "/w" || p.GitBranch != "main" || !p.GitDirty || p.ForegroundCmd != "vim" {
		t.Fatalf("context not applied: %+v", p)
	}
	// Absent collectors (zero fields) leave decorations zero — never fabricated.
	p2 := ApplyPaneContext(model.Pane{ID: "p2", Cwd: "/keep"}, rpcapi.PaneContextResult{})
	if p2.GitBranch != "" || p2.ForegroundCmd != "" || p2.GitDirty {
		t.Fatalf("absent context must stay zero: %+v", p2)
	}
	if p2.Cwd != "/keep" {
		t.Fatalf("empty cwd must not clobber existing: %+v", p2)
	}
}

// TestGrantsFromInspect proves hook.inspect delivers the full frozen trust card
// so the confirmation is TrustComplete (no UNAVAILABLE fields).
func TestGrantsFromInspect(t *testing.T) {
	res := rpcapi.HookInspectResult{
		Project: rpcapi.HookProjectTrust{Key: "k", Root: "/proj", State: "approved", Epoch: 3},
		Grants: []rpcapi.HookGrantDetail{{
			ID: "g1", HookID: "h1", ExecPath: "/usr/bin/hook", ExecSHA256: "abc123",
			Events: []string{"PreToolUse"}, Scope: "fixed", FixedPath: "/proj/sub",
			EnvKeys: []string{"PATH", "HOME"}, TimeoutMS: 5000, OutputCap: 65536,
			BoundEpoch: 3, Active: true,
		}},
	}
	grants := GrantsFromInspect(res)
	if len(grants) != 1 {
		t.Fatalf("want 1 grant, got %d", len(grants))
	}
	g := grants[0]
	if g.Project != "/proj" || g.Executable != "/usr/bin/hook" || g.Digest != "abc123" {
		t.Fatalf("trust identity not mapped: %+v", g)
	}
	if g.TimeoutMS != 5000 || g.OutputCapB != 65536 {
		t.Fatalf("limits not mapped: %+v", g)
	}
	if g.CwdScope != "fixed:/proj/sub" {
		t.Fatalf("cwd scope: %q", g.CwdScope)
	}
	if len(g.EnvKeys) != 2 || g.EnvKeys[0] != "PATH" {
		t.Fatalf("env keys (names only): %+v", g.EnvKeys)
	}
	if !g.TrustComplete() {
		t.Fatal("hook.inspect grant should be TrustComplete (all frozen fields on the wire)")
	}
}

func TestWireProjections(t *testing.T) {
	h := HealthFromWire(rpcapi.HealthResult{BootID: "b", Version: "1.2", Protocol: "1.1", Sessions: 2})
	if h.BootID != "b" || h.Version != "1.2" || h.Sessions != 2 {
		t.Fatalf("health mapping: %+v", h)
	}
	n := NotificationFromWire(rpcapi.NotificationInfo{ID: "n", Kind: "attention", Title: "hey", Read: true})
	if n.ID != "n" || n.Kind != model.NotifyAttention || !n.Read {
		t.Fatalf("notification mapping: %+v", n)
	}
}
