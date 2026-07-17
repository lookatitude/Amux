package render

import (
	"fmt"

	"github.com/amux-run/amux/internal/tui/geometry"
	"github.com/amux-run/amux/internal/tui/model"
)

// Options is the render capability profile (U7 accessibility). The zero value
// is the full-fidelity path; the accessible paths flip individual flags.
type Options struct {
	// Mono disables color, expressing focus/state via attributes (bold/reverse)
	// and glyphs only — the monochrome/no-color focus path.
	Mono bool
	// ASCIIBorders draws borders with ASCII (+-|) instead of box-drawing runes,
	// for terminals/screen-readers that mangle Unicode line art.
	ASCIIBorders bool
	// ReducedMotion suppresses spinners/animation; callers render a static
	// label instead of an animated frame (honored by the app layer).
	ReducedMotion bool
}

// PaneView is everything needed to draw one pane: its resolved geometry, the
// backend cell snapshot to project, and the presentation decorations derived
// from client state. All decoration fields are projections — the renderer only
// displays them.
type PaneView struct {
	Layout        geometry.PaneLayout
	Snapshot      model.CellSnapshot
	Focused       bool
	Title         string
	Class         model.SurfaceClass
	ExitReason    string
	Lease         model.LeaseState
	Attach        model.AttachPhase
	Process       string
	Cwd           string
	GitBranch     string
	GitDirty      bool
	ActiveSurface string // e.g. "2/3" (active index / count)
	Unread        int
	ShowCursor    bool
}

// Render composes the panes into a fresh rows×cols Screen. Panes are drawn in
// slice order; because geometry guarantees non-overlapping rects the order does
// not affect content, only border-adjacency ties (deterministic given a stable
// pane order). The caller supplies the pane order (sorted by id upstream).
func Render(rows, cols int, panes []PaneView, opts Options) *Screen {
	sc := NewScreen(rows, cols)
	st := resolveStyles(opts)
	for _, pv := range panes {
		drawPane(sc, pv, opts, st)
	}
	return sc
}

func drawPane(sc *Screen, pv PaneView, opts Options, st styleSet) {
	o := pv.Layout.Outer
	if o.Empty() {
		return
	}
	if pv.Layout.Bordered {
		drawBorder(sc, pv, opts, st)
	}
	c := pv.Layout.Content
	if pv.Layout.TooSmall || c.Empty() {
		drawTooSmall(sc, pv, st)
		return
	}
	drawContent(sc, pv, st)
	if pv.ShowCursor {
		drawCursor(sc, pv, st)
	}
}

// drawContent projects the snapshot's cells into the content rect, clipping to
// the rect and honouring authoritative cell widths (wide head + spacer).
func drawContent(sc *Screen, pv PaneView, st styleSet) {
	snap := pv.Snapshot
	c := pv.Layout.Content
	for row := 0; row < c.H; row++ {
		if row >= len(snap.Cells) {
			break
		}
		line := snap.Cells[row]
		col := 0
		for col < c.W {
			if col >= len(line) {
				break
			}
			cell := line[col]
			if cell.Width == 0 {
				col++ // spacer already covered by its wide head
				continue
			}
			sc.Set(c.X+col, c.Y+row, cell)
			if cell.Width == 2 {
				col += 2
			} else {
				col++
			}
		}
	}
}

func drawCursor(sc *Screen, pv PaneView, st styleSet) {
	cur := pv.Snapshot.Cursor
	if !cur.Visible {
		return
	}
	c := pv.Layout.Content
	if cur.Col < 0 || cur.Col >= c.W || cur.Row < 0 || cur.Row >= c.H {
		return
	}
	x, y := c.X+cur.Col, c.Y+cur.Row
	under := sc.At(x, y)
	under.Style = st.cursor
	if under.Content == "" {
		under.Content = " "
		under.Width = 1
	}
	sc.Set(x, y, under)
}

// drawTooSmall renders the minimum-size fallback: a single marker glyph so the
// operator sees the pane exists without a corrupt partial frame.
func drawTooSmall(sc *Screen, pv PaneView, st styleSet) {
	o := pv.Layout.Outer
	glyph := "!"
	if o.W >= 1 && o.H >= 1 {
		sc.SetRune(o.X, o.Y, glyph, st.warn)
	}
}

