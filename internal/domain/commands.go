package domain

// Commands are immutable inputs (ADR-0002). A command names *what the caller
// wants*; it carries no pointers into State and is safe to log, replay, and hand
// across the actor boundary. Apply is the single entry point: it validates the
// whole command against the current State first and, only if valid, mutates a
// clone and returns it. On any error the caller's State is returned untouched
// with a typed *Error and no events — there is never a partial transition
// (PRD F1: "commit exactly one deterministic state transition or return a typed
// error without partial state").
//
// Determinism: given the same prior State, the same Command, and an IDSource
// that yields the same IDs, Apply produces byte-identical results. That is the
// property the replay and snapshot suites rely on.
type Command interface {
	isCommand()
}

// CreateWorkspace creates a workspace containing exactly one pane with one
// surface. Name may be empty; PrimaryRoot is optional. FirstPaneCwd seeds the
// initial pane's cwd.
type CreateWorkspace struct {
	Name         string
	PrimaryRoot  string
	FirstPaneCwd string
}

// RenameWorkspace changes a workspace's display name.
type RenameWorkspace struct {
	Workspace WorkspaceID
	Name      string
}

// CloseWorkspace removes a workspace and its entire subtree. This is the only
// way to remove the last pane of a workspace.
type CloseWorkspace struct {
	Workspace WorkspaceID
}

// SplitPane splits Target into a split node whose first child is the original
// pane and whose second child is a new pane with its own first surface. Ratio is
// the first child's fraction (clamped); zero means DefaultRatio. The new pane
// becomes focused.
type SplitPane struct {
	Workspace   WorkspaceID
	Target      PaneID
	Orientation SplitOrientation
	Ratio       float64
	NewPaneCwd  string
}

// ClosePane removes a leaf pane and collapses its parent split so the sibling
// takes the parent's place. Closing the workspace's only pane is a conflict; use
// CloseWorkspace instead.
type ClosePane struct {
	Workspace WorkspaceID
	Pane      PaneID
}

// FocusPane makes Pane the focused pane and most-recent focus-history entry.
type FocusPane struct {
	Workspace WorkspaceID
	Pane      PaneID
}

// ResizePane sets the ratio of Pane's parent split. Ratio is interpreted as the
// fraction for the parent split's *first* child (clamped). Resizing the root
// pane (no parent split) is a conflict.
type ResizePane struct {
	Workspace WorkspaceID
	Pane      PaneID
	Ratio     float64
}

// Equalize resets every split ratio in a workspace to a balanced value.
type Equalize struct {
	Workspace WorkspaceID
}

// SpawnSurface appends a new surface to a pane and makes it active.
type SpawnSurface struct {
	Workspace WorkspaceID
	Pane      PaneID
	Title     string
}

// SetActiveSurface changes which existing surface of a pane is active.
type SetActiveSurface struct {
	Workspace WorkspaceID
	Pane      PaneID
	Surface   SurfaceID
}

// CloseSurface removes a surface from a pane. Removing the pane's last surface
// is a conflict (a pane always has at least one surface). If the closed surface
// was active, the previous surface in order becomes active (or the next, if it
// was first).
type CloseSurface struct {
	Workspace WorkspaceID
	Pane      PaneID
	Surface   SurfaceID
}

func (CreateWorkspace) isCommand()  {}
func (RenameWorkspace) isCommand()  {}
func (CloseWorkspace) isCommand()   {}
func (SplitPane) isCommand()        {}
func (ClosePane) isCommand()        {}
func (FocusPane) isCommand()        {}
func (ResizePane) isCommand()       {}
func (Equalize) isCommand()         {}
func (SpawnSurface) isCommand()     {}
func (SetActiveSurface) isCommand() {}
func (CloseSurface) isCommand()     {}

