// projections.go implements the minor-1 read-only projections (T4 handoff to
// T5): surface.cells, workspace.tree, and pane.context. Each is a pure
// translation of state the daemon already owns — the derived VT grid, the
// domain split tree, and the B10 context collectors — onto the immutable
// rpcapi shapes. Nothing here mutates durable state; every payload is bounded
// (grids by surface geometry, trees by the pane count, context by the
// collectors' own byte/time budgets).
package daemon

import (
	"context"

	v1 "github.com/amux-run/amux/api/v1"

	"github.com/amux-run/amux/internal/domain"
	"github.com/amux-run/amux/internal/rpcapi"
	"github.com/amux-run/amux/internal/terminal"
)

// --- surface.cells -----------------------------------------------------------

// SurfaceCells projects one surface's derived cell grid. UpToSeq is a floor:
// the ring's latest committed sequence is read BEFORE the engine snapshot, so
// the grid always reflects at least that much of the raw stream (the safe
// direction — a client may re-render slightly newer derived state, but never
// believes it has raw bytes it does not). The exact snapshot-at-N payload
// remains the attach stream's cells option (attachStream).
func (e *Engine) SurfaceCells(ctx context.Context, p rpcapi.SurfaceCellsParams) (rpcapi.SurfaceCellsResult, error) {
	_, sr, err := e.surface(p.Session, p.Surface)
	if err != nil {
		return rpcapi.SurfaceCellsResult{}, err
	}
	if sr.engine == nil {
		return rpcapi.SurfaceCellsResult{}, &engineError{code: v1.ErrConflict, msg: "surface has no cell engine"}
	}
	var latest uint64
	if sr.ring != nil {
		latest = sr.ring.LatestSeq()
	}
	out := rpcapi.SurfaceCellsResult{Surface: string(sr.id), UpToSeq: latest}
	if p.IfChangedSince != 0 && latest <= p.IfChangedSince {
		out.Unchanged = true
		return out, nil
	}
	grid := cellGridFrom(sr.engine.CellSnapshot())
	out.Grid = &grid
	return out, nil
}

// cellGridFrom converts the engine's immutable snapshot to the wire shape.
func cellGridFrom(s terminal.CellSnapshot) rpcapi.CellGrid {
	g := rpcapi.CellGrid{
		Rows: s.Rows,
		Cols: s.Cols,
		Cursor: rpcapi.CellCursor{
			Row: s.Cursor.Row, Col: s.Cursor.Col,
			Visible: s.Cursor.Visible, WrapNext: s.Cursor.WrapNext,
		},
		Pen:              cellStyleFrom(s.Pen),
		Title:            s.Title,
		AltScreen:        s.AltScreen,
		Autowrap:         s.Autowrap,
		ScrollTop:        s.ScrollTop,
		ScrollBottom:     s.ScrollBottom,
		BellCount:        s.BellCount,
		UnsupportedCount: s.UnsupportedCount,
	}
	g.Cells = make([][]rpcapi.SurfaceCell, len(s.Cells))
	for r, row := range s.Cells {
		wire := make([]rpcapi.SurfaceCell, len(row))
		for c, cell := range row {
			wire[c] = rpcapi.SurfaceCell{
				Text:  cell.Content,
				Width: cell.Width,
				Style: cellStyleFrom(cell.Style),
			}
		}
		g.Cells[r] = wire
	}
	return g
}

// cellStyleFrom returns nil for the default style so blank cells stay tiny on
// the wire (boundedness in practice, not just in principle).
func cellStyleFrom(s terminal.Style) *rpcapi.CellStyle {
	if s == (terminal.Style{}) {
		return nil
	}
	return &rpcapi.CellStyle{
		FG:    cellColorFrom(s.FG),
		BG:    cellColorFrom(s.BG),
		Attrs: uint16(s.Attrs),
	}
}

func cellColorFrom(c terminal.Color) *rpcapi.CellColor {
	switch c.Mode {
	case terminal.ColorANSI:
		return &rpcapi.CellColor{Mode: rpcapi.CellColorANSI, Index: c.Index}
	case terminal.Color256:
		return &rpcapi.CellColor{Mode: rpcapi.CellColor256, Index: c.Index}
	case terminal.ColorRGB:
		return &rpcapi.CellColor{Mode: rpcapi.CellColorRGB, R: c.R, G: c.G, B: c.B}
	default:
		return nil
	}
}

// --- workspace.tree ----------------------------------------------------------

