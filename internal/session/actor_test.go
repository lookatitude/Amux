package session

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/amux-run/amux/internal/domain"
	"github.com/amux-run/amux/internal/platform"
)

func newActor(t *testing.T, ring int) *Actor {
	t.Helper()
	a := New(Config{
		ID:         "ses-test",
		IDs:        domain.NewCountingSource(),
		Clock:      platform.NewFakeClock(1_000),
		RingEvents: ring,
	})
	a.Start()
	t.Cleanup(a.Stop)
	return a
}

func mustCreateWorkspace(t *testing.T, a *Actor, name string) (domain.WorkspaceID, domain.PaneID) {
	t.Helper()
	res, err := a.Submit(context.Background(), domain.CreateWorkspace{Name: name})
	if err != nil {
		t.Fatalf("CreateWorkspace: %v", err)
	}
	ev := res.Events[0].Payload.(domain.WorkspaceCreated)
	return ev.Workspace, ev.FirstPane
}

// A sequence is allocated only after a command commits: 16 goroutines × 50
// submissions with a rejected command mixed in still yield a strictly
// contiguous 1..N committed log (the real-actor version of the ordering
// model's proof; run under -race).
func TestEventSequencesMonotonicContiguousUnderConcurrency(t *testing.T) {
	a := newActor(t, 1<<16)
	ctx := context.Background()
	ws, _ := mustCreateWorkspace(t, a, "w")

	var wg sync.WaitGroup
	for g := 0; g < 16; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for i := 0; i < 50; i++ {
				if i%5 == 0 {
					// Invalid: unknown workspace -> typed not_found, no seq.
					_, err := a.Submit(ctx, domain.RenameWorkspace{Workspace: "nope", Name: "x"})
					if domain.CodeOf(err) != domain.CodeNotFound {
						t.Errorf("expected typed not_found, got %v", err)
					}
					continue
				}
				if _, err := a.Submit(ctx, domain.RenameWorkspace{Workspace: ws, Name: "n"}); err != nil {
					t.Errorf("Submit: %v", err)
				}
			}
		}(g)
	}
	wg.Wait()

	latest, err := a.LatestSeq(ctx)
	if err != nil {
		t.Fatal(err)
	}
	events, err := a.Replay(ctx, 1)
	if err != nil {
		t.Fatalf("Replay: %v", err)
	}
	if uint64(len(events)) != latest {
		t.Fatalf("retained %d events, latest seq %d", len(events), latest)
	}
	for i, e := range events {
		if e.Seq != uint64(i+1) {
			t.Fatalf("gap: events[%d].Seq = %d", i, e.Seq)
		}
	}
}

func TestReplayGapIsTyped(t *testing.T) {
	a := newActor(t, 4) // tiny ring forces eviction
	ctx := context.Background()
	ws, _ := mustCreateWorkspace(t, a, "w")
	for i := 0; i < 10; i++ {
		if _, err := a.Submit(ctx, domain.RenameWorkspace{Workspace: ws, Name: "n"}); err != nil {
			t.Fatal(err)
		}
	}
	_, err := a.Replay(ctx, 1)
	var gap *EventGapError
	if !errors.As(err, &gap) {
		t.Fatalf("expected EventGapError, got %v", err)
	}
	if gap.OldestRetained == 0 || gap.Latest == 0 || gap.Requested != 1 {
		t.Fatalf("gap receipt incomplete: %+v", gap)
	}
	// Recovery: snapshot + fresh cursor resumes cleanly (snapshot-on-gap).
	snap, cursor, err := a.Snapshot(ctx)
	if err != nil || snap == nil {
		t.Fatalf("Snapshot: %v", err)
	}
	if cursor != gap.Latest {
		t.Fatalf("snapshot cursor %d != latest %d", cursor, gap.Latest)
	}
	if evs, err := a.Replay(ctx, cursor+1); err != nil || len(evs) != 0 {
		t.Fatalf("replay from fresh cursor: %v %v", evs, err)
	}
}