// Apply is the sole state-transition function. See the Command doc.
func Apply(s *State, cmd Command, ids IDSource) (*State, []Event, error) {
	if s == nil {
		return nil, nil, newError(CodeInvalidArgument, "nil state")
	}
	if ids == nil {
		return s, nil, newError(CodeInvalidArgument, "nil IDSource")
	}
	next := s.clone()
	var events []Event
	var err error
	switch c := cmd.(type) {
	case CreateWorkspace:
		events, err = applyCreateWorkspace(next, c, ids)
	case RenameWorkspace:
		events, err = applyRenameWorkspace(next, c)
	case CloseWorkspace:
		events, err = applyCloseWorkspace(next, c)
	case SplitPane:
		events, err = applySplitPane(next, c, ids)
	case ClosePane:
		events, err = applyClosePane(next, c)
	case FocusPane:
		events, err = applyFocusPane(next, c)
	case ResizePane:
		events, err = applyResizePane(next, c)
	case Equalize:
		events, err = applyEqualize(next, c)
	case SpawnSurface:
		events, err = applySpawnSurface(next, c, ids)
	case SetActiveSurface:
		events, err = applySetActiveSurface(next, c)
	case CloseSurface:
		events, err = applyCloseSurface(next, c)
	default:
		return s, nil, newError(CodeInvalidArgument, "unknown command type")
	}
	if err != nil {
		// Discard the clone; the caller keeps the untouched original.
		return s, nil, err
	}
	next.Rev++
	return next, events, nil
}

// lookup helpers ------------------------------------------------------------

func (s *State) mustWorkspace(id WorkspaceID) (*Workspace, error) {
	w, ok := s.workspaces[id]
	if !ok {
		return nil, newError(CodeNotFound, "workspace not found: "+string(id))
	}
	return w, nil
}

// command handlers ----------------------------------------------------------

func applyCreateWorkspace(s *State, c CreateWorkspace, ids IDSource) ([]Event, error) {
	wid := ids.NextWorkspace()
	pid := ids.NextPane()
	sid := ids.NextSurface()
	pane := &Pane{
		ID:       pid,
		Cwd:      c.FirstPaneCwd,
		surfaces: []*Surface{{ID: sid, Title: ""}},
		active:   sid,
	}
	w := &Workspace{
		ID:           wid,
		Name:         c.Name,
		PrimaryRoot:  c.PrimaryRoot,
		root:         &treeNode{pane: pid},
		panes:        map[PaneID]*Pane{pid: pane},
		focused:      pid,
		focusHistory: []PaneID{pid},
		rev:          1,
	}
	s.workspaces[wid] = w
	s.workspaceOrder = append(s.workspaceOrder, wid)
	return []Event{WorkspaceCreated{
		baseEvent:    baseEvent{Workspace: wid, Rev: w.rev},
		Name:         c.Name,
		PrimaryRoot:  c.PrimaryRoot,
		FirstPane:    pid,
		FirstSurface: sid,
	}}, nil
}

func applyRenameWorkspace(s *State, c RenameWorkspace) ([]Event, error) {
	w, err := s.mustWorkspace(c.Workspace)
	if err != nil {
		return nil, err
	}
	w.Name = c.Name
	w.rev++
	return []Event{WorkspaceRenamed{baseEvent: baseEvent{Workspace: w.ID, Rev: w.rev}, Name: c.Name}}, nil
}

func applyCloseWorkspace(s *State, c CloseWorkspace) ([]Event, error) {
	w, err := s.mustWorkspace(c.Workspace)
	if err != nil {
		return nil, err
	}
	rev := w.rev + 1
	delete(s.workspaces, c.Workspace)
	filtered := s.workspaceOrder[:0]
	for _, id := range s.workspaceOrder {
		if id != c.Workspace {
			filtered = append(filtered, id)
		}
	}
	s.workspaceOrder = filtered
	return []Event{WorkspaceClosed{baseEvent: baseEvent{Workspace: c.Workspace, Rev: rev}}}, nil
}

