package domain

// Snapshot is the pure-data projection of a session graph that the persistence
// layer (internal/snapshot, ADR-0005 ComponentGraph) serializes and restores.
// It exists inside domain because the split tree is deliberately unexported
// (ADR-0002: identity is by opaque ID, never by structural position); only this
// package can walk it, so only this package can produce or consume a faithful
// copy. The DTO carries no behavior and no pointers into a live State.
//
// Rehydrate is the restore half: it consumes EXTERNAL data (a decoded snapshot
// generation) and therefore fails closed — the rebuilt graph is verified with
// the same Check() the property suite asserts, and any violation returns a
// typed error with no partial state (ADR-0005 "reject partial/corrupt
// generations").
type Snapshot struct {
	Session    SessionID           `json:"session"`
	Rev        uint64              `json:"rev"`
	Workspaces []SnapshotWorkspace `json:"workspaces"`
}

// SnapshotWorkspace mirrors Workspace: name, primary root, the full split
// tree, panes in deterministic tree (leaf) order, focus state, and revision.
type SnapshotWorkspace struct {
	ID           WorkspaceID    `json:"id"`
	Name         string         `json:"name,omitempty"`
	PrimaryRoot  string         `json:"primary_root,omitempty"`
	Root         *SnapshotNode  `json:"root"`
	Panes        []SnapshotPane `json:"panes"`
	Focused      PaneID         `json:"focused"`
	FocusHistory []PaneID       `json:"focus_history"`
	Rev          uint64         `json:"rev"`
}

// SnapshotNode is one split-tree node. Exactly one shape holds (the same
// leaf-XOR-split rule Check() enforces): a leaf has Pane != "" and nil
// children; a split has Pane == "", a valid orientation, an in-bounds ratio,
// and two non-nil children.
type SnapshotNode struct {
	Pane        PaneID           `json:"pane,omitempty"`
	Orientation SplitOrientation `json:"orientation,omitempty"`
	Ratio       float64          `json:"ratio,omitempty"`
	First       *SnapshotNode    `json:"first,omitempty"`
	Second      *SnapshotNode    `json:"second,omitempty"`
}

// SnapshotPane mirrors Pane: cwd, opaque project tag, ordered surfaces, and
// the single active surface.
type SnapshotPane struct {
	ID       PaneID            `json:"id"`
	Cwd      string            `json:"cwd,omitempty"`
	Project  ProjectID         `json:"project,omitempty"`
	Surfaces []SnapshotSurface `json:"surfaces"`
	Active   SurfaceID         `json:"active"`
}

// SnapshotSurface mirrors Surface identity + presentation metadata. Raw PTY
// bytes and cell grids are NOT part of the graph snapshot; they live in replay
// sidecars and are derived, respectively (ADR-0005 authority table).
type SnapshotSurface struct {
	ID    SurfaceID `json:"id"`
	Title string    `json:"title,omitempty"`
}

// Export produces a deep-copied Snapshot of the state. It never fails: a State
// reachable through Apply is valid by construction.
func (s *State) Export() *Snapshot {
	out := &Snapshot{Session: s.Session, Rev: s.Rev}
	for _, wid := range s.workspaceOrder {
		w := s.workspaces[wid]
		sw := SnapshotWorkspace{
			ID:           w.ID,
			Name:         w.Name,
			PrimaryRoot:  w.PrimaryRoot,
			Root:         exportNode(w.root),
			Focused:      w.focused,
			FocusHistory: append([]PaneID(nil), w.focusHistory...),
			Rev:          w.rev,
		}
		for _, pid := range w.PaneOrder() {
			p := w.panes[pid]
			sp := SnapshotPane{ID: p.ID, Cwd: p.Cwd, Project: p.Project, Active: p.active}
			for _, sf := range p.surfaces {
				sp.Surfaces = append(sp.Surfaces, SnapshotSurface{ID: sf.ID, Title: sf.Title})
			}
			sw.Panes = append(sw.Panes, sp)
		}
		out.Workspaces = append(out.Workspaces, sw)
	}
	return out
}

