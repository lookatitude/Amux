// Package clientadapter is the ONE seam between the terminal UI's pure model
// layer and the frozen backend client contracts (internal/client, internal/
// rpcapi, api/v1). It maps the daemon's read-only wire projections onto the
// immutable model view types the renderer consumes, and classifies client
// errors into attach-recovery signals. It adds NO durable state and changes NO
// protocol semantics — it is a pure client-facing adapter.
//
// The four minor-1 projections delivered by T4 are consumed here directly:
//
//   - surface.cells  (rpcapi.CellGrid)          → model.CellSnapshot  (live cells)
//   - workspace.tree (rpcapi.WorkspaceTreeResult) → geometry.Node + panes (layout)
//   - pane.context   (rpcapi.PaneContextResult)  → pane git/process/exit decor
//   - hook.inspect   (rpcapi.HookInspectResult)  → full model.HookGrant trust card
//
// The UI never parses raw VT, owns an authoritative grid, sequences attach
// streams, decides trust, or discovers context locally: every field rendered
// here is a projection of daemon authority. Crucially this package does NOT
// import internal/terminal — the derived cell grid reaches the UI only through
// the surface.cells / attach-cells wire projection, never by reaching into the
// backend VT engine.
package clientadapter

import (
	"encoding/json"
	"errors"

	v1 "github.com/amux-run/amux/api/v1"
	"github.com/amux-run/amux/internal/client"
	"github.com/amux-run/amux/internal/rpcapi"
	"github.com/amux-run/amux/internal/tui/attachstate"
	"github.com/amux-run/amux/internal/tui/geometry"
	"github.com/amux-run/amux/internal/tui/model"
)

// --- surface.cells / attach cells → model.CellSnapshot -----------------------

// CellGridFromWire projects the daemon's derived cell-grid wire form
// (rpcapi.CellGrid, the bounded projection of terminal.CellSnapshot) onto the
// client-facing model.CellSnapshot. It is a pure field/element copy: cell
// widths are AUTHORITATIVE from the backend (no client-side width
// recomputation), and grapheme text — including combining marks and wide-cell
// heads/spacers — is copied verbatim. A nil grid yields an empty snapshot.
func CellGridFromWire(g *rpcapi.CellGrid, upToSeq uint64) model.CellSnapshot {
	if g == nil {
		return model.CellSnapshot{}
	}
	out := model.CellSnapshot{
		Rows: g.Rows, Cols: g.Cols, Title: g.Title, AltScreen: g.AltScreen,
		UpToSeq: upToSeq,
		Cursor:  model.Cursor{Row: g.Cursor.Row, Col: g.Cursor.Col, Visible: g.Cursor.Visible},
	}
	if len(g.Cells) > 0 {
		out.Cells = make([][]model.Cell, len(g.Cells))
		for r, row := range g.Cells {
			nr := make([]model.Cell, len(row))
			for c, cell := range row {
				nr[c] = model.Cell{
					Content: cell.Text,
					Width:   cell.Width,
					Style:   styleFromWire(cell.Style),
				}
			}
			out.Cells[r] = nr
		}
	}
	return out
}

// CellSnapshotFromResult projects a surface.cells result. When the daemon
// answered Unchanged (delta gate: nothing new since IfChangedSince) the grid is
// nil and the caller keeps its prior snapshot; ok reports whether a fresh grid
// was delivered.
func CellSnapshotFromResult(r rpcapi.SurfaceCellsResult) (snap model.CellSnapshot, ok bool) {
	if r.Unchanged || r.Grid == nil {
		return model.CellSnapshot{}, false
	}
	return CellGridFromWire(r.Grid, r.UpToSeq), true
}

// CellSnapshotFromAttach projects the exact snapshot-at-N cell grid carried in
// the opt-in attach_snapshot "cells" payload (rpcapi.AttachSnapshotCells).
func CellSnapshotFromAttach(a rpcapi.AttachSnapshotCells) model.CellSnapshot {
	return CellGridFromWire(&a.Grid, a.UpToSeq)
}

