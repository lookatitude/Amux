package attachstate

import (
	"testing"

	"github.com/amux-run/amux/internal/tui/model"
)

func TestHappyPathConnectToLive(t *testing.T) {
	m := New()
	if m.State().Phase != model.PhaseIdle {
		t.Fatal("zero machine should be idle")
	}
	m.Connecting()
	m.Snapshot(100, nil, false)
	if m.State().Phase != model.PhaseReplaying {
		t.Fatalf("after snapshot want replaying, got %s", m.State().Phase)
	}
	m.Lease(model.LeaseOwned)
	s := m.ReplayComplete()
	if s.Phase != model.PhaseLive {
		t.Fatalf("owner should be live, got %s", s.Phase)
	}
	if !s.Lease.Writable() {
		t.Fatal("owner lease should be writable")
	}
}

func TestReadOnlyAttach(t *testing.T) {
	m := New()
	m.Connecting()
	m.Snapshot(50, nil, true)
	s := m.ReplayComplete()
	if s.Phase != model.PhaseReadOnly {
		t.Fatalf("read-only attach should be read_only, got %s", s.Phase)
	}
	if s.Lease.Writable() {
		t.Fatal("read-only must not be writable")
	}
}

func TestLeaseTakeoverDropsToReadOnly(t *testing.T) {
	m := New()
	m.Connecting()
	m.Snapshot(1, nil, false)
	m.Lease(model.LeaseOwned)
	m.ReplayComplete()
	// Another client takes over.
	s := m.Lease(model.LeaseLost)
	if s.Phase != model.PhaseReadOnly {
		t.Fatalf("after takeover want read_only, got %s", s.Phase)
	}
	if s.Lease != model.LeaseLost {
		t.Fatalf("lease should be lost, got %s", s.Lease)
	}
	// Reacquire → writable live again.
	s = m.Lease(model.LeaseOwned)
	if s.Phase != model.PhaseLive || !s.Lease.Writable() {
		t.Fatalf("reacquire should restore live+writable: %+v", s)
	}
}

func TestReplayGapIsVisibleBoundary(t *testing.T) {
	m := New()
	m.Connecting()
	gap := &GapInfo{RequestedFrom: 1, OldestRetained: 40, LatestSeq: 100}
	s := m.Snapshot(100, gap, false)
	if s.Gap == nil || s.Gap.OldestRetained != 40 {
		t.Fatalf("replay gap not recorded: %+v", s.Gap)
	}
}

func TestErrorRecoveryMapping(t *testing.T) {
	cases := []struct {
		kind  ErrKind
		phase model.AttachPhase
		rec   Recovery
	}{
		{ErrConnLost, model.PhaseDisconnected, RecRedial},
		{ErrBootChanged, model.PhaseDaemonRestarted, RecReSnapshot},
		{ErrEventGap, model.PhaseGapRecovery, RecReSnapshot},
		{ErrReplayGap, model.PhaseGapRecovery, RecReattach},
		{ErrSlowConsumer, model.PhaseSlowDetached, RecReattach},
	}
	for _, tc := range cases {
		m := New()
		m.Connecting()
		s := m.Error(tc.kind)
		if s.Phase != tc.phase || s.Recovery != tc.rec {
			t.Errorf("%v: got phase=%s rec=%s, want %s/%s", tc.kind, s.Phase, s.Recovery, tc.phase, tc.rec)
		}
		if !s.NeedsRecovery() {
			t.Errorf("%v: should need recovery", tc.kind)
		}
		if !s.Phase.Recoverable() {
			t.Errorf("%v: phase %s should be recoverable", tc.kind, s.Phase)
		}
	}
}

func TestRecoveredReturnsToConnecting(t *testing.T) {
	m := New()
	m.Connecting()
	m.Error(ErrEventGap)
	s := m.Recovered()
	if s.Phase != model.PhaseConnecting || s.NeedsRecovery() {
		t.Fatalf("after recovery want connecting/no-recovery, got %+v", s)
	}
}

func TestStoppedSurface(t *testing.T) {
	m := New()
	m.Connecting()
	m.Snapshot(1, nil, false)
	s := m.SurfaceStopped()
	if s.Phase != model.PhaseStopped {
		t.Fatalf("want stopped, got %s", s.Phase)
	}
}
