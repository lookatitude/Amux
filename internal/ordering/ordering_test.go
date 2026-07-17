package ordering

import (
	"sync"
	"testing"
)

// TestEventSequenceMonotonicContiguous proves that a session actor allocates
// sequences only on commit and that, even under concurrent submission, the
// committed log is strictly monotonic and gap-free (ADR-0004). Rejected commands
// allocate nothing, so they never create a gap.
func TestEventSequenceMonotonicContiguous(t *testing.T) {
	s := NewSessionActor()
	go s.Run()

	const goroutines, each = 16, 50
	var wg sync.WaitGroup
	var committed int64
	var mu sync.Mutex
	for g := 0; g < goroutines; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for i := 0; i < each; i++ {
				// Reject every 5th command to prove rejects leave no gap.
				commit := (i%5 != 0)
				resp := s.Submit(commit)
				if commit {
					if !resp.committed || resp.seq == 0 {
						t.Errorf("committed submit returned seq 0")
					}
					mu.Lock()
					committed++
					mu.Unlock()
				} else if resp.seq != 0 {
					t.Errorf("rejected submit allocated sequence %d", resp.seq)
				}
			}
		}(g)
	}
	wg.Wait()
	s.Stop()

	log := s.Log()
	if int64(len(log)) != committed {
		t.Fatalf("log length %d != committed count %d", len(log), committed)
	}
	for i, seq := range log {
		if seq != uint64(i+1) {
			t.Fatalf("sequence gap/dup at index %d: got %d, want %d", i, seq, i+1)
		}
	}
}

// TestRevokeFirstCreatesNoChild is the deterministic revoke-first ordering: once
// trust is revoked, a launch authorization at the pre-revoke epoch fails and
// zero children are created (spec "Linearizable hook launch contract").
func TestRevokeFirstCreatesNoChild(t *testing.T) {
	const p ProjectID = "proj-A"
	c := NewControlActor(p)
	go c.Run()
	defer c.Stop()

	newEpoch := c.Revoke(p) // revoke linearizes first
	if newEpoch != 2 {
		t.Fatalf("revoke must bump epoch 1->2, got %d", newEpoch)
	}
	authorized, _ := c.Authorize(p, 1) // stale epoch, post-revoke
	if authorized {
		t.Fatal("launch authorized after revoke; must be denied")
	}
	if c.Children(p) != 0 {
		t.Fatalf("revoke-first must create zero children, got %d", c.Children(p))
	}
}

// TestLaunchFirstThenRevoke is the launch-first ordering: authorization succeeds
// (one child), then a later revoke bumps the epoch so any subsequent launch —
// even at the correct new epoch, since trust is cleared — is denied. The real
// runtime drives terminate/kill for the in-flight child; the model proves the
// linearization boundary.
func TestLaunchFirstThenRevoke(t *testing.T) {
	const p ProjectID = "proj-B"
	c := NewControlActor(p)
	go c.Run()
	defer c.Stop()

	authorized, epoch := c.Authorize(p, 1) // launch linearizes first
	if !authorized || epoch != 1 {
		t.Fatalf("pre-revoke launch must be authorized at epoch 1, got auth=%v epoch=%d", authorized, epoch)
	}
	if c.Children(p) != 1 {
		t.Fatalf("launch-first must create exactly one child, got %d", c.Children(p))
	}
	newEpoch := c.Revoke(p)
	if newEpoch != 2 {
		t.Fatalf("revoke must bump epoch to 2, got %d", newEpoch)
	}
	if authorized, _ := c.Authorize(p, 2); authorized {
		t.Fatal("post-revoke launch at new epoch must still be denied (trust cleared)")
	}
	if c.Children(p) != 1 {
		t.Fatalf("no further child may be created after revoke, got %d", c.Children(p))
	}
}