func exportNode(n *treeNode) *SnapshotNode {
	if n == nil {
		return nil
	}
	if n.isLeaf() {
		return &SnapshotNode{Pane: n.pane}
	}
	return &SnapshotNode{
		Orientation: n.orientation,
		Ratio:       n.ratio,
		First:       exportNode(n.first),
		Second:      exportNode(n.second),
	}
}

// Rehydrate rebuilds a State from a Snapshot, failing closed on any invariant
// violation. The returned State passes Check(); on error no partial state is
// returned. Revisions are restored exactly so events resume from the persisted
// revision line (ADR-0002 §Revisions).
func Rehydrate(snap *Snapshot) (*State, error) {
	if snap == nil {
		return nil, newError(CodeInvalidArgument, "nil snapshot")
	}
	s := NewState(snap.Session)
	s.Rev = snap.Rev
	for i := range snap.Workspaces {
		sw := &snap.Workspaces[i]
		if sw.ID == "" {
			return nil, newError(CodeInvalidArgument, "snapshot workspace with empty id")
		}
		if _, dup := s.workspaces[sw.ID]; dup {
			return nil, newError(CodeInvalidArgument, "duplicate workspace in snapshot: "+string(sw.ID))
		}
		root, err := rehydrateNode(sw.Root)
		if err != nil {
			return nil, err
		}
		w := &Workspace{
			ID:           sw.ID,
			Name:         sw.Name,
			PrimaryRoot:  sw.PrimaryRoot,
			root:         root,
			panes:        make(map[PaneID]*Pane, len(sw.Panes)),
			focused:      sw.Focused,
			focusHistory: append([]PaneID(nil), sw.FocusHistory...),
			rev:          sw.Rev,
		}
		for j := range sw.Panes {
			sp := &sw.Panes[j]
			if sp.ID == "" {
				return nil, newError(CodeInvalidArgument, "snapshot pane with empty id in workspace "+string(sw.ID))
			}
			if _, dup := w.panes[sp.ID]; dup {
				return nil, newError(CodeInvalidArgument, "duplicate pane in snapshot: "+string(sp.ID))
			}
			p := &Pane{ID: sp.ID, Cwd: sp.Cwd, Project: sp.Project, active: sp.Active}
			for _, sf := range sp.Surfaces {
				p.surfaces = append(p.surfaces, &Surface{ID: sf.ID, Title: sf.Title})
			}
			w.panes[sp.ID] = p
		}
		s.workspaces[sw.ID] = w
		s.workspaceOrder = append(s.workspaceOrder, sw.ID)
	}
	// One validation gate for everything else (tree shape, pane/leaf equality,
	// surface membership, focus permutation, ratio bounds): the frozen Check.
	// Its CodeInternal classification is for states domain itself produced;
	// here the input is external data, so the failure is reported as
	// invalid_argument with the underlying violation preserved.
	if err := s.Check(); err != nil {
		return nil, newError(CodeInvalidArgument, "snapshot fails graph invariants: "+err.Error())
	}
	return s, nil
}

func rehydrateNode(n *SnapshotNode) (*treeNode, error) {
	if n == nil {
		return nil, newError(CodeInvalidArgument, "nil split-tree node in snapshot")
	}
	if n.Pane != "" {
		if n.First != nil || n.Second != nil {
			return nil, newError(CodeInvalidArgument, "snapshot leaf "+string(n.Pane)+" has children")
		}
		return &treeNode{pane: n.Pane}, nil
	}
	first, err := rehydrateNode(n.First)
	if err != nil {
		return nil, err
	}
	second, err := rehydrateNode(n.Second)
	if err != nil {
		return nil, err
	}
	return &treeNode{
		orientation: n.Orientation,
		ratio:       n.Ratio,
		first:       first,
		second:      second,
	}, nil
}