func applySplitPane(s *State, c SplitPane, ids IDSource) ([]Event, error) {
	w, err := s.mustWorkspace(c.Workspace)
	if err != nil {
		return nil, err
	}
	if !c.Orientation.valid() {
		return nil, newError(CodeInvalidArgument, "invalid split orientation")
	}
	if _, ok := w.panes[c.Target]; !ok {
		return nil, newError(CodeNotFound, "target pane not found: "+string(c.Target))
	}
	ratio := c.Ratio
	if ratio == 0 {
		ratio = DefaultRatio
	}
	ratio = clampRatio(ratio)

	npid := ids.NextPane()
	nsid := ids.NextSurface()
	newPane := &Pane{
		ID:       npid,
		Cwd:      c.NewPaneCwd,
		surfaces: []*Surface{{ID: nsid}},
		active:   nsid,
	}
	// Rewrite the target leaf into a split node in place.
	replaced := replaceLeaf(&w.root, c.Target, func(leaf *treeNode) *treeNode {
		return &treeNode{
			orientation: c.Orientation,
			ratio:       ratio,
			first:       &treeNode{pane: c.Target},
			second:      &treeNode{pane: npid},
		}
	})
	if !replaced {
		return nil, newError(CodeInternal, "target pane present in map but not in tree: "+string(c.Target))
	}
	w.panes[npid] = newPane
	w.touchFocus(npid)
	w.rev++
	return []Event{PaneSplit{
		baseEvent:   baseEvent{Workspace: w.ID, Rev: w.rev},
		Target:      c.Target,
		Orientation: c.Orientation,
		Ratio:       ratio,
		NewPane:     npid,
		NewSurface:  nsid,
	}}, nil
}

func applyClosePane(s *State, c ClosePane) ([]Event, error) {
	w, err := s.mustWorkspace(c.Workspace)
	if err != nil {
		return nil, err
	}
	if _, ok := w.panes[c.Pane]; !ok {
		return nil, newError(CodeNotFound, "pane not found: "+string(c.Pane))
	}
	if len(w.panes) == 1 {
		return nil, newError(CodeConflict, "cannot close the only pane of a workspace; close the workspace instead")
	}
	// Collapse: the pane's sibling replaces the parent split.
	parent, isFirst, found := w.root.findParent(c.Pane)
	if !found {
		return nil, newError(CodeInternal, "non-root pane has no parent split: "+string(c.Pane))
	}
	sibling := parent.second
	if !isFirst {
		sibling = parent.first
	}
	*parent = *sibling
	delete(w.panes, c.Pane)
	// Drop from focus history; refocus most-recent survivor.
	filtered := w.focusHistory[:0]
	for _, id := range w.focusHistory {
		if id != c.Pane {
			filtered = append(filtered, id)
		}
	}
	w.focusHistory = filtered
	w.focused = w.focusHistory[len(w.focusHistory)-1]
	w.rev++
	return []Event{PaneClosed{baseEvent: baseEvent{Workspace: w.ID, Rev: w.rev}, Pane: c.Pane}}, nil
}

func applyFocusPane(s *State, c FocusPane) ([]Event, error) {
	w, err := s.mustWorkspace(c.Workspace)
	if err != nil {
		return nil, err
	}
	if _, ok := w.panes[c.Pane]; !ok {
		return nil, newError(CodeNotFound, "pane not found: "+string(c.Pane))
	}
	if w.focused == c.Pane {
		// Idempotent focus still bumps recency but produces a consistent event.
		w.touchFocus(c.Pane)
		w.rev++
		return []Event{PaneFocused{baseEvent: baseEvent{Workspace: w.ID, Rev: w.rev}, Pane: c.Pane}}, nil
	}
	w.touchFocus(c.Pane)
	w.rev++
	return []Event{PaneFocused{baseEvent: baseEvent{Workspace: w.ID, Rev: w.rev}, Pane: c.Pane}}, nil
}

func applyResizePane(s *State, c ResizePane) ([]Event, error) {
	w, err := s.mustWorkspace(c.Workspace)
	if err != nil {
		return nil, err
	}
	if _, ok := w.panes[c.Pane]; !ok {
		return nil, newError(CodeNotFound, "pane not found: "+string(c.Pane))
	}
	parent, _, found := w.root.findParent(c.Pane)
	if !found {
		return nil, newError(CodeConflict, "cannot resize the root pane; it has no parent split")
	}
	ratio := clampRatio(c.Ratio)
	parent.ratio = ratio
	w.rev++
	return []Event{PaneResized{baseEvent: baseEvent{Workspace: w.ID, Rev: w.rev}, Pane: c.Pane, Ratio: ratio}}, nil
}

