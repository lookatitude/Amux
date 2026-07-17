package geometry

import (
	"fmt"
	"testing"
)

// tiles asserts the panes exactly cover the bounds with no overlap and no gap:
// every cell in Bounds belongs to exactly one pane's Outer rect.
func tiles(t *testing.T, l Layout) {
	t.Helper()
	b := l.Bounds
	if b.Empty() {
		return
	}
	owner := make([][]string, b.H)
	for y := range owner {
		owner[y] = make([]string, b.W)
	}
	for _, p := range l.Panes {
		r := p.Outer
		for y := r.Y; y < r.Bottom(); y++ {
			for x := r.X; x < r.Right(); x++ {
				if y < 0 || y >= b.H || x < 0 || x >= b.W {
					t.Fatalf("pane %s rect %+v out of bounds %+v", p.PaneID, r, b)
				}
				if owner[y][x] != "" {
					t.Fatalf("overlap at (%d,%d): %s and %s", x, y, owner[y][x], p.PaneID)
				}
				owner[y][x] = p.PaneID
			}
		}
	}
	for y := 0; y < b.H; y++ {
		for x := 0; x < b.W; x++ {
			if owner[y][x] == "" {
				t.Fatalf("gap at (%d,%d): no pane covers it", x, y)
			}
		}
	}
}

// contentInside asserts each content rect sits within its outer rect.
func contentInside(t *testing.T, l Layout) {
	t.Helper()
	for _, p := range l.Panes {
		c, o := p.Content, p.Outer
		if c.W == 0 || c.H == 0 {
			continue
		}
		if c.X < o.X || c.Y < o.Y || c.Right() > o.Right() || c.Bottom() > o.Bottom() {
			t.Fatalf("pane %s content %+v escapes outer %+v", p.PaneID, c, o)
		}
	}
}

func TestSinglePaneFillsTerminal(t *testing.T) {
	l := Compute(Leaf("p1"), 80, 24, DefaultConfig())
	if len(l.Panes) != 1 {
		t.Fatalf("want 1 pane, got %d", len(l.Panes))
	}
	p, _ := l.Pane("p1")
	if p.Outer != (Rect{0, 0, 80, 24}) {
		t.Fatalf("outer = %+v", p.Outer)
	}
	if p.Content != (Rect{1, 1, 78, 22}) {
		t.Fatalf("content = %+v", p.Content)
	}
	tiles(t, l)
	contentInside(t, l)
}

func TestTwoPaneHorizontalOddWidth(t *testing.T) {
	// 81 is odd; the leading child absorbs the extra column deterministically.
	l := Compute(Split(Horizontal, Leaf("a"), Leaf("b")), 81, 24, DefaultConfig())
	a, _ := l.Pane("a")
	b, _ := l.Pane("b")
	if a.Outer.W+b.Outer.W != 81 {
		t.Fatalf("widths %d+%d != 81", a.Outer.W, b.Outer.W)
	}
	if a.Outer.W != 41 || b.Outer.W != 40 {
		t.Fatalf("odd split widths a=%d b=%d (want 41,40)", a.Outer.W, b.Outer.W)
	}
	tiles(t, l)
	contentInside(t, l)
}

func TestNestedSplitTilesAndNoOverlap(t *testing.T) {
	// a | (b / c) — a horizontal split whose right child is a vertical split.
	tree := Split(Horizontal,
		Leaf("a"),
		Split(Vertical, Leaf("b"), Leaf("c")),
	)
	l := Compute(tree, 80, 25, DefaultConfig())
	if len(l.Panes) != 3 {
		t.Fatalf("want 3 panes, got %d", len(l.Panes))
	}
	tiles(t, l)
	contentInside(t, l)
}

// buildBalanced builds a balanced binary split tree with n leaves, alternating
// orientation by depth, to exercise 1..8 panes uniformly.
func buildBalanced(ids []string, o Orientation) *Node {
	if len(ids) == 1 {
		return Leaf(ids[0])
	}
	mid := len(ids) / 2
	next := Horizontal
	if o == Horizontal {
		next = Vertical
	}
	return Split(o, buildBalanced(ids[:mid], next), buildBalanced(ids[mid:], next))
}

