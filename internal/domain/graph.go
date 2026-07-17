package domain

// This file freezes the Amux object model that ADR-0002 governs:
//
//	session -> workspaces -> split-tree panes -> ordered surfaces
//
// State is the graph a single per-session event-loop actor owns (ADR-0001). The
// daemon-global control actor owns the session *registry* (which sessions
// exist, their project trust, epochs); this package models only the graph a
// session actor mutates. Every value here is deterministic and free of
// transport, persistence, PTY, terminal, TUI, or provider concerns — the
// dependency-rule test in internal/archtest enforces that inbound-only import
// direction.

// SplitOrientation names how a split node arranges its two children. The names
// describe the *arrangement of panes*, not the divider: Horizontal lays panes
// out left-to-right (a vertical divider between them); Vertical stacks panes
// top-to-bottom (a horizontal divider). ADR-0002 freezes this vocabulary so the
// CLI, protocol, and TUI never disagree about what "split horizontal" means.
type SplitOrientation uint8

const (
	// SplitHorizontal arranges the two children side by side (first = left).
	SplitHorizontal SplitOrientation = iota + 1
	// SplitVertical stacks the two children (first = top).
	SplitVertical
)

func (o SplitOrientation) valid() bool {
	return o == SplitHorizontal || o == SplitVertical
}

func (o SplitOrientation) String() string {
	switch o {
	case SplitHorizontal:
		return "horizontal"
	case SplitVertical:
		return "vertical"
	default:
		return "invalid"
	}
}

// Ratio bounds. A split's ratio is the fraction of space given to its first
// child; it is clamped into [MinRatio, 1-MinRatio] so no pane can be resized to
// a zero or negative rectangle. Geometry (pixel/cell allocation) is a pure
// function the TUI owns (U2); the domain only guarantees the ratio invariant.
const (
	MinRatio     = 0.05
	DefaultRatio = 0.5
)

func clampRatio(r float64) float64 {
	if r < MinRatio {
		return MinRatio
	}
	if r > 1-MinRatio {
		return 1 - MinRatio
	}
	return r
}

// treeNode is one node of a workspace's binary split tree. Exactly one of two
// shapes holds, enforced by invariant checks:
//
//   - leaf: pane != "" and first == nil && second == nil
//   - split: pane == "" and first != nil && second != nil, orientation valid
//
// The tree is unexported; callers navigate the graph through PaneID lookups and
// the accessor methods below, never by holding node pointers (ADR-0002:
// identity is by opaque ID, not by structural position).
type treeNode struct {
	pane        PaneID
	orientation SplitOrientation
	ratio       float64
	first       *treeNode
	second      *treeNode
}

func (n *treeNode) isLeaf() bool { return n.pane != "" }

func (n *treeNode) clone() *treeNode {
	if n == nil {
		return nil
	}
	c := *n
	c.first = n.first.clone()
	c.second = n.second.clone()
	return &c
}

// leaves appends every pane ID in deterministic left-to-right tree order.
func (n *treeNode) leaves(out *[]PaneID) {
	if n == nil {
		return
	}
	if n.isLeaf() {
		*out = append(*out, n.pane)
		return
	}
	n.first.leaves(out)
	n.second.leaves(out)
}

// findParent returns the split node whose child (first or second) is the leaf
// carrying target, plus which side. Returns (nil, false) if target is the root
// leaf or absent.
func (n *treeNode) findParent(target PaneID) (parent *treeNode, isFirst bool, found bool) {
	if n == nil || n.isLeaf() {
		return nil, false, false
	}
	if n.first.isLeaf() && n.first.pane == target {
		return n, true, true
	}
	if n.second.isLeaf() && n.second.pane == target {
		return n, false, true
	}
	if p, f, ok := n.first.findParent(target); ok {
		return p, f, true
	}
	return n.second.findParent(target)
}

// equalize sets every split ratio to DefaultRatio, producing a balanced tree.
func (n *treeNode) equalize() {
	if n == nil || n.isLeaf() {
		return
	}
	n.ratio = DefaultRatio
	n.first.equalize()
	n.second.equalize()
}

// Surface is one ordered terminal surface inside a pane. The domain owns only
// identity and presentation metadata; raw PTY bytes and cell grids live in the
// terminal engine, never here (ADR-0002 / ADR-0006).
type Surface struct {
	ID    SurfaceID
	Title string
}

func (s *Surface) clone() *Surface { c := *s; return &c }