func applyEqualize(s *State, c Equalize) ([]Event, error) {
	w, err := s.mustWorkspace(c.Workspace)
	if err != nil {
		return nil, err
	}
	w.root.equalize()
	w.rev++
	return []Event{WorkspaceEqualized{baseEvent: baseEvent{Workspace: w.ID, Rev: w.rev}}}, nil
}

func applySpawnSurface(s *State, c SpawnSurface, ids IDSource) ([]Event, error) {
	w, err := s.mustWorkspace(c.Workspace)
	if err != nil {
		return nil, err
	}
	p, ok := w.panes[c.Pane]
	if !ok {
		return nil, newError(CodeNotFound, "pane not found: "+string(c.Pane))
	}
	sid := ids.NextSurface()
	p.surfaces = append(p.surfaces, &Surface{ID: sid, Title: c.Title})
	p.active = sid
	w.rev++
	return []Event{SurfaceSpawned{baseEvent: baseEvent{Workspace: w.ID, Rev: w.rev}, Pane: c.Pane, Surface: sid, Title: c.Title}}, nil
}

func applySetActiveSurface(s *State, c SetActiveSurface) ([]Event, error) {
	w, err := s.mustWorkspace(c.Workspace)
	if err != nil {
		return nil, err
	}
	p, ok := w.panes[c.Pane]
	if !ok {
		return nil, newError(CodeNotFound, "pane not found: "+string(c.Pane))
	}
	if _, idx := p.surface(c.Surface); idx < 0 {
		return nil, newError(CodeNotFound, "surface not found in pane: "+string(c.Surface))
	}
	p.active = c.Surface
	w.rev++
	return []Event{ActiveSurfaceChanged{baseEvent: baseEvent{Workspace: w.ID, Rev: w.rev}, Pane: c.Pane, Surface: c.Surface}}, nil
}

func applyCloseSurface(s *State, c CloseSurface) ([]Event, error) {
	w, err := s.mustWorkspace(c.Workspace)
	if err != nil {
		return nil, err
	}
	p, ok := w.panes[c.Pane]
	if !ok {
		return nil, newError(CodeNotFound, "pane not found: "+string(c.Pane))
	}
	_, idx := p.surface(c.Surface)
	if idx < 0 {
		return nil, newError(CodeNotFound, "surface not found in pane: "+string(c.Surface))
	}
	if len(p.surfaces) == 1 {
		return nil, newError(CodeConflict, "cannot close the last surface of a pane")
	}
	wasActive := p.active == c.Surface
	p.surfaces = append(p.surfaces[:idx], p.surfaces[idx+1:]...)
	if wasActive {
		// Prefer the previous surface in order; if the removed one was first,
		// take the new first. Deterministic.
		newIdx := idx - 1
		if newIdx < 0 {
			newIdx = 0
		}
		p.active = p.surfaces[newIdx].ID
	}
	w.rev++
	return []Event{SurfaceClosed{
		baseEvent: baseEvent{Workspace: w.ID, Rev: w.rev},
		Pane:      c.Pane,
		Surface:   c.Surface,
		NewActive: p.active,
	}}, nil
}

// replaceLeaf walks the tree rooted at *root, finds the leaf carrying target,
// and replaces that node in place using make(). Returns true if replaced.
func replaceLeaf(root **treeNode, target PaneID, make func(*treeNode) *treeNode) bool {
	n := *root
	if n == nil {
		return false
	}
	if n.isLeaf() {
		if n.pane == target {
			*root = make(n)
			return true
		}
		return false
	}
	if replaceLeaf(&n.first, target, make) {
		return true
	}
	return replaceLeaf(&n.second, target, make)
}
