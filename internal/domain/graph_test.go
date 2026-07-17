package domain

import (
	"strings"
	"testing"
)

// mustApply applies cmd and fails the test on error.
func mustApply(t *testing.T, s *State, cmd Command, ids IDSource) (*State, []Event) {
	t.Helper()
	ns, ev, err := Apply(s, cmd, ids)
	if err != nil {
		t.Fatalf("Apply(%T) unexpected error: %v", cmd, err)
	}
	if err := ns.Check(); err != nil {
		t.Fatalf("Apply(%T) produced invalid state: %v", cmd, err)
	}
	return ns, ev
}

func firstWorkspace(t *testing.T, s *State) *Workspace {
	t.Helper()
	order := s.WorkspaceOrder()
	if len(order) == 0 {
		t.Fatal("no workspaces")
	}
	w, _ := s.Workspace(order[0])
	return w
}

func TestCreateWorkspace(t *testing.T) {
	ids := NewCountingSource()
	s := NewState("ses-1")
	s, ev := mustApply(t, s, CreateWorkspace{Name: "main", PrimaryRoot: "/repo", FirstPaneCwd: "/repo"}, ids)

	if len(s.WorkspaceOrder()) != 1 {
		t.Fatalf("want 1 workspace, got %d", len(s.WorkspaceOrder()))
	}
	w := firstWorkspace(t, s)
	if w.Name != "main" || w.PrimaryRoot != "/repo" {
		t.Fatalf("workspace metadata not preserved: %+v", w)
	}
	if len(w.PaneOrder()) != 1 {
		t.Fatalf("new workspace must have exactly one pane, got %d", len(w.PaneOrder()))
	}
	p, _ := w.Pane(w.Focused())
	if p == nil {
		t.Fatal("focused pane missing")
	}
	if p.Cwd != "/repo" {
		t.Fatalf("first pane cwd not seeded: %q", p.Cwd)
	}
	if len(p.Surfaces()) != 1 {
		t.Fatalf("first pane must have one surface, got %d", len(p.Surfaces()))
	}
	if p.ActiveSurface() != p.Surfaces()[0].ID {
		t.Fatal("first surface must be active")
	}
	if got := ev[0].(WorkspaceCreated); got.FirstPane != p.ID {
		t.Fatalf("event FirstPane mismatch: %v vs %v", got.FirstPane, p.ID)
	}
	if s.Rev != 1 {
		t.Fatalf("state rev must advance to 1, got %d", s.Rev)
	}
}

func TestSplitFocusesNewPaneAndCollapsesOnClose(t *testing.T) {
	ids := NewCountingSource()
	s := NewState("ses-1")
	s, _ = mustApply(t, s, CreateWorkspace{}, ids)
	w := firstWorkspace(t, s)
	target := w.Focused()

	s, ev := mustApply(t, s, SplitPane{Workspace: w.ID, Target: target, Orientation: SplitHorizontal}, ids)
	w = firstWorkspace(t, s)
	if len(w.PaneOrder()) != 2 {
		t.Fatalf("want 2 panes after split, got %d", len(w.PaneOrder()))
	}
	sp := ev[0].(PaneSplit)
	if w.Focused() != sp.NewPane {
		t.Fatalf("split must focus the new pane; focused=%v new=%v", w.Focused(), sp.NewPane)
	}
	if sp.Ratio != DefaultRatio {
		t.Fatalf("zero ratio must default to %v, got %v", DefaultRatio, sp.Ratio)
	}

	// Closing the new pane collapses the split; the original pane returns as root.
	s, _ = mustApply(t, s, ClosePane{Workspace: w.ID, Pane: sp.NewPane}, ids)
	w = firstWorkspace(t, s)
	order := w.PaneOrder()
	if len(order) != 1 || order[0] != target {
		t.Fatalf("collapse must leave original pane as sole root, got %v", order)
	}
	if w.Focused() != target {
		t.Fatalf("focus must fall back to the surviving pane, got %v", w.Focused())
	}
}