// Pane is a leaf of the split tree. It owns an independent cwd, an optional
// project association (the opaque trust tag; the control actor computes it), and
// an ordered, non-empty list of surfaces exactly one of which is active.
type Pane struct {
	ID       PaneID
	Cwd      string
	Project  ProjectID // "" = no project association yet
	surfaces []*Surface
	active   SurfaceID
}

// Surfaces returns a copy of the pane's ordered surfaces for read-only callers.
func (p *Pane) Surfaces() []Surface {
	out := make([]Surface, len(p.surfaces))
	for i, s := range p.surfaces {
		out[i] = *s
	}
	return out
}

// ActiveSurface returns the pane's single active surface ID.
func (p *Pane) ActiveSurface() SurfaceID { return p.active }

func (p *Pane) clone() *Pane {
	c := *p
	c.surfaces = make([]*Surface, len(p.surfaces))
	for i, s := range p.surfaces {
		c.surfaces[i] = s.clone()
	}
	return &c
}

func (p *Pane) surface(id SurfaceID) (*Surface, int) {
	for i, s := range p.surfaces {
		if s.ID == id {
			return s, i
		}
	}
	return nil, -1
}

// Workspace is a named split tree of panes with a single focused pane and a
// full recency-ordered focus history (most-recent last). It optionally records
// a single primary project root; hook cwd scope `workspace-primary` resolves
// only against that root (spec trust boundary).
type Workspace struct {
	ID           WorkspaceID
	Name         string
	PrimaryRoot  string
	root         *treeNode
	panes        map[PaneID]*Pane
	focused      PaneID
	focusHistory []PaneID
	rev          uint64
}

// Rev returns the workspace's monotonic revision, bumped once per committed
// mutation that touches it.
func (w *Workspace) Rev() uint64 { return w.rev }

// Focused returns the currently focused pane ID.
func (w *Workspace) Focused() PaneID { return w.focused }

// Pane returns the pane with the given ID, or (nil, false).
func (w *Workspace) Pane(id PaneID) (*Pane, bool) {
	p, ok := w.panes[id]
	return p, ok
}

// PaneOrder returns pane IDs in deterministic left-to-right tree order.
func (w *Workspace) PaneOrder() []PaneID {
	var out []PaneID
	w.root.leaves(&out)
	return out
}

// FocusHistory returns a copy of the recency-ordered focus history.
func (w *Workspace) FocusHistory() []PaneID {
	return append([]PaneID(nil), w.focusHistory...)
}

func (w *Workspace) clone() *Workspace {
	c := *w
	c.root = w.root.clone()
	c.panes = make(map[PaneID]*Pane, len(w.panes))
	for id, p := range w.panes {
		c.panes[id] = p.clone()
	}
	c.focusHistory = append([]PaneID(nil), w.focusHistory...)
	return &c
}

// touchFocus moves pane to the most-recent position of the focus history and
// sets it focused. pane must already be a member of the workspace.
func (w *Workspace) touchFocus(pane PaneID) {
	filtered := w.focusHistory[:0:0]
	for _, id := range w.focusHistory {
		if id != pane {
			filtered = append(filtered, id)
		}
	}
	w.focusHistory = append(filtered, pane)
	w.focused = pane
}

// State is the whole graph one session actor owns. It is a value type in spirit:
// Apply never mutates a State the caller still holds; it clones, mutates the
// clone, and returns it (ADR-0001 determinism rule).
type State struct {
	Session        SessionID
	Rev            uint64
	workspaces     map[WorkspaceID]*Workspace
	workspaceOrder []WorkspaceID
}

// NewState returns the empty graph for a session (no workspaces yet).
func NewState(session SessionID) *State {
	return &State{
		Session:    session,
		workspaces: map[WorkspaceID]*Workspace{},
	}
}

// Workspace returns the workspace with the given ID, or (nil, false).
func (s *State) Workspace(id WorkspaceID) (*Workspace, bool) {
	w, ok := s.workspaces[id]
	return w, ok
}

// WorkspaceOrder returns workspace IDs in stable creation order.
func (s *State) WorkspaceOrder() []WorkspaceID {
	return append([]WorkspaceID(nil), s.workspaceOrder...)
}

func (s *State) clone() *State {
	c := *s
	c.workspaces = make(map[WorkspaceID]*Workspace, len(s.workspaces))
	for id, w := range s.workspaces {
		c.workspaces[id] = w.clone()
	}
	c.workspaceOrder = append([]WorkspaceID(nil), s.workspaceOrder...)
	return &c
}

