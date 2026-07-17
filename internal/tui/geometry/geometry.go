// Package geometry is the pure split-tree layout engine for the terminal UI
// (U2). Every function here is deterministic and side-effect free: given a
// pane split tree and a terminal size it computes non-overlapping outer and
// content rectangles, resolves directional neighbours, equalizes ratios, and
// adjusts a split ratio — with no dependency on Bubble Tea, the renderer, or
// the daemon. The daemon owns the authoritative pane tree; this package only
// lays out the tree it is given, so it can be golden- and property-tested in
// isolation (the lane's core deliverable to T6).
//
// Coordinate model: cells, origin top-left, X grows right, Y grows down. A
// Rect is a half-open region [X, X+W) × [Y, Y+H). Borders (when enabled) are a
// one-cell box around each pane; the content Rect is the pane inset by the
// border. Rounding remainder from ratio division is distributed to the leading
// children deterministically so a layout is a pure function of (tree, size,
// config).
package geometry

// Orientation is a split's child arrangement.
type Orientation int

const (
	// Horizontal arranges children left-to-right (columns); the split divides
	// width and shares height. The wire's "horizontal" split.
	Horizontal Orientation = iota
	// Vertical arranges children top-to-bottom (rows); the split divides height
	// and shares width. The wire's "vertical" split.
	Vertical
)

func (o Orientation) String() string {
	if o == Vertical {
		return "vertical"
	}
	return "horizontal"
}

// Rect is a half-open cell rectangle.
type Rect struct {
	X, Y, W, H int
}

// Empty reports whether the rect has no area.
func (r Rect) Empty() bool { return r.W <= 0 || r.H <= 0 }

// Right is the exclusive right edge (X+W).
func (r Rect) Right() int { return r.X + r.W }

// Bottom is the exclusive bottom edge (Y+H).
func (r Rect) Bottom() int { return r.Y + r.H }

// CenterX/CenterY are integer centres used by neighbour resolution.
func (r Rect) CenterX() int { return r.X + r.W/2 }
func (r Rect) CenterY() int { return r.Y + r.H/2 }

// Config tunes the layout without changing the tree.
type Config struct {
	// Border draws a one-cell box around each pane; content is inset by it.
	// Disabled for the monochrome/minimum-size fallback where every cell counts.
	Border bool
	// MinContentW/MinContentH are the minimum usable content dimensions. A pane
	// whose content falls below either is flagged TooSmall so the renderer shows
	// the minimum-size fallback instead of a corrupt frame.
	MinContentW, MinContentH int
}

// DefaultConfig is the standard bordered layout with a 1×1 minimum content.
func DefaultConfig() Config {
	return Config{Border: true, MinContentW: 1, MinContentH: 1}
}

// PaneLayout is one leaf pane's resolved placement.
type PaneLayout struct {
	PaneID string
	// Outer is the full allocated region including any border.
	Outer Rect
	// Content is the region available for cell rendering (Outer inset by the
	// border when Config.Border is set); clamped to non-negative.
	Content Rect
	// Bordered records whether a border was drawn for this pane (false when the
	// pane is too small to host both a border and any content).
	Bordered bool
	// TooSmall marks a pane whose content is below Config minimums; the renderer
	// must show the minimum-size fallback rather than partial cells.
	TooSmall bool
}