// Subscribing from a past cursor splices ring replay and live delivery
// atomically: a concurrent writer cannot create a gap or duplicate at the
// boundary.
func TestSubscribeSplicesReplayAndLive(t *testing.T) {
	a := newActor(t, 1<<16)
	ctx := context.Background()
	ws, _ := mustCreateWorkspace(t, a, "w")
	for i := 0; i < 20; i++ {
		if _, err := a.Submit(ctx, domain.RenameWorkspace{Workspace: ws, Name: "n"}); err != nil {
			t.Fatal(err)
		}
	}

	stop := make(chan struct{})
	writerDone := make(chan struct{})
	go func() {
		defer close(writerDone)
		for {
			select {
			case <-stop:
				return
			default:
				if _, err := a.Submit(ctx, domain.RenameWorkspace{Workspace: ws, Name: "live"}); err != nil {
					t.Errorf("Submit: %v", err)
					return
				}
			}
		}
	}()

	sub, err := a.Subscribe(ctx, SubscribeOptions{FromSeq: 1, Buffer: 1 << 15})
	if err != nil {
		t.Fatalf("Subscribe: %v", err)
	}
	var last uint64
	for i := 0; i < 200; i++ {
		e, ok := <-sub.C()
		if !ok {
			t.Fatalf("subscription closed early: %v", sub.Err())
		}
		if e.Seq != last+1 {
			t.Fatalf("boundary violation: got seq %d after %d", e.Seq, last)
		}
		last = e.Seq
	}
	close(stop)
	<-writerDone
	sub.Cancel()
}

func TestSlowConsumerDisconnectReceipt(t *testing.T) {
	a := newActor(t, 64)
	ctx := context.Background()
	ws, _ := mustCreateWorkspace(t, a, "w")

	sub, err := a.Subscribe(ctx, SubscribeOptions{Buffer: 2})
	if err != nil {
		t.Fatal(err)
	}
	// Never read: the third undelivered event overflows the bounded buffer.
	for i := 0; i < 5; i++ {
		if _, err := a.Submit(ctx, domain.RenameWorkspace{Workspace: ws, Name: "n"}); err != nil {
			t.Fatal(err)
		}
	}
	// Drain what was buffered; the channel must be closed afterwards.
	n := 0
	for range sub.C() {
		n++
	}
	if n == 0 || n > 2 {
		t.Fatalf("buffered deliveries = %d, want 1..2", n)
	}
	if !errors.Is(sub.Err(), ErrSlowConsumer) {
		t.Fatalf("Err() = %v, want ErrSlowConsumer", sub.Err())
	}
	if sub.LastDelivered() == 0 {
		t.Fatal("disconnect receipt lacks the last delivered sequence")
	}
	// The actor itself stays healthy.
	if _, err := a.Submit(ctx, domain.RenameWorkspace{Workspace: ws, Name: "after"}); err != nil {
		t.Fatalf("actor wedged after slow-consumer disconnect: %v", err)
	}
}

func TestSubscribeFilterByWorkspace(t *testing.T) {
	a := newActor(t, 1024)
	ctx := context.Background()
	ws1, _ := mustCreateWorkspace(t, a, "one")
	ws2, _ := mustCreateWorkspace(t, a, "two")

	sub, err := a.Subscribe(ctx, SubscribeOptions{Buffer: 16, Filter: FilterWorkspace(ws2)})
	if err != nil {
		t.Fatal(err)
	}
	defer sub.Cancel()
	if _, err := a.Submit(ctx, domain.RenameWorkspace{Workspace: ws1, Name: "x"}); err != nil {
		t.Fatal(err)
	}
	if _, err := a.Submit(ctx, domain.RenameWorkspace{Workspace: ws2, Name: "y"}); err != nil {
		t.Fatal(err)
	}
	e := <-sub.C()
	if e.Workspace != ws2 || e.Kind != "workspace_renamed" {
		t.Fatalf("filter delivered %+v", e)
	}
}