// --- attach stream (flow 12) → attach lifecycle projections -------------------

// AttachSnapshot is the client-facing projection of the attach_snapshot event
// header (flow 12): the daemon-declared cutover sequence, the opt-in exact
// snapshot-at-N cell grid, and the typed replay-gap boundary when the requested
// replay cursor was evicted. Every field is daemon truth; the client folds it
// and never re-derives or stitches sequences.
type AttachSnapshot struct {
	Surface string
	Title   string
	UpToSeq uint64
	// Cells is the exact snapshot-at-N grid (nil when the attach did not opt in).
	Cells *model.CellSnapshot
	// Gap is the replay_gap boundary (nil when replay is contiguous).
	Gap *attachstate.GapInfo
}

// AttachSnapshotFromPayload decodes an attach_snapshot event payload into the
// projection above. The payload shape is the flow-12 wire contract
// (surface/rows/cols/title/up_to_seq + optional cells + optional replay_gap);
// unknown fields are ignored (event payloads are lenient, ADR-0003).
func AttachSnapshotFromPayload(payload []byte) (AttachSnapshot, error) {
	var p struct {
		Surface   string                      `json:"surface"`
		Title     string                      `json:"title"`
		UpToSeq   uint64                      `json:"up_to_seq"`
		Cells     *rpcapi.AttachSnapshotCells `json:"cells"`
		ReplayGap *struct {
			RequestedFrom  uint64 `json:"requested_from"`
			OldestRetained uint64 `json:"oldest_retained"`
			LatestSeq      uint64 `json:"latest_seq"`
		} `json:"replay_gap"`
	}
	if err := json.Unmarshal(payload, &p); err != nil {
		return AttachSnapshot{}, err
	}
	out := AttachSnapshot{Surface: p.Surface, Title: p.Title, UpToSeq: p.UpToSeq}
	if p.Cells != nil {
		cs := CellSnapshotFromAttach(*p.Cells)
		out.Cells = &cs
	}
	if p.ReplayGap != nil {
		out.Gap = &attachstate.GapInfo{
			RequestedFrom:  p.ReplayGap.RequestedFrom,
			OldestRetained: p.ReplayGap.OldestRetained,
			LatestSeq:      p.ReplayGap.LatestSeq,
		}
	}
	return out, nil
}

func styleFromWire(s *rpcapi.CellStyle) model.Style {
	if s == nil {
		return model.Style{}
	}
	// The wire attribute bitmask mirrors the engine SGR model, whose bit order
	// model.Attr also mirrors (Bold..Strike), so the mask copies directly.
	return model.Style{
		FG:    colorFromWire(s.FG),
		BG:    colorFromWire(s.BG),
		Attrs: model.Attr(s.Attrs),
	}
}

func colorFromWire(c *rpcapi.CellColor) model.Color {
	if c == nil {
		return model.Color{}
	}
	out := model.Color{Index: c.Index, R: c.R, G: c.G, B: c.B}
	switch c.Mode {
	case rpcapi.CellColorANSI:
		out.Mode = model.ColorANSI
	case rpcapi.CellColor256:
		out.Mode = model.Color256
	case rpcapi.CellColorRGB:
		out.Mode = model.ColorRGB
	default:
		out.Mode = model.ColorDefault
	}
	return out
}

// --- workspace.tree → geometry.Node + panes ----------------------------------

// TreeFromWire projects the authoritative workspace split-tree
// (rpcapi.WorkspaceTreeResult) onto the pure geometry tree the layout engine
// lays out, the focused pane id, and the pane view models. The daemon owns the
// tree; this only shapes its own read-only projection for layout — no second
// authoritative layout model is kept (ADR: the wire tree is a VIEW).
func TreeFromWire(r rpcapi.WorkspaceTreeResult) (root *geometry.Node, focused string, panes []model.Pane) {
	root = treeNodeFromWire(r.Root)
	focused = r.Focused
	if r.Root != nil {
		panes = collectPanes(r.Root, r.Focused)
	}
	return root, focused, panes
}

