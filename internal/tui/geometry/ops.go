package geometry

import "fmt"

// Equalize returns a copy of the tree with every split reset to equal ratios.
// Pure: the input tree is untouched.
func Equalize(root *Node) *Node {
	c := root.Clone()
	equalize(c)
	return c
}

func equalize(n *Node) {
	if n == nil || n.IsLeaf() {
		return
	}
	k := len(n.Children)
	n.Ratios = make([]float64, k)
	for i := range n.Ratios {
		n.Ratios[i] = 1.0 / float64(k)
	}
	for _, c := range n.Children {
		equalize(c)
	}
}

// MinRatio is the floor a single child may be resized to, so a resize can never
// starve a sibling to zero cells.
const MinRatio = 0.05

// Resize returns a copy of the tree in which the split that is the PARENT of
// pane `paneID` has its ratio for that pane adjusted by delta (its immediate
// sibling absorbs the change). delta is a fraction of the split's primary
// dimension (e.g. +0.05 grows the pane by 5%). The pane and its resized sibling
// are each clamped to [MinRatio, 1-MinRatio]. Returns an error when the pane is
// the root leaf (no parent split) or is not found. Pure.
func Resize(root *Node, paneID string, delta float64) (*Node, error) {
	c := root.Clone()
	parent, idx := findParent(c, paneID)
	if parent == nil {
		return nil, fmt.Errorf("geometry: pane %q has no parent split to resize", paneID)
	}
	ratios := normRatios(parent)
	// The immediate sibling that absorbs the delta: the next child, or the
	// previous one for the last child, so a two-pane split always works.
	sib := idx + 1
	if sib >= len(ratios) {
		sib = idx - 1
	}
	newSelf := clampRatio(ratios[idx] + delta)
	applied := newSelf - ratios[idx]
	newSib := ratios[sib] - applied
	if newSib < MinRatio {
		newSib = MinRatio
		newSelf = ratios[idx] + (ratios[sib] - newSib)
	}
	ratios[idx] = newSelf
	ratios[sib] = newSib
	parent.Ratios = ratios
	return c, nil
}

func clampRatio(r float64) float64 {
	if r < MinRatio {
		return MinRatio
	}
	if r > 1-MinRatio {
		return 1 - MinRatio
	}
	return r
}

// findParent locates the split node that directly contains the leaf paneID and
// the child index of that leaf, or (nil, -1).
func findParent(n *Node, paneID string) (*Node, int) {
	if n == nil || n.IsLeaf() {
		return nil, -1
	}
	for i, c := range n.Children {
		if c.IsLeaf() && c.PaneID == paneID {
			return n, i
		}
	}
	for _, c := range n.Children {
		if p, i := findParent(c, paneID); p != nil {
			return p, i
		}
	}
	return nil, -1
}
