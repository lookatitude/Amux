package context

import (
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/amux-run/amux/internal/platform"
)

// waitFor is the tiny watchdog for asynchronous worker completion; no
// production timing depends on it.
func waitFor(t *testing.T, ch <-chan struct{}, what string) {
	t.Helper()
	select {
	case <-ch:
	case <-time.After(5 * time.Second):
		t.Fatalf("timed out waiting for %s", what)
	}
}

func TestPollerDebounceTwoPollsInsideIntervalRunOnce(t *testing.T) {
	clock := platform.NewFakeClock(0)
	var collects atomic.Int64
	done := make(chan struct{}, 16)
	p, err := NewPoller(PollerConfig{
		Clock:       clock,
		MinInterval: 100 * time.Millisecond,
		Workers:     1,
		Collect: func(pane string) (PaneContext, error) {
			collects.Add(1)
			return PaneContext{Pane: pane}, nil
		},
		Emit: func(PaneContext) { done <- struct{}{} },
	})
	if err != nil {
		t.Fatalf("new poller: %v", err)
	}
	defer p.Close()

	if !p.Request("p1") {
		t.Fatal("first request must be accepted")
	}
	waitFor(t, done, "first collection")

	if p.Request("p1") {
		t.Fatal("second request inside the debounce interval must be dropped")
	}
	if got := p.Stats().Debounced; got != 1 {
		t.Fatalf("debounced = %d, want 1", got)
	}

	clock.Advance(150 * time.Millisecond)
	if !p.Request("p1") {
		t.Fatal("request after the interval must be accepted")
	}
	waitFor(t, done, "second collection")
	if got := collects.Load(); got != 2 {
		t.Fatalf("collect executions = %d, want 2 (two polls inside interval ran once)", got)
	}
}

func TestPollerDebounceIsPerKey(t *testing.T) {
	clock := platform.NewFakeClock(0)
	done := make(chan struct{}, 16)
	p, err := NewPoller(PollerConfig{
		Clock:       clock,
		MinInterval: time.Minute,
		Collect:     func(pane string) (PaneContext, error) { return PaneContext{Pane: pane}, nil },
		Emit:        func(PaneContext) { done <- struct{}{} },
	})
	if err != nil {
		t.Fatalf("new poller: %v", err)
	}
	defer p.Close()

	if !p.Request("p1") || !p.Request("p2") {
		t.Fatal("distinct panes must debounce independently")
	}
	waitFor(t, done, "p1 collection")
	waitFor(t, done, "p2 collection")
}

func TestPollerBoundedConcurrencyAndOverflowShedding(t *testing.T) {
	clock := platform.NewFakeClock(0)
	started := make(chan string, 8)
	gate := make(chan struct{})
	var emitted sync.WaitGroup
	p, err := NewPoller(PollerConfig{
		Clock:      clock,
		Workers:    1,
		QueueDepth: 1,
		Collect: func(pane string) (PaneContext, error) {
			started <- pane
			<-gate // hold the single worker busy
			return PaneContext{Pane: pane}, nil
		},
		Emit: func(PaneContext) { emitted.Done() },
	})
	if err != nil {
		t.Fatalf("new poller: %v", err)
	}

	emitted.Add(2)
	if !p.Request("p1") {
		t.Fatal("p1 must be accepted")
	}
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("worker never started p1")
	}
	// Worker is busy with p1 and the queue holds one slot: p2 queues, p3 sheds.
	if !p.Request("p2") {
		t.Fatal("p2 must be queued")
	}
	if p.Request("p3") {
		t.Fatal("p3 must be shed: queue full, request must not block")
	}
	if got := p.Stats().Overflow; got != 1 {
		t.Fatalf("overflow = %d, want 1", got)
	}
	close(gate)
	emitted.Wait()
	p.Close()
}

func TestPollerEmitRunsOffTheCallerGoroutine(t *testing.T) {
	clock := platform.NewFakeClock(0)
	gate := make(chan struct{})
	done := make(chan struct{}, 1)
	var emittedBeforeReturn atomic.Bool
	p, err := NewPoller(PollerConfig{
		Clock: clock,
		Collect: func(pane string) (PaneContext, error) {
			<-gate
			return PaneContext{Pane: pane}, nil
		},
		Emit: func(PaneContext) {
			emittedBeforeReturn.Store(true)
			done <- struct{}{}
		},
	})
	if err != nil {
		t.Fatalf("new poller: %v", err)
	}
	defer p.Close()

	if !p.Request("p1") {
		t.Fatal("request must be accepted")
	}
	// Request returned while collect is still gated: the callback cannot have
	// run on this goroutine.
	if emittedBeforeReturn.Load() {
		t.Fatal("emit ran synchronously inside Request")
	}
	close(gate)
	waitFor(t, done, "async emit")
}

func TestPollerCollectErrorIsCountedNotEmitted(t *testing.T) {
	clock := platform.NewFakeClock(0)
	var emits atomic.Int64
	p, err := NewPoller(PollerConfig{
		Clock:   clock,
		Collect: func(string) (PaneContext, error) { return PaneContext{}, errors.New("probe failed") },
		Emit:    func(PaneContext) { emits.Add(1) },
	})
	if err != nil {
		t.Fatalf("new poller: %v", err)
	}
	p.Request("p1")
	p.Close() // drains the queue and joins the workers
	if emits.Load() != 0 {
		t.Fatalf("emit fired despite collect error")
	}
	if got := p.Stats().Errors; got != 1 {
		t.Fatalf("errors = %d, want 1", got)
	}
}

func TestPollerCloseRejectsFurtherRequests(t *testing.T) {
	clock := platform.NewFakeClock(0)
	p, err := NewPoller(PollerConfig{
		Clock:   clock,
		Collect: func(pane string) (PaneContext, error) { return PaneContext{Pane: pane}, nil },
		Emit:    func(PaneContext) {},
	})
	if err != nil {
		t.Fatalf("new poller: %v", err)
	}
	p.Close()
	p.Close() // idempotent
	if p.Request("p1") {
		t.Fatal("request after close must be rejected")
	}
}