func TestSnapshotRestoreResumesCursor(t *testing.T) {
	a := newActor(t, 1024)
	ctx := context.Background()
	ws, pane := mustCreateWorkspace(t, a, "w")
	if _, err := a.Submit(ctx, domain.SplitPane{Workspace: ws, Target: pane, Orientation: domain.SplitVertical}); err != nil {
		t.Fatal(err)
	}
	snap, cursor, err := a.Snapshot(ctx)
	if err != nil {
		t.Fatal(err)
	}

	b := New(Config{ID: snap.Session, IDs: domain.NewCountingSource(), Clock: platform.NewFakeClock(2_000)})
	b.Start()
	t.Cleanup(b.Stop)
	if err := b.Restore(ctx, snap, cursor); err != nil {
		t.Fatalf("Restore: %v", err)
	}
	st, err := b.State(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(st.WorkspaceOrder()) != 1 {
		t.Fatalf("restored workspaces: %v", st.WorkspaceOrder())
	}
	res, err := b.Submit(ctx, domain.RenameWorkspace{Workspace: ws, Name: "after-restore"})
	if err != nil {
		t.Fatal(err)
	}
	if res.Events[0].Seq != cursor+1 {
		t.Fatalf("post-restore seq = %d, want %d", res.Events[0].Seq, cursor+1)
	}
	// Restore refuses to run over a non-empty actor (no silent state merge).
	if err := b.Restore(ctx, snap, cursor); err == nil {
		t.Fatal("Restore over live state must fail closed")
	}
}

// Injected drop: a consumer that loses events (simulated by resuming from a
// cursor beyond the retained window after ring eviction) must hit the typed
// gap and recover via snapshot + fresh cursor — never silently bridge.
func TestInjectedDropForcesSnapshotRecovery(t *testing.T) {
	a := newActor(t, 8)
	ctx := context.Background()
	ws, _ := mustCreateWorkspace(t, a, "w")
	for i := 0; i < 32; i++ {
		if _, err := a.Submit(ctx, domain.RenameWorkspace{Workspace: ws, Name: "n"}); err != nil {
			t.Fatal(err)
		}
	}
	// Client remembered cursor 3; the ring has long evicted it.
	_, err := a.Subscribe(ctx, SubscribeOptions{FromSeq: 3, Buffer: 64})
	var gap *EventGapError
	if !errors.As(err, &gap) {
		t.Fatalf("expected typed event gap on stale cursor subscribe, got %v", err)
	}
	snap, cursor, err := a.Snapshot(ctx)
	if err != nil || snap == nil {
		t.Fatal(err)
	}
	sub, err := a.Subscribe(ctx, SubscribeOptions{FromSeq: cursor + 1, Buffer: 64})
	if err != nil {
		t.Fatalf("post-snapshot subscribe: %v", err)
	}
	defer sub.Cancel()
	res, err := a.Submit(ctx, domain.RenameWorkspace{Workspace: ws, Name: "recovered"})
	if err != nil {
		t.Fatal(err)
	}
	e := <-sub.C()
	if e.Seq != res.Events[0].Seq || e.Seq != cursor+1 {
		t.Fatalf("recovered stream starts at %d, want %d", e.Seq, cursor+1)
	}
}

func TestSubmitReturnsTypedDomainErrors(t *testing.T) {
	a := newActor(t, 64)
	ctx := context.Background()
	ws, pane := mustCreateWorkspace(t, a, "w")
	_, err := a.Submit(ctx, domain.ClosePane{Workspace: ws, Pane: pane})
	if domain.CodeOf(err) != domain.CodeConflict {
		t.Fatalf("closing the only pane: %v", err)
	}
	if latest, _ := a.LatestSeq(ctx); latest != 1 {
		t.Fatalf("rejected command allocated a sequence: latest=%d", latest)
	}
}
