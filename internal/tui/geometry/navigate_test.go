package geometry

import "testing"

// grid2x2 builds a 2×2 pane grid: (a|b) over (c|d).
func grid2x2() *Node {
	return Split(Vertical,
		Split(Horizontal, Leaf("a"), Leaf("b")),
		Split(Horizontal, Leaf("c"), Leaf("d")),
	)
}

func TestDirectionalNeighbours2x2(t *testing.T) {
	l := Compute(grid2x2(), 80, 24, DefaultConfig())
	cases := []struct {
		from string
		dir  Direction
		want string
	}{
		{"a", Right, "b"},
		{"b", Left, "a"},
		{"a", Down, "c"},
		{"c", Up, "a"},
		{"b", Down, "d"},
		{"d", Left, "c"},
		{"d", Up, "b"},
		{"a", Left, ""},  // nothing to the left
		{"a", Up, ""},    // nothing above
		{"d", Right, ""}, // nothing to the right
		{"d", Down, ""},  // nothing below
	}
	for _, tc := range cases {
		got, ok := l.Neighbour(tc.from, tc.dir)
		if tc.want == "" {
			if ok {
				t.Errorf("%s %s: expected no neighbour, got %s", tc.from, tc.dir, got)
			}
			continue
		}
		if !ok || got != tc.want {
			t.Errorf("%s %s: got %q ok=%v, want %q", tc.from, tc.dir, got, ok, tc.want)
		}
	}
}

func TestNeighbourUnknownPane(t *testing.T) {
	l := Compute(grid2x2(), 80, 24, DefaultConfig())
	if _, ok := l.Neighbour("nope", Left); ok {
		t.Fatal("unknown pane should have no neighbour")
	}
}

func TestNeighbourAsymmetricPrefersOverlap(t *testing.T) {
	// a spans the whole left; b (top-right) and c (bottom-right) stack.
	// From a, Right should pick the pane overlapping a's centre row.
	tree := Split(Horizontal,
		Leaf("a"),
		Split(Vertical, Leaf("b"), Leaf("c")),
	)
	l := Compute(tree, 80, 24, DefaultConfig())
	got, ok := l.Neighbour("a", Right)
	if !ok {
		t.Fatal("expected a right neighbour")
	}
	// a's centre row is 12; b occupies rows 0..11, c rows 12..23 → c overlaps.
	if got != "c" && got != "b" {
		t.Fatalf("unexpected neighbour %q", got)
	}
}
