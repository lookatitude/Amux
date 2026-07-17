package domain

import "testing"

// buildSplitWorkspace makes ws with three panes: root split horizontal
// (ratio from SplitPane), whose second child is split vertical.
func buildSplitWorkspace(t *testing.T) (*State, *Workspace) {
	t.Helper()
	ids := NewCountingSource()
	s := NewState("ses-tree")
	s, _ = mustApply(t, s, CreateWorkspace{Name: "main", FirstPaneCwd: "/repo"}, ids)
	w := firstWorkspace(t, s)
	first := w.Focused()
	s, _ = mustApply(t, s, SplitPane{Workspace: w.ID, Target: first, Orientation: SplitHorizontal, Ratio: 0.6}, ids)
	w = firstWorkspace(t, s)
	second := w.PaneOrder()[1]
	s, _ = mustApply(t, s, SplitPane{Workspace: w.ID, Target: second, Orientation: SplitVertical, Ratio: 0.3}, ids)
	return s, firstWorkspace(t, s)
}

// TestTreeViewProjectsAuthoritativeTree proves Tree() reproduces the exact
// split structure: orientations, ratios, nesting, and the same left-to-right
// leaf order PaneOrder reports (one authority, one projection).
func TestTreeViewProjectsAuthoritativeTree(t *testing.T) {
	_, w := buildSplitWorkspace(t)
	v := w.Tree()
	if v == nil || v.IsLeaf() {
		t.Fatalf("root must be a split, got %+v", v)
	}
	if v.Orientation != SplitHorizontal || v.Ratio != 0.6 {
		t.Fatalf("root split wrong: orient=%v ratio=%v", v.Orientation, v.Ratio)
	}
	if !v.First.IsLeaf() {
		t.Fatalf("first child must be the original pane leaf")
	}
	if v.Second.IsLeaf() || v.Second.Orientation != SplitVertical || v.Second.Ratio != 0.3 {
		t.Fatalf("second child must be the vertical split: %+v", v.Second)
	}
	var leaves []PaneID
	var walk func(n *TreeView)
	walk = func(n *TreeView) {
		if n == nil {
			return
		}
		if n.IsLeaf() {
			leaves = append(leaves, n.Pane)
			return
		}
		walk(n.First)
		walk(n.Second)
	}
	walk(v)
	order := w.PaneOrder()
	if len(leaves) != len(order) {
		t.Fatalf("leaf count %d != pane order %d", len(leaves), len(order))
	}
	for i := range order {
		if leaves[i] != order[i] {
			t.Fatalf("leaf order diverges at %d: %v vs %v", i, leaves, order)
		}
	}
}

// TestTreeViewIsImmutableCopy proves mutating the view cannot reach the
// workspace's authoritative tree.
func TestTreeViewIsImmutableCopy(t *testing.T) {
	_, w := buildSplitWorkspace(t)
	v := w.Tree()
	v.Ratio = 0.99
	v.First = nil
	v.Second = nil
	fresh := w.Tree()
	if fresh.Ratio != 0.6 || fresh.First == nil || fresh.Second == nil {
		t.Fatal("mutating a TreeView leaked into the workspace tree")
	}
	if err := w.check(); err != nil {
		t.Fatalf("workspace invalidated by view mutation: %v", err)
	}
}