func treeNodeFromWire(n *rpcapi.TreeNode) *geometry.Node {
	if n == nil {
		return nil
	}
	if n.Pane != nil {
		return geometry.Leaf(n.Pane.ID)
	}
	if n.Split == nil {
		return nil
	}
	first := treeNodeFromWire(n.Split.First)
	second := treeNodeFromWire(n.Split.Second)
	node := &geometry.Node{
		Orient:   orientFromWire(n.Split.Orientation),
		Children: []*geometry.Node{first, second},
	}
	// The wire split is binary with a single ratio for First; geometry carries
	// one positive ratio per child. A degenerate (<=0 or >=1) ratio falls back
	// to an equal split (nil Ratios), which the layout engine normalises.
	if r := n.Split.Ratio; r > 0 && r < 1 {
		node.Ratios = []float64{r, 1 - r}
	}
	return node
}

func collectPanes(n *rpcapi.TreeNode, focused string) []model.Pane {
	var out []model.Pane
	var walk func(*rpcapi.TreeNode)
	walk = func(node *rpcapi.TreeNode) {
		if node == nil {
			return
		}
		if node.Pane != nil {
			out = append(out, paneFromTree(*node.Pane, focused))
			return
		}
		if node.Split != nil {
			walk(node.Split.First)
			walk(node.Split.Second)
		}
	}
	walk(n)
	return out
}

func paneFromTree(p rpcapi.TreePane, focused string) model.Pane {
	out := model.Pane{
		ID:      p.ID,
		Cwd:     p.Cwd,
		Project: p.Project,
		Focused: p.Focused || p.ID == focused,
		Active:  p.Active,
	}
	for _, s := range p.Surfaces {
		out.Surfaces = append(out.Surfaces, SurfaceFromWire(s))
	}
	return out
}

func orientFromWire(o rpcapi.Orientation) geometry.Orientation {
	if o == rpcapi.OrientVertical {
		return geometry.Vertical
	}
	return geometry.Horizontal
}

// --- pane.context → pane decorations -----------------------------------------

// ApplyPaneContext folds a pane.context projection (rpcapi.PaneContextResult)
// into a pane's optional status decorations. Zero wire fields mean "not
// determined" (collector unavailable / fail-closed) and are left zero — never
// fabricated. ExitCode, when present, is not stored on the pane (surface class
// carries exit classification); cwd/git/foreground decorate the status line.
func ApplyPaneContext(p model.Pane, ctx rpcapi.PaneContextResult) model.Pane {
	if ctx.Cwd != "" {
		p.Cwd = ctx.Cwd
	}
	p.GitBranch = ctx.GitBranch
	p.GitDirty = ctx.GitDirty
	p.ForegroundCmd = ctx.ForegroundCmd
	return p
}

// --- hook.inspect → full trust card ------------------------------------------

// GrantsFromInspect projects a hook.inspect result (rpcapi.HookInspectResult)
// onto the full model.HookGrant trust cards the confirmation UI displays. Every
// frozen trust field the confirmation matrix requires is now on the wire, so
// the cards are TrustComplete (no UNAVAILABLE markers) unless the daemon itself
// left a field empty. The UI DISPLAYS these and never decides authorization.
func GrantsFromInspect(r rpcapi.HookInspectResult) []model.HookGrant {
	out := make([]model.HookGrant, 0, len(r.Grants))
	for _, g := range r.Grants {
		out = append(out, GrantDetailToModel(g, r.Project))
	}
	return out
}