// Check verifies every ADR-0002 invariant across the whole graph. It is the
// single source of truth for what "a valid graph" means and is asserted after
// every command in the property suite. Production actors trust their own
// transitions; Check exists so tests can prove those transitions never produce
// an invalid State. It returns a *Error (code invalid_argument) describing the
// first violation, or nil.
func (s *State) Check() error {
	// Deterministic workspace iteration for stable error messages.
	if len(s.workspaceOrder) != len(s.workspaces) {
		return newError(CodeInternal, "workspaceOrder length != workspaces map length")
	}
	seenWs := map[WorkspaceID]bool{}
	for _, id := range s.workspaceOrder {
		if seenWs[id] {
			return newError(CodeInternal, "duplicate workspace in order: "+string(id))
		}
		seenWs[id] = true
		w, ok := s.workspaces[id]
		if !ok {
			return newError(CodeInternal, "workspaceOrder references missing workspace: "+string(id))
		}
		if err := w.check(); err != nil {
			return err
		}
	}
	return nil
}

func (w *Workspace) check() error {
	if w.root == nil {
		return newError(CodeInternal, "workspace "+string(w.ID)+" has nil root (empty workspaces are not allowed)")
	}
	// Collect leaves and validate tree shape.
	leafSet := map[PaneID]bool{}
	if err := checkNode(w.root, leafSet, w.ID); err != nil {
		return err
	}
	// Panes map must equal the leaf set exactly.
	if len(leafSet) != len(w.panes) {
		return newError(CodeInternal, "workspace "+string(w.ID)+": pane map size != tree leaf count")
	}
	for id, p := range w.panes {
		if !leafSet[id] {
			return newError(CodeInternal, "pane "+string(id)+" in map but not in tree")
		}
		if p.ID != id {
			return newError(CodeInternal, "pane map key != pane.ID for "+string(id))
		}
		if len(p.surfaces) == 0 {
			return newError(CodeInternal, "pane "+string(id)+" has zero surfaces")
		}
		if _, idx := p.surface(p.active); idx < 0 {
			return newError(CodeInternal, "pane "+string(id)+" active surface not in its surface list")
		}
		seenSurf := map[SurfaceID]bool{}
		for _, sf := range p.surfaces {
			if seenSurf[sf.ID] {
				return newError(CodeInternal, "duplicate surface "+string(sf.ID)+" in pane "+string(id))
			}
			seenSurf[sf.ID] = true
		}
	}
	// Focus: focused is the most-recent history entry; history is a permutation
	// of the pane set.
	if len(w.focusHistory) != len(w.panes) {
		return newError(CodeInternal, "workspace "+string(w.ID)+": focus history length != pane count")
	}
	seenFocus := map[PaneID]bool{}
	for _, id := range w.focusHistory {
		if seenFocus[id] {
			return newError(CodeInternal, "duplicate pane in focus history: "+string(id))
		}
		seenFocus[id] = true
		if !leafSet[id] {
			return newError(CodeInternal, "focus history references non-existent pane: "+string(id))
		}
	}
	if w.focused != w.focusHistory[len(w.focusHistory)-1] {
		return newError(CodeInternal, "workspace "+string(w.ID)+": focused pane != most-recent focus history entry")
	}
	return nil
}

func checkNode(n *treeNode, leafSet map[PaneID]bool, ws WorkspaceID) error {
	if n == nil {
		return newError(CodeInternal, "nil tree node in workspace "+string(ws))
	}
	if n.isLeaf() {
		if n.first != nil || n.second != nil {
			return newError(CodeInternal, "leaf pane "+string(n.pane)+" has children")
		}
		if leafSet[n.pane] {
			return newError(CodeInternal, "duplicate pane in tree: "+string(n.pane))
		}
		leafSet[n.pane] = true
		return nil
	}
	// split node
	if n.first == nil || n.second == nil {
		return newError(CodeInternal, "split node with a nil child in workspace "+string(ws))
	}
	if !n.orientation.valid() {
		return newError(CodeInternal, "split node with invalid orientation in workspace "+string(ws))
	}
	if n.ratio < MinRatio || n.ratio > 1-MinRatio {
		return newError(CodeInternal, "split ratio out of bounds in workspace "+string(ws))
	}
	if err := checkNode(n.first, leafSet, ws); err != nil {
		return err
	}
	return checkNode(n.second, leafSet, ws)
}