func TestOneToEightPanesTileExactly(t *testing.T) {
	for n := 1; n <= 8; n++ {
		ids := make([]string, n)
		for i := range ids {
			ids[i] = fmt.Sprintf("p%d", i)
		}
		tree := buildBalanced(ids, Horizontal)
		for _, dim := range [][2]int{{80, 24}, {81, 25}, {132, 43}, {37, 19}} {
			l := Compute(tree, dim[0], dim[1], DefaultConfig())
			if len(l.Panes) != n {
				t.Fatalf("n=%d dim=%v: got %d panes", n, dim, len(l.Panes))
			}
			tiles(t, l)
			contentInside(t, l)
		}
	}
}

func TestTinyTerminalDropsBorderAndFlagsTooSmall(t *testing.T) {
	// A 2×2 terminal cannot host a border (needs 3×3); border is dropped and the
	// whole area becomes content, but content is below the default 1×1? 2×2 >=
	// 1×1 so not too small; check a 1x1.
	l := Compute(Leaf("p"), 2, 2, DefaultConfig())
	p, _ := l.Pane("p")
	if p.Bordered {
		t.Fatalf("2x2 pane should not be bordered")
	}
	if p.Content != (Rect{0, 0, 2, 2}) {
		t.Fatalf("content = %+v, want full 2x2", p.Content)
	}
	// Split a 3-wide terminal into two: each child is too narrow for a border.
	l2 := Compute(Split(Horizontal, Leaf("a"), Leaf("b")), 3, 10, DefaultConfig())
	tiles(t, l2)
	for _, pl := range l2.Panes {
		if pl.Bordered {
			t.Fatalf("pane %s too narrow to border but Bordered=true", pl.PaneID)
		}
	}
}

func TestZeroSizeTerminalIsEmptyNotPanic(t *testing.T) {
	l := Compute(Split(Horizontal, Leaf("a"), Leaf("b")), 0, 0, DefaultConfig())
	if !l.Bounds.Empty() {
		t.Fatalf("bounds should be empty")
	}
	// Panes still exist with zero-area rects; must not panic and must flag small.
	for _, p := range l.Panes {
		if !p.TooSmall {
			t.Fatalf("pane %s in 0x0 terminal must be TooSmall", p.PaneID)
		}
	}
}

func TestRatioSplitRoundsDeterministically(t *testing.T) {
	tree := &Node{Orient: Horizontal, Children: []*Node{Leaf("a"), Leaf("b"), Leaf("c")}, Ratios: []float64{0.5, 0.3, 0.2}}
	l := Compute(tree, 100, 10, DefaultConfig())
	a, _ := l.Pane("a")
	b, _ := l.Pane("b")
	c, _ := l.Pane("c")
	if a.Outer.W != 50 || b.Outer.W != 30 || c.Outer.W != 20 {
		t.Fatalf("ratio widths a=%d b=%d c=%d", a.Outer.W, b.Outer.W, c.Outer.W)
	}
	tiles(t, l)
}

func TestDivideSumsToTotal(t *testing.T) {
	for _, total := range []int{0, 1, 3, 7, 80, 81, 99, 100, 133} {
		for _, ratios := range [][]float64{{0.5, 0.5}, {0.33, 0.33, 0.34}, {0.1, 0.2, 0.3, 0.4}, {1.0 / 3, 1.0 / 3, 1.0 / 3}} {
			sizes := divide(total, ratios)
			sum := 0
			for _, s := range sizes {
				if s < 0 {
					t.Fatalf("negative size %d", s)
				}
				sum += s
			}
			if total > 0 && sum != total {
				t.Fatalf("total=%d ratios=%v: sizes sum to %d", total, ratios, sum)
			}
		}
	}
}
