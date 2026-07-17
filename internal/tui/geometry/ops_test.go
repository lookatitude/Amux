package geometry

import (
	"math"
	"testing"
)

func TestEqualizeResetsRatios(t *testing.T) {
	tree := &Node{Orient: Horizontal, Children: []*Node{Leaf("a"), Leaf("b"), Leaf("c")}, Ratios: []float64{0.7, 0.2, 0.1}}
	eq := Equalize(tree)
	// original untouched (purity)
	if tree.Ratios[0] != 0.7 {
		t.Fatal("Equalize mutated input tree")
	}
	for _, r := range eq.Ratios {
		if math.Abs(r-1.0/3) > 1e-9 {
			t.Fatalf("ratio %g not equalized", r)
		}
	}
	l := Compute(eq, 90, 10, DefaultConfig())
	a, _ := l.Pane("a")
	b, _ := l.Pane("b")
	c, _ := l.Pane("c")
	if a.Outer.W != 30 || b.Outer.W != 30 || c.Outer.W != 30 {
		t.Fatalf("equalized widths a=%d b=%d c=%d", a.Outer.W, b.Outer.W, c.Outer.W)
	}
}

func TestResizeAdjustsParentSplit(t *testing.T) {
	tree := Split(Horizontal, Leaf("a"), Leaf("b")) // equal
	grown, err := Resize(tree, "a", +0.2)
	if err != nil {
		t.Fatal(err)
	}
	// original untouched
	if len(tree.Ratios) != 0 {
		t.Fatal("Resize mutated input tree ratios")
	}
	r := normRatios(grown)
	if math.Abs(r[0]-0.7) > 1e-9 || math.Abs(r[1]-0.3) > 1e-9 {
		t.Fatalf("after +0.2 grow: ratios %v (want 0.7,0.3)", r)
	}
}

func TestResizeClampsSiblingFloor(t *testing.T) {
	tree := Split(Horizontal, Leaf("a"), Leaf("b"))
	grown, err := Resize(tree, "a", +5.0) // absurd delta
	if err != nil {
		t.Fatal(err)
	}
	r := normRatios(grown)
	if r[1] < MinRatio-1e-9 {
		t.Fatalf("sibling starved below floor: %v", r)
	}
	if r[0] > 1-MinRatio+1e-9 {
		t.Fatalf("pane exceeded ceiling: %v", r)
	}
}

func TestResizeRootLeafErrors(t *testing.T) {
	if _, err := Resize(Leaf("solo"), "solo", 0.1); err == nil {
		t.Fatal("resizing the root leaf must error (no parent split)")
	}
}

func TestValidateCatchesDefects(t *testing.T) {
	if err := Split(Horizontal, Leaf("a"), Leaf("a")).Validate(); err == nil {
		t.Fatal("duplicate pane id should fail validation")
	}
	if err := (&Node{Orient: Horizontal, Children: []*Node{Leaf("a")}}).Validate(); err == nil {
		t.Fatal("split with 1 child should fail validation")
	}
	if err := grid2x2().Validate(); err != nil {
		t.Fatalf("valid tree rejected: %v", err)
	}
}
