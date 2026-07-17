package geometry

import "fmt"

// Node is a split-tree node. A leaf carries a non-empty PaneID and no children;
// a split carries an orientation, >=2 children, and one ratio per child that
// sums to ~1. The tree is a value structure the caller builds from the daemon's
// pane graph; layout never mutates it.
type Node struct {
	PaneID   string
	Orient   Orientation
	Children []*Node
	// Ratios has one entry per child (fraction of the split's primary dimension).
	// nil/empty means "equal split". Values need not be pre-normalised; layout
	// normalises defensively.
	Ratios []float64
}

// Leaf builds a leaf node for paneID.
func Leaf(paneID string) *Node { return &Node{PaneID: paneID} }

// Split builds a split node with equal ratios over the given children.
func Split(o Orientation, children ...*Node) *Node {
	return &Node{Orient: o, Children: children}
}

// IsLeaf reports whether n is a leaf pane.
func (n *Node) IsLeaf() bool { return n == nil || len(n.Children) == 0 }

// Leaves returns every leaf pane id in stable left-to-right / top-to-bottom
// (pre-order) sequence.
func (n *Node) Leaves() []string {
	var out []string
	n.walkLeaves(func(id string) { out = append(out, id) })
	return out
}

func (n *Node) walkLeaves(fn func(string)) {
	if n == nil {
		return
	}
	if n.IsLeaf() {
		if n.PaneID != "" {
			fn(n.PaneID)
		}
		return
	}
	for _, c := range n.Children {
		c.walkLeaves(fn)
	}
}

// Count returns the number of leaf panes.
func (n *Node) Count() int {
	c := 0
	n.walkLeaves(func(string) { c++ })
	return c
}

// Clone deep-copies the tree so callers can mutate ratios without aliasing the
// authoritative structure.
func (n *Node) Clone() *Node {
	if n == nil {
		return nil
	}
	cp := &Node{PaneID: n.PaneID, Orient: n.Orient}
	if len(n.Ratios) > 0 {
		cp.Ratios = append([]float64(nil), n.Ratios...)
	}
	for _, c := range n.Children {
		cp.Children = append(cp.Children, c.Clone())
	}
	return cp
}

// Validate reports the first structural defect in the tree, or nil. A well-
// formed tree has: unique non-empty leaf ids, every split with >=2 children,
// and (when present) exactly one positive ratio per child.
func (n *Node) Validate() error {
	seen := map[string]bool{}
	return validate(n, seen)
}

func validate(n *Node, seen map[string]bool) error {
	if n == nil {
		return fmt.Errorf("geometry: nil node")
	}
	if n.IsLeaf() {
		if n.PaneID == "" {
			return fmt.Errorf("geometry: leaf with empty pane id")
		}
		if seen[n.PaneID] {
			return fmt.Errorf("geometry: duplicate pane id %q", n.PaneID)
		}
		seen[n.PaneID] = true
		return nil
	}
	if len(n.Children) < 2 {
		return fmt.Errorf("geometry: split with %d children (need >=2)", len(n.Children))
	}
	if len(n.Ratios) != 0 && len(n.Ratios) != len(n.Children) {
		return fmt.Errorf("geometry: %d ratios for %d children", len(n.Ratios), len(n.Children))
	}
	for _, r := range n.Ratios {
		if r <= 0 {
			return fmt.Errorf("geometry: non-positive ratio %g", r)
		}
	}
	for _, c := range n.Children {
		if err := validate(c, seen); err != nil {
			return err
		}
	}
	return nil
}

// normRatios returns a defensive normalised copy of a split's ratios: equal
// when unset, and rescaled to sum to 1 otherwise. Never returns nil for a
// split with children.
func normRatios(n *Node) []float64 {
	k := len(n.Children)
	out := make([]float64, k)
	if len(n.Ratios) != k {
		for i := range out {
			out[i] = 1.0 / float64(k)
		}
		return out
	}
	var sum float64
	for _, r := range n.Ratios {
		if r > 0 {
			sum += r
		}
	}
	if sum <= 0 {
		for i := range out {
			out[i] = 1.0 / float64(k)
		}
		return out
	}
	for i, r := range n.Ratios {
		if r <= 0 {
			r = 0
		}
		out[i] = r / sum
	}
	return out
}