// drawBorder draws the pane box, embedding the title in the top edge and a
// compact status in the bottom edge. Focus is expressed by border weight/attr.
func drawBorder(sc *Screen, pv PaneView, opts Options, st styleSet) {
	o := pv.Layout.Outer
	b := borderRunes(opts.ASCIIBorders, pv.Focused)
	bs := st.border
	if pv.Focused {
		bs = st.borderFocus
	}
	x0, y0 := o.X, o.Y
	x1, y1 := o.Right()-1, o.Bottom()-1

	// corners
	sc.SetRune(x0, y0, b.tl, bs)
	sc.SetRune(x1, y0, b.tr, bs)
	sc.SetRune(x0, y1, b.bl, bs)
	sc.SetRune(x1, y1, b.br, bs)
	// edges
	for x := x0 + 1; x < x1; x++ {
		sc.SetRune(x, y0, b.h, bs)
		sc.SetRune(x, y1, b.h, bs)
	}
	for y := y0 + 1; y < y1; y++ {
		sc.SetRune(x0, y, b.v, bs)
		sc.SetRune(x1, y, b.v, bs)
	}

	inner := o.W - 2 // columns available on an edge between corners
	if inner <= 0 {
		return
	}
	// Top edge: focus marker + title.
	top := topLabel(pv)
	if top != "" {
		label := " " + truncate(top, inner-2) + " "
		sc.DrawText(x0+1, y0, label, pickStyle(st, pv.Focused))
	}
	// Bottom edge: status (process/cwd/git/surface/unread/lease/class).
	bottom := statusLabel(pv)
	if bottom != "" {
		label := " " + truncate(bottom, inner-2) + " "
		sc.DrawText(x0+1, y1, label, st.status)
	}
}

func pickStyle(st styleSet, focused bool) model.Style {
	if focused {
		return st.titleFocus
	}
	return st.title
}

// topLabel is the title segment: a focus dot, the title, and lease/attach glyph.
func topLabel(pv PaneView) string {
	title := pv.Title
	if title == "" {
		title = pv.ActiveSurface
	}
	lead := ""
	if pv.Focused {
		lead = "◆ "
	}
	glyph := leaseGlyph(pv.Lease)
	if glyph != "" {
		glyph = " " + glyph
	}
	return fmt.Sprintf("%s%s%s", lead, title, glyph)
}

// statusLabel composes the bottom-edge status without ever exceeding the edge
// (truncation happens at draw time). Fields the wire did not supply are omitted.
func statusLabel(pv PaneView) string {
	parts := make([]string, 0, 6)
	if pv.Class == model.ClassStopped {
		s := "stopped"
		if pv.ExitReason != "" {
			s += ": " + pv.ExitReason
		}
		parts = append(parts, "["+s+"]")
	} else if pv.Class == model.ClassRestarted {
		parts = append(parts, "[restarted]")
	}
	if pv.Attach != "" && pv.Attach != model.PhaseLive && pv.Attach != model.PhaseIdle {
		parts = append(parts, "‹"+string(pv.Attach)+"›")
	}
	if pv.Process != "" {
		parts = append(parts, pv.Process)
	}
	if pv.GitBranch != "" {
		g := pv.GitBranch
		if pv.GitDirty {
			g += "*"
		}
		parts = append(parts, g)
	}
	if pv.Cwd != "" {
		parts = append(parts, pv.Cwd)
	}
	if pv.Unread > 0 {
		parts = append(parts, fmt.Sprintf("•%d", pv.Unread))
	}
	return joinNonEmpty(parts, " · ")
}

func leaseGlyph(l model.LeaseState) string {
	switch l {
	case model.LeaseOwned:
		return "●"
	case model.LeaseOther:
		return "○"
	case model.LeaseReadOnly:
		return "◌"
	case model.LeaseLost:
		return "✗"
	default:
		return ""
	}
}

func joinNonEmpty(parts []string, sep string) string {
	out := ""
	for _, p := range parts {
		if p == "" {
			continue
		}
		if out != "" {
			out += sep
		}
		out += p
	}
	return out
}
