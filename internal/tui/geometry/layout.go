package geometry

import "sort"

// Layout is the resolved placement of every pane plus the bounds it was solved
// in. Panes is ordered by pane id for deterministic iteration and golden
// output; use Pane(id) for direct lookup.
type Layout struct {
	Bounds Rect
	Panes  []PaneLayout
	index  map[string]int
}

// Pane returns the layout for a pane id.
func (l Layout) Pane(id string) (PaneLayout, bool) {
	if l.index == nil {
		return PaneLayout{}, false
	}
	i, ok := l.index[id]
	if !ok {
		return PaneLayout{}, false
	}
	return l.Panes[i], true
}

// Compute lays out the tree inside a terminal of width×height using cfg. It
// distributes ratio-rounding remainder to leading children so the union of
// pane rects exactly tiles the bounds with no overlap and no gap (borders are
// inside each pane's own rect). A nil/empty tree yields an empty layout.
func Compute(root *Node, width, height int, cfg Config) Layout {
	if width < 0 {
		width = 0
	}
	if height < 0 {
		height = 0
	}
	bounds := Rect{X: 0, Y: 0, W: width, H: height}
	l := Layout{Bounds: bounds, index: map[string]int{}}
	if root == nil {
		return l
	}
	place(root, bounds, cfg, &l)
	sort.Slice(l.Panes, func(i, j int) bool { return l.Panes[i].PaneID < l.Panes[j].PaneID })
	for i, p := range l.Panes {
		l.index[p.PaneID] = i
	}
	return l
}

// place recursively assigns rects. A leaf becomes a PaneLayout; a split divides
// its primary dimension by normalised ratios with deterministic remainder
// distribution.
func place(n *Node, area Rect, cfg Config, out *Layout) {
	if n == nil {
		return
	}
	if n.IsLeaf() {
		out.Panes = append(out.Panes, leafLayout(n.PaneID, area, cfg))
		return
	}
	ratios := normRatios(n)
	total := area.W
	if n.Orient == Vertical {
		total = area.H
	}
	sizes := divide(total, ratios)
	pos := area.X
	if n.Orient == Vertical {
		pos = area.Y
	}
	for i, c := range n.Children {
		var child Rect
		if n.Orient == Horizontal {
			child = Rect{X: pos, Y: area.Y, W: sizes[i], H: area.H}
		} else {
			child = Rect{X: area.X, Y: pos, W: area.W, H: sizes[i]}
		}
		pos += sizes[i]
		place(c, child, cfg, out)
	}
}

// leafLayout derives a pane's outer/content rects and small-size flags.
func leafLayout(id string, outer Rect, cfg Config) PaneLayout {
	pl := PaneLayout{PaneID: id, Outer: outer, Content: outer}
	if cfg.Border {
		// A border needs at least 3×3 to enclose a 1×1 content cell; below that
		// we drop the border and hand the whole area to content so tiny panes
		// still show something rather than an all-border box.
		if outer.W >= 3 && outer.H >= 3 {
			pl.Bordered = true
			pl.Content = Rect{X: outer.X + 1, Y: outer.Y + 1, W: outer.W - 2, H: outer.H - 2}
		}
	}
	if pl.Content.W < 0 {
		pl.Content.W = 0
	}
	if pl.Content.H < 0 {
		pl.Content.H = 0
	}
	minW, minH := cfg.MinContentW, cfg.MinContentH
	if minW < 1 {
		minW = 1
	}
	if minH < 1 {
		minH = 1
	}
	if pl.Content.W < minW || pl.Content.H < minH {
		pl.TooSmall = true
	}
	return pl
}

// divide splits total cells among len(ratios) children by proportion, giving
// every child floor(ratio*total) and distributing the leftover cells one each
// to the children with the largest fractional remainders (ties by index). The
// result always sums to exactly total (>=0) so panes tile without gaps.
func divide(total int, ratios []float64) []int {
	k := len(ratios)
	sizes := make([]int, k)
	if k == 0 || total <= 0 {
		return sizes
	}
	type frac struct {
		i int
		f float64
	}
	rem := make([]frac, k)
	assigned := 0
	for i, r := range ratios {
		exact := r * float64(total)
		base := int(exact) // floor for non-negative exact
		sizes[i] = base
		assigned += base
		rem[i] = frac{i, exact - float64(base)}
	}
	leftover := total - assigned
	if leftover > 0 {
		sort.SliceStable(rem, func(a, b int) bool {
			if rem[a].f != rem[b].f {
				return rem[a].f > rem[b].f
			}
			return rem[a].i < rem[b].i
		})
		for j := 0; j < leftover; j++ {
			sizes[rem[j%k].i]++
		}
	}
	return sizes
}