func TestCannotCloseLastPane(t *testing.T) {
	ids := NewCountingSource()
	s := NewState("ses-1")
	s, _ = mustApply(t, s, CreateWorkspace{}, ids)
	w := firstWorkspace(t, s)
	_, _, err := Apply(s, ClosePane{Workspace: w.ID, Pane: w.Focused()}, ids)
	if CodeOf(err) != CodeConflict {
		t.Fatalf("closing last pane must be a conflict, got %v", err)
	}
}

func TestSurfaceLifecycle(t *testing.T) {
	ids := NewCountingSource()
	s := NewState("ses-1")
	s, _ = mustApply(t, s, CreateWorkspace{}, ids)
	w := firstWorkspace(t, s)
	pane := w.Focused()
	s0 := func() Surface {
		p, _ := firstWorkspace(t, s).Pane(pane)
		return p.Surfaces()[0]
	}()

	// Spawn a second surface -> becomes active.
	s, ev := mustApply(t, s, SpawnSurface{Workspace: w.ID, Pane: pane, Title: "logs"}, ids)
	spawned := ev[0].(SurfaceSpawned)
	p, _ := firstWorkspace(t, s).Pane(pane)
	if p.ActiveSurface() != spawned.Surface {
		t.Fatal("spawned surface must become active")
	}
	if len(p.Surfaces()) != 2 {
		t.Fatalf("want 2 surfaces, got %d", len(p.Surfaces()))
	}

	// Switch active back to the first.
	s, _ = mustApply(t, s, SetActiveSurface{Workspace: w.ID, Pane: pane, Surface: s0.ID}, ids)
	p, _ = firstWorkspace(t, s).Pane(pane)
	if p.ActiveSurface() != s0.ID {
		t.Fatal("SetActiveSurface did not take")
	}

	// Close the first (active) surface -> active reassigned deterministically.
	s, ce := mustApply(t, s, CloseSurface{Workspace: w.ID, Pane: pane, Surface: s0.ID}, ids)
	closed := ce[0].(SurfaceClosed)
	p, _ = firstWorkspace(t, s).Pane(pane)
	if len(p.Surfaces()) != 1 {
		t.Fatalf("want 1 surface after close, got %d", len(p.Surfaces()))
	}
	if closed.NewActive != p.ActiveSurface() || p.ActiveSurface() != spawned.Surface {
		t.Fatalf("active must fall to surviving surface, got %v", p.ActiveSurface())
	}

	// Cannot close the last surface.
	_, _, err := Apply(s, CloseSurface{Workspace: w.ID, Pane: pane, Surface: spawned.Surface}, ids)
	if CodeOf(err) != CodeConflict {
		t.Fatalf("closing last surface must conflict, got %v", err)
	}
}

func TestTypedErrors(t *testing.T) {
	ids := NewCountingSource()
	s := NewState("ses-1")
	s, _ = mustApply(t, s, CreateWorkspace{}, ids)
	w := firstWorkspace(t, s)

	cases := []struct {
		name string
		cmd  Command
		want ErrorCode
	}{
		{"missing workspace", RenameWorkspace{Workspace: "nope", Name: "x"}, CodeNotFound},
		{"missing pane", FocusPane{Workspace: w.ID, Pane: "nope"}, CodeNotFound},
		{"bad orientation", SplitPane{Workspace: w.ID, Target: w.Focused(), Orientation: 0}, CodeInvalidArgument},
		{"resize root", ResizePane{Workspace: w.ID, Pane: w.Focused(), Ratio: 0.3}, CodeConflict},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ns, _, err := Apply(s, tc.cmd, ids)
			if CodeOf(err) != tc.want {
				t.Fatalf("want %v, got %v", tc.want, err)
			}
			// The original state must be returned untouched on error.
			if ns != s {
				t.Fatal("error path must return the original *State pointer untouched")
			}
		})
	}
}

