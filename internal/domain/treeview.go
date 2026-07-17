// treeview.go is the read-only split-tree projection (T4 contract completion,
// minor-1 workspace.tree). The binary split tree stays unexported and
// navigable only by ID (ADR-0002); TreeView is an immutable deep-copy VIEW of
// that single authoritative tree — never a second layout model — so an outer
// layer can project real geometry (orientation, nesting, ratios) without ever
// holding live node pointers.
package domain

// TreeView is one node of the split-tree projection. Exactly one shape holds,
// mirroring the internal invariant: a leaf carries Pane and no children; a
// split carries Orientation/Ratio and both children.
type TreeView struct {
	// Pane is the leaf's pane ID ("" on a split node).
	Pane PaneID
	// Orientation and Ratio are meaningful on split nodes only. Ratio is the
	// fraction of the axis allocated to First.
	Orientation SplitOrientation
	Ratio       float64
	First       *TreeView
	Second      *TreeView
}

// IsLeaf reports whether the node is a pane leaf.
func (v *TreeView) IsLeaf() bool { return v != nil && v.Pane != "" }

// Tree returns an immutable deep copy of the workspace's split tree. Mutating
// the returned view never affects the workspace (and cannot: every node is a
// fresh allocation).
func (w *Workspace) Tree() *TreeView {
	return treeView(w.root)
}

func treeView(n *treeNode) *TreeView {
	if n == nil {
		return nil
	}
	return &TreeView{
		Pane:        n.pane,
		Orientation: n.orientation,
		Ratio:       n.ratio,
		First:       treeView(n.first),
		Second:      treeView(n.second),
	}
}
