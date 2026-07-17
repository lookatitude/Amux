package domain

import (
	"reflect"
	"testing"
)

// buildSampleState drives Apply through a representative command sequence so
// snapshot tests exercise splits, focus history, surfaces, and ratios rather
// than a trivial single-pane graph.
func buildSampleState(t *testing.T) *State {
	t.Helper()
	ids := NewCountingSource()
	s := NewState(ids.NextSession())
	var err error
	apply := func(cmd Command) {
		t.Helper()
		s, _, err = Apply(s, cmd, ids)
		if err != nil {
			t.Fatalf("Apply(%T): %v", cmd, err)
		}
	}
	apply(CreateWorkspace{Name: "alpha", PrimaryRoot: "/repo/alpha", FirstPaneCwd: "/repo/alpha"})
	wsID := s.WorkspaceOrder()[0]
	ws, _ := s.Workspace(wsID)
	first := ws.PaneOrder()[0]
	apply(SplitPane{Workspace: wsID, Target: first, Orientation: SplitHorizontal, Ratio: 0.3, NewPaneCwd: "/repo/alpha/sub"})
	ws, _ = s.Workspace(wsID)
	second := ws.PaneOrder()[1]
	apply(SplitPane{Workspace: wsID, Target: second, Orientation: SplitVertical, NewPaneCwd: "/tmp"})
	apply(SpawnSurface{Workspace: wsID, Pane: first, Title: "editor"})
	apply(FocusPane{Workspace: wsID, Pane: first})
	apply(CreateWorkspace{Name: "beta", FirstPaneCwd: "/repo/beta"})
	return s
}

func TestExportRehydrateRoundTrip(t *testing.T) {
	s := buildSampleState(t)
	snap := s.Export()

	restored, err := Rehydrate(snap)
	if err != nil {
		t.Fatalf("Rehydrate: %v", err)
	}
	if err := restored.Check(); err != nil {
		t.Fatalf("restored state fails invariants: %v", err)
	}
	// Round-trip equality is asserted on the exported form, which is a pure
	// data projection of the graph.
	if !reflect.DeepEqual(snap, restored.Export()) {
		t.Fatalf("export -> rehydrate -> export is not identity:\nfirst:  %#v\nsecond: %#v", snap, restored.Export())
	}
}

func TestExportIsDeepCopy(t *testing.T) {
	s := buildSampleState(t)
	snap := s.Export()
	// Mutating the snapshot must not reach the live state.
	snap.Workspaces[0].Name = "tampered"
	snap.Workspaces[0].Panes[0].Cwd = "/tampered"
	ws, _ := s.Workspace(snap.Workspaces[0].ID)
	if ws.Name == "tampered" {
		t.Fatal("Export shares workspace memory with live state")
	}
	p, _ := ws.Pane(snap.Workspaces[0].Panes[0].ID)
	if p.Cwd == "/tampered" {
		t.Fatal("Export shares pane memory with live state")
	}
}

// Rehydrate consumes external (persisted) data and must fail closed on any
// invariant violation rather than constructing a corrupt graph (ADR-0005
// "reject partial/corrupt generations").
func TestRehydrateRejectsCorruptSnapshots(t *testing.T) {
	base := func() *Snapshot { return buildSampleState(t).Export() }

	cases := []struct {
		name   string
		mutate func(*Snapshot)
	}{
		{"nil snapshot handled", nil},
		{"orphan pane in map", func(s *Snapshot) {
			w := &s.Workspaces[0]
			w.Panes = append(w.Panes, SnapshotPane{ID: "ghost", Cwd: "/", Surfaces: []SnapshotSurface{{ID: "gs"}}, Active: "gs"})
		}},
		{"ratio out of bounds", func(s *Snapshot) {
			s.Workspaces[0].Root.Ratio = 0.001
		}},
		{"focused not last in history", func(s *Snapshot) {
			w := &s.Workspaces[0]
			w.Focused = w.FocusHistory[0]
		}},
		{"active surface not a member", func(s *Snapshot) {
			s.Workspaces[0].Panes[0].Active = "nope"
		}},
		{"duplicate workspace id", func(s *Snapshot) {
			s.Workspaces = append(s.Workspaces, s.Workspaces[0])
		}},
		{"pane with zero surfaces", func(s *Snapshot) {
			s.Workspaces[0].Panes[0].Surfaces = nil
		}},
		{"split with nil child", func(s *Snapshot) {
			s.Workspaces[0].Root.Second = nil
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var snap *Snapshot
			if tc.mutate != nil {
				snap = base()
				tc.mutate(snap)
			}
			if _, err := Rehydrate(snap); err == nil {
				t.Fatal("Rehydrate accepted a corrupt snapshot; must fail closed")
			}
		})
	}
}