// WorkspaceTree projects the authoritative split tree of one workspace:
// stable pane/surface IDs, orientation, nesting, ratios, focus, active
// surfaces, and the deterministic orders a renderer needs. It is a read-only
// deep copy — the domain tree remains the single layout authority.
func (e *Engine) WorkspaceTree(ctx context.Context, p rpcapi.WorkspaceTreeParams) (rpcapi.WorkspaceTreeResult, error) {
	st, err := e.state(ctx, domain.SessionID(p.Session))
	if err != nil {
		return rpcapi.WorkspaceTreeResult{}, err
	}
	w, ok := st.Workspace(domain.WorkspaceID(p.Workspace))
	if !ok {
		return rpcapi.WorkspaceTreeResult{}, &engineError{code: v1.ErrNotFound, msg: "workspace not found"}
	}
	out := rpcapi.WorkspaceTreeResult{
		Workspace: string(w.ID),
		Name:      w.Name,
		Rev:       w.Rev(),
		Focused:   string(w.Focused()),
		PaneOrder: []string{},
		Root:      treeNodeFrom(w, w.Tree()),
	}
	for _, id := range w.PaneOrder() {
		out.PaneOrder = append(out.PaneOrder, string(id))
	}
	for _, id := range w.FocusHistory() {
		out.FocusHistory = append(out.FocusHistory, string(id))
	}
	return out, nil
}

func treeNodeFrom(w *domain.Workspace, v *domain.TreeView) *rpcapi.TreeNode {
	if v == nil {
		return nil
	}
	if v.IsLeaf() {
		pane, ok := w.Pane(v.Pane)
		if !ok {
			// Cannot happen on a checked graph; keep the projection total.
			return &rpcapi.TreeNode{Pane: &rpcapi.TreePane{ID: string(v.Pane), Surfaces: []rpcapi.SurfaceInfo{}}}
		}
		tp := &rpcapi.TreePane{
			ID:       string(pane.ID),
			Cwd:      pane.Cwd,
			Project:  string(pane.Project),
			Focused:  w.Focused() == pane.ID,
			Active:   string(pane.ActiveSurface()),
			Surfaces: []rpcapi.SurfaceInfo{},
		}
		for _, sf := range pane.Surfaces() {
			tp.Surfaces = append(tp.Surfaces, rpcapi.SurfaceInfo{
				ID: string(sf.ID), Title: sf.Title, Active: sf.ID == pane.ActiveSurface(),
			})
		}
		return &rpcapi.TreeNode{Pane: tp}
	}
	orient := rpcapi.OrientHorizontal
	if v.Orientation == domain.SplitVertical {
		orient = rpcapi.OrientVertical
	}
	return &rpcapi.TreeNode{Split: &rpcapi.TreeSplit{
		Orientation: orient,
		Ratio:       v.Ratio,
		First:       treeNodeFrom(w, v.First),
		Second:      treeNodeFrom(w, v.Second),
	}}
}

// --- pane.context ------------------------------------------------------------

// PaneContext projects the daemon-owned B10 context for one pane: cwd from
// the graph, bounded git facts from the injected collector, and the active
// surface's foreground process / recorded exit. Every field the daemon cannot
// determine stays zero — honest absence, never fabrication, and never a
// UI-local probe.
func (e *Engine) PaneContext(ctx context.Context, p rpcapi.PaneContextParams) (rpcapi.PaneContextResult, error) {
	st, err := e.state(ctx, domain.SessionID(p.Session))
	if err != nil {
		return rpcapi.PaneContextResult{}, err
	}
	w, ok := st.Workspace(domain.WorkspaceID(p.Workspace))
	if !ok {
		return rpcapi.PaneContextResult{}, &engineError{code: v1.ErrNotFound, msg: "workspace not found"}
	}
	pane, ok := w.Pane(domain.PaneID(p.Pane))
	if !ok {
		return rpcapi.PaneContextResult{}, &engineError{code: v1.ErrNotFound, msg: "pane not found"}
	}
	rt, err := e.runtime(domain.SessionID(p.Session))
	if err != nil {
		return rpcapi.PaneContextResult{}, err
	}

	out := rpcapi.PaneContextResult{
		Pane:      string(pane.ID),
		Cwd:       pane.Cwd,
		UpdatedMS: e.deps.Clock.NowUnixMilli(),
	}
	if e.deps.GitContext != nil && pane.Cwd != "" {
		if info, gerr := e.deps.GitContext(ctx, pane.Cwd); gerr == nil && info.Present {
			out.GitRoot = info.Root
			out.GitBranch = info.Branch
			out.GitDirty = info.Dirty
		}
	}
	active := pane.ActiveSurface()
	if active == "" {
		return out, nil
	}
	rt.mu.Lock()
	sr := rt.surfaces[active]
	var exitCode *int
	live := false
	if sr != nil {
		live = sr.live
		if sr.exitCode != nil {
			code := *sr.exitCode
			exitCode = &code
		}
	}
	rt.mu.Unlock()
	out.ExitCode = exitCode
	if live && e.deps.Foreground != nil && rt.sup != nil {
		if fd, ok := rt.sup.MasterFD(string(active)); ok {
			if pid, cmd, ferr := e.deps.Foreground(fd); ferr == nil {
				out.ForegroundPID = pid
				out.ForegroundCmd = cmd
			}
		}
	}
	return out, nil
}