// GrantDetailToModel maps one rpcapi.HookGrantDetail plus the project trust
// identity onto a model.HookGrant. The env allowlist carries KEY NAMES ONLY
// (values never cross the wire, ADR-0005); cwd scope combines the scope kind
// with the fixed path when present.
func GrantDetailToModel(g rpcapi.HookGrantDetail, project rpcapi.HookProjectTrust) model.HookGrant {
	scope := g.Scope
	if g.FixedPath != "" {
		scope = g.Scope + ":" + g.FixedPath
	}
	return model.HookGrant{
		ID:         g.ID,
		HookID:     g.HookID,
		Project:    project.Root,
		Events:     append([]string(nil), g.Events...),
		Scope:      g.Scope,
		Active:     g.Active,
		BoundEpoch: g.BoundEpoch,

		Executable: g.ExecPath,
		Digest:     g.ExecSHA256,
		CwdScope:   scope,
		EnvKeys:    append([]string(nil), g.EnvKeys...),
		TimeoutMS:  g.TimeoutMS,
		OutputCapB: g.OutputCap,
	}
}

// --- error classification & other wire projections ---------------------------

// ClassifyErr maps a client error into an attach-recovery signal. It reads the
// frozen v1 error taxonomy and client.ErrBootChanged only — never message
// strings (automation branches on codes, STR-6).
func ClassifyErr(err error) attachstate.ErrKind {
	if err == nil {
		return attachstate.ErrNone
	}
	if errors.Is(err, client.ErrBootChanged) {
		return attachstate.ErrBootChanged
	}
	switch client.CodeOf(err) {
	case v1.ErrReplayGap:
		return attachstate.ErrReplayGap
	case v1.ErrEventGap:
		return attachstate.ErrEventGap
	case v1.ErrResourceExhausted:
		return attachstate.ErrSlowConsumer
	case v1.ErrInternal:
		// The client types a lost connection as a retryable internal error.
		var e *client.Error
		if errors.As(err, &e) && e.Retryable {
			return attachstate.ErrConnLost
		}
	}
	return attachstate.ErrNone
}

// IsLeaseDenied reports whether err is the daemon's typed input-lease
// rejection (v1.ErrNotInputLeaseHolder): another client holds the lease, so
// the UI presents read-only state. Code-based, never message-string matching.
func IsLeaseDenied(err error) bool {
	return client.CodeOf(err) == v1.ErrNotInputLeaseHolder
}

// HealthFromWire maps rpcapi.HealthResult onto model.Health.
func HealthFromWire(h rpcapi.HealthResult) model.Health {
	return model.Health{
		BootID: h.BootID, Version: h.Version, Protocol: h.Protocol,
		Sessions: h.Sessions, UptimeMS: h.UptimeMS,
	}
}

// SurfaceFromWire maps rpcapi.SurfaceInfo onto model.Surface.
func SurfaceFromWire(s rpcapi.SurfaceInfo) model.Surface {
	return model.Surface{
		ID: s.ID, Title: s.Title, Active: s.Active,
		Class: classFromWire(s.Class), ExitReason: s.ExitReason,
	}
}

func classFromWire(c string) model.SurfaceClass {
	switch c {
	case "live":
		return model.ClassLive
	case "restarted":
		return model.ClassRestarted
	case "stopped":
		return model.ClassStopped
	default:
		return model.ClassUnknown
	}
}

// PaneFromWire maps rpcapi.PaneInspectResult onto model.Pane (the pane.inspect
// flow). Git/process context is populated separately via ApplyPaneContext from
// the pane.context projection; it is left zero here rather than guessed.
func PaneFromWire(p rpcapi.PaneInspectResult) model.Pane {
	out := model.Pane{
		ID: p.Pane, Cwd: p.Cwd, Project: p.Project, Focused: p.Focused,
		Active: p.Active, LatestSeq: p.LatestSeq,
	}
	for _, s := range p.Surfaces {
		out.Surfaces = append(out.Surfaces, SurfaceFromWire(s))
	}
	return out
}

// NotificationFromWire maps rpcapi.NotificationInfo onto model.Notification.
func NotificationFromWire(n rpcapi.NotificationInfo) model.Notification {
	return model.Notification{
		ID: n.ID, Kind: model.NotificationKind(n.Kind), Title: n.Title,
		Body: n.Body, CreatedMS: n.CreatedMS, Read: n.Read,
	}
}
