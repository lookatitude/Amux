package domain

import (
	"math/rand"
	"testing"
)

// TestInvariantsHoldUnderRandomCommands drives long pseudo-random command
// sequences through Apply and asserts, after every accepted command, that the
// full ADR-0002 invariant set (State.Check) still holds. Rejected commands
// (typed errors) must never mutate state — verified structurally because Apply
// returns the original pointer on error. This is the property backbone the
// downstream replay and snapshot suites stand on.
func TestInvariantsHoldUnderRandomCommands(t *testing.T) {
	for seed := int64(1); seed <= 8; seed++ {
		seed := seed
		t.Run("seed="+itoa(uint64(seed)), func(t *testing.T) {
			s, _ := runSequence(t, seed, 600)
			if err := s.Check(); err != nil {
				t.Fatalf("final state invalid: %v", err)
			}
		})
	}
}

// TestDeterministicReplay proves the core determinism contract: re-applying the
// exact recorded command sequence from an empty state with a fresh (identical)
// IDSource yields a byte-identical graph. Because production IDs come from a
// deterministic CountingSource in tests, referenced IDs line up on replay — the
// same guarantee snapshot restore relies on.
func TestDeterministicReplay(t *testing.T) {
	for seed := int64(1); seed <= 8; seed++ {
		final, recorded := runSequence(t, seed, 600)
		want := dump(final)

		// Replay from scratch.
		ids := NewCountingSource()
		replay := NewState("ses-1")
		for _, cmd := range recorded {
			ns, _, err := Apply(replay, cmd, ids)
			if err != nil {
				t.Fatalf("seed %d: recorded command %T failed on replay: %v", seed, cmd, err)
			}
			replay = ns
		}
		if got := dump(replay); got != want {
			t.Fatalf("seed %d: replay diverged from original run\n--- want ---\n%s\n--- got ---\n%s", seed, want, got)
		}
	}
}

// TestGeneratorDeterminism sanity-checks the test oracle itself: same seed ->
// identical run, so a replay divergence indicts Apply, not the generator.
func TestGeneratorDeterminism(t *testing.T) {
	a, _ := runSequence(t, 42, 300)
	b, _ := runSequence(t, 42, 300)
	if dump(a) != dump(b) {
		t.Fatal("generator is non-deterministic; property tests would be unsound")
	}
}

// runSequence generates up to n pseudo-random commands from a fixed seed,
// applies each, asserts Check() after every accepted command, and returns the
// final state plus the list of accepted commands (for replay). Rejected
// commands are dropped, not recorded.
func runSequence(t *testing.T, seed int64, n int) (*State, []Command) {
	t.Helper()
	rng := rand.New(rand.NewSource(seed))
	ids := NewCountingSource()
	s := NewState("ses-1")
	var recorded []Command

	for i := 0; i < n; i++ {
		cmd := genCommand(rng, s)
		if cmd == nil {
			continue
		}
		ns, _, err := Apply(s, cmd, ids)
		if err != nil {
			// Typed rejection: state must be untouched (same pointer).
			if ns != s {
				t.Fatalf("rejected %T returned a new state pointer", cmd)
			}
			// Only certain codes are legitimate; an internal code is a bug.
			if CodeOf(err) == CodeInternal {
				t.Fatalf("command %T produced internal error: %v", cmd, err)
			}
			continue
		}
		if err := ns.Check(); err != nil {
			t.Fatalf("accepted %T produced invalid state: %v", cmd, err)
		}
		s = ns
		recorded = append(recorded, cmd)
	}
	return s, recorded
}

// genCommand picks a random command biased toward keeping a non-trivial graph
// alive. It references existing entities where possible so most commands are
// accepted; it deliberately also emits some invalid references to exercise the
// typed-error paths.
func genCommand(rng *rand.Rand, s *State) Command {
	wsIDs := s.WorkspaceOrder()
	// Early on, or with some probability, create a workspace.
	if len(wsIDs) == 0 || rng.Intn(12) == 0 {
		return CreateWorkspace{
			Name:         "w" + itoa(uint64(rng.Intn(1000))),
			PrimaryRoot:  pickRoot(rng),
			FirstPaneCwd: pickRoot(rng),
		}
	}
	w, _ := s.Workspace(wsIDs[rng.Intn(len(wsIDs))])
	panes := w.PaneOrder()
	pane := panes[rng.Intn(len(panes))]
	p, _ := w.Pane(pane)
	surfaces := p.Surfaces()

	switch rng.Intn(11) {
	case 0:
		return SplitPane{Workspace: w.ID, Target: pane, Orientation: pickOrientation(rng), Ratio: pickRatio(rng)}
	case 1:
		return FocusPane{Workspace: w.ID, Pane: pane}
	case 2:
		return ResizePane{Workspace: w.ID, Pane: pane, Ratio: pickRatio(rng)}
	case 3:
		return Equalize{Workspace: w.ID}
	case 4:
		return SpawnSurface{Workspace: w.ID, Pane: pane, Title: "s" + itoa(uint64(rng.Intn(1000)))}
	case 5:
		return SetActiveSurface{Workspace: w.ID, Pane: pane, Surface: surfaces[rng.Intn(len(surfaces))].ID}
	case 6:
		return CloseSurface{Workspace: w.ID, Pane: pane, Surface: surfaces[rng.Intn(len(surfaces))].ID}
	case 7:
		return ClosePane{Workspace: w.ID, Pane: pane}
	case 8:
		return RenameWorkspace{Workspace: w.ID, Name: "r" + itoa(uint64(rng.Intn(1000)))}
	case 9:
		// Deliberately invalid reference (exercises not_found path).
		return FocusPane{Workspace: w.ID, Pane: "ghost-pane"}
	default:
		// Occasionally close a whole workspace (keeps at least one alive often
		// because CreateWorkspace fires frequently).
		if len(wsIDs) > 1 {
			return CloseWorkspace{Workspace: w.ID}
		}
		return SpawnSurface{Workspace: w.ID, Pane: pane, Title: "keep"}
	}
}

func pickOrientation(rng *rand.Rand) SplitOrientation {
	if rng.Intn(2) == 0 {
		return SplitHorizontal
	}
	return SplitVertical
}

func pickRatio(rng *rand.Rand) float64 {
	// Range spans below/above the clamp bounds so clamping is exercised.
	return rng.Float64() * 1.1
}

func pickRoot(rng *rand.Rand) string {
	roots := []string{"", "/a", "/b/c", "/repo/x"}
	return roots[rng.Intn(len(roots))]
}