// TestTwoSessionsShareProjectNoPostRevokeLaunch stress-races two sessions
// launching against one shared project while a revoke fires, under -race. It
// proves (a) no data race in the control actor, (b) the audit tally is
// consistent with the authorize replies, and (c) after the revoke completes, no
// stale-epoch launch can succeed — the "no launch linearizes after revocation"
// guarantee.
func TestTwoSessionsShareProjectNoPostRevokeLaunch(t *testing.T) {
	const p ProjectID = "shared"
	c := NewControlActor(p)
	go c.Run()
	defer c.Stop()

	const attemptsPerSession = 200
	var wg sync.WaitGroup
	var mu sync.Mutex
	trueCount := 0

	launch := func() {
		defer wg.Done()
		for i := 0; i < attemptsPerSession; i++ {
			if ok, _ := c.Authorize(p, 1); ok {
				mu.Lock()
				trueCount++
				mu.Unlock()
			}
		}
	}
	wg.Add(2)
	go launch() // session 1
	go launch() // session 2

	// Revoke somewhere in the middle of the storm.
	c.Revoke(p)
	wg.Wait()

	// Audit consistency: every authorized reply corresponds to exactly one child.
	if c.Children(p) != trueCount {
		t.Fatalf("audit child count %d != authorized replies %d", c.Children(p), trueCount)
	}
	// After the storm and the completed revoke, a stale-epoch launch must fail.
	if ok, _ := c.Authorize(p, 1); ok {
		t.Fatal("stale-epoch launch succeeded after revoke completed")
	}
}

func TestLeaseStateMachine(t *testing.T) {
	var s LeaseState
	const a, b ClientID = "a", "b"

	// Non-holder write rejected while free.
	if ok, ev := s.WriteAllowed(a); ok || ev != InputRejected {
		t.Fatal("write on free lease must be rejected")
	}
	// Acquire.
	s, ev, ok := s.Acquire(a)
	if !ok || ev != LeaseAcquired {
		t.Fatal("acquire on free lease must succeed")
	}
	if ok, _ := s.WriteAllowed(a); !ok {
		t.Fatal("holder must be allowed to write")
	}
	if ok, ev := s.WriteAllowed(b); ok || ev != InputRejected {
		t.Fatal("non-holder write must be rejected")
	}
	// Second client cannot implicitly acquire.
	if _, _, ok := s.Acquire(b); ok {
		t.Fatal("acquire on held lease by another client must fail (needs takeover)")
	}
	// Deliberate takeover is evented.
	s, ev = s.Takeover(b)
	if ev != LeaseTakenOver {
		t.Fatal("takeover must emit takeover event")
	}
	if h, held := s.Holder(); !held || h != b {
		t.Fatal("takeover must transfer holder to b")
	}
	// Disconnect releases the lease.
	s, ev = s.Disconnect(b)
	if ev != LeaseReleased {
		t.Fatal("disconnect by holder must release")
	}
	if _, held := s.Holder(); held {
		t.Fatal("lease must be free after disconnect")
	}
}

func TestAttachCutoverContiguous(t *testing.T) {
	// Replay 5..8 ending at snapshot boundary 8, then live 9..11.
	out, err := AttachCutover(8, []uint64{5, 6, 7, 8}, []uint64{9, 10, 11})
	if err != nil {
		t.Fatalf("contiguous cutover must succeed: %v", err)
	}
	want := []uint64{5, 6, 7, 8, 9, 10, 11}
	if len(out) != len(want) {
		t.Fatalf("cutover length mismatch: %v", out)
	}
	for i := range want {
		if out[i] != want[i] {
			t.Fatalf("cutover order mismatch at %d: %v", i, out)
		}
	}
}

func TestAttachCutoverDetectsGaps(t *testing.T) {
	// Live starts at 10 but boundary is 8 -> missing 9: a gap.
	if _, err := AttachCutover(8, []uint64{7, 8}, []uint64{10, 11}); err != ErrGap {
		t.Fatalf("missing sequence must report ErrGap, got %v", err)
	}
	// Replay does not reach the snapshot boundary.
	if _, err := AttachCutover(9, []uint64{7, 8}, []uint64{10}); err != ErrGap {
		t.Fatalf("replay short of boundary must report ErrGap, got %v", err)
	}
	// Duplicate at boundary (live re-delivers 8).
	if _, err := AttachCutover(8, []uint64{7, 8}, []uint64{8, 9}); err != ErrGap {
		t.Fatalf("duplicate live frame must report ErrGap, got %v", err)
	}
}