func TestApplyDoesNotMutateInput(t *testing.T) {
	ids := NewCountingSource()
	s := NewState("ses-1")
	s, _ = mustApply(t, s, CreateWorkspace{}, ids)
	before := dump(s)

	// A successful command must not mutate the caller's prior State value.
	w := firstWorkspace(t, s)
	_, _, err := Apply(s, SplitPane{Workspace: w.ID, Target: w.Focused(), Orientation: SplitVertical}, ids)
	if err != nil {
		t.Fatal(err)
	}
	if after := dump(s); after != before {
		t.Fatalf("Apply mutated its input state\nbefore:\n%s\nafter:\n%s", before, after)
	}
}

func TestResizeAndEqualize(t *testing.T) {
	ids := NewCountingSource()
	s := NewState("ses-1")
	s, _ = mustApply(t, s, CreateWorkspace{}, ids)
	w := firstWorkspace(t, s)
	root := w.Focused()
	s, ev := mustApply(t, s, SplitPane{Workspace: w.ID, Target: root, Orientation: SplitHorizontal, Ratio: 0.7}, ids)
	newPane := ev[0].(PaneSplit).NewPane

	// Resize clamps out-of-range ratios.
	s, re := mustApply(t, s, ResizePane{Workspace: w.ID, Pane: newPane, Ratio: 0.999}, ids)
	if got := re[0].(PaneResized).Ratio; got != 1-MinRatio {
		t.Fatalf("resize must clamp to %v, got %v", 1-MinRatio, got)
	}
	// Equalize resets ratios; state stays valid.
	s, _ = mustApply(t, s, Equalize{Workspace: w.ID}, ids)
	if err := s.Check(); err != nil {
		t.Fatalf("equalize produced invalid state: %v", err)
	}
}

// dump renders the whole graph deterministically for equality assertions. It is
// the oracle the determinism/replay property test compares against.
func dump(s *State) string {
	var b strings.Builder
	b.WriteString("session=" + string(s.Session) + "\n")
	for _, wid := range s.WorkspaceOrder() {
		w, _ := s.Workspace(wid)
		b.WriteString("ws " + string(w.ID) + " name=" + w.Name + " root=" + w.PrimaryRoot + " rev=" + itoa(w.rev) + " focused=" + string(w.Focused()) + "\n")
		b.WriteString("  tree: ")
		dumpNode(&b, w.root)
		b.WriteString("\n  focus:")
		for _, id := range w.FocusHistory() {
			b.WriteString(" " + string(id))
		}
		b.WriteString("\n")
		for _, pid := range w.PaneOrder() {
			p, _ := w.Pane(pid)
			b.WriteString("  pane " + string(p.ID) + " cwd=" + p.Cwd + " active=" + string(p.ActiveSurface()) + " surfaces=")
			for _, sf := range p.Surfaces() {
				b.WriteString("[" + string(sf.ID) + ":" + sf.Title + "]")
			}
			b.WriteString("\n")
		}
	}
	return b.String()
}

func dumpNode(b *strings.Builder, n *treeNode) {
	if n == nil {
		b.WriteString("nil")
		return
	}
	if n.isLeaf() {
		b.WriteString(string(n.pane))
		return
	}
	b.WriteString("(" + n.orientation.String() + ":" + ftoa(n.ratio) + " ")
	dumpNode(b, n.first)
	b.WriteString(" ")
	dumpNode(b, n.second)
	b.WriteString(")")
}

func itoa(n uint64) string {
	if n == 0 {
		return "0"
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	return string(buf[i:])
}

func ftoa(f float64) string {
	// Fixed 2-decimal rendering; ratios are always in [0.05, 0.95].
	scaled := int(f*100 + 0.5)
	return itoa(uint64(scaled/100)) + "." + pad2(scaled%100)
}

func pad2(n int) string {
	if n < 10 {
		return "0" + itoa(uint64(n))
	}
	return itoa(uint64(n))
}
