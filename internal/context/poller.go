package context

import (
	"errors"
	"sync"
	"sync/atomic"
	"time"

	"github.com/amux-run/amux/internal/platform"
)

// CollectFunc produces one pane's context snapshot. It runs on a poller
// worker goroutine and may block (bounded by its own timeouts, e.g. the git
// collector's per-command deadline).
type CollectFunc func(pane string) (PaneContext, error)

// PollEmit receives finished snapshots. It is invoked from a poller worker
// goroutine — never from the goroutine that called Request — so an actor can
// trigger polls from inside its loop without re-entering itself.
type PollEmit func(PaneContext)

// PollerConfig wires a Poller.
type PollerConfig struct {
	// Clock drives the per-pane debounce (monotonic reading; inject
	// platform.FakeClock in tests).
	Clock platform.Clock
	// MinInterval is the per-pane debounce: a second Request for the same pane
	// within this interval is dropped and counted. Zero disables debouncing.
	MinInterval time.Duration
	// Workers caps concurrent collections (default 2).
	Workers int
	// QueueDepth bounds pending requests (default 64); an overflowing Request
	// is dropped and counted, never blocked on.
	QueueDepth int
	// Collect produces the snapshot; Emit receives it.
	Collect CollectFunc
	Emit    PollEmit
}

// PollerStats are drop/error observability counters.
type PollerStats struct {
	Debounced uint64 // requests dropped by the per-pane interval
	Overflow  uint64 // requests dropped because the queue was full
	Errors    uint64 // collections that returned an error (no emit)
}

// Poller schedules per-pane context collection: per-key debounce on the
// injected clock, bounded concurrency via a fixed worker pool, and callback
// delivery outside the requesting goroutine. Request never blocks — under
// pressure it sheds load (counted) instead of stalling an actor.
type Poller struct {
	clock       platform.Clock
	minInterval time.Duration
	collect     CollectFunc
	emit        PollEmit

	jobs chan string
	wg   sync.WaitGroup

	mu     sync.Mutex
	last   map[string]int64 // pane → monotonic nanos of last accepted request
	closed bool

	debounced atomic.Uint64
	overflow  atomic.Uint64
	errs      atomic.Uint64
}

// NewPoller validates the config, starts the worker pool, and returns the
// running poller. Close releases it.
func NewPoller(cfg PollerConfig) (*Poller, error) {
	switch {
	case cfg.Clock == nil:
		return nil, errors.New("context: poller clock is required")
	case cfg.Collect == nil:
		return nil, errors.New("context: poller collect func is required")
	case cfg.Emit == nil:
		return nil, errors.New("context: poller emit func is required")
	case cfg.MinInterval < 0:
		return nil, errors.New("context: poller min interval must be >= 0")
	}
	workers := cfg.Workers
	if workers <= 0 {
		workers = 2
	}
	depth := cfg.QueueDepth
	if depth <= 0 {
		depth = 64
	}
	p := &Poller{
		clock:       cfg.Clock,
		minInterval: cfg.MinInterval,
		collect:     cfg.Collect,
		emit:        cfg.Emit,
		jobs:        make(chan string, depth),
		last:        make(map[string]int64),
	}
	p.wg.Add(workers)
	for i := 0; i < workers; i++ {
		go p.worker()
	}
	return p, nil
}

func (p *Poller) worker() {
	defer p.wg.Done()
	for pane := range p.jobs {
		snap, err := p.collect(pane)
		if err != nil {
			p.errs.Add(1)
			continue
		}
		p.emit(snap)
	}
}

// Request asks for one collection of pane. It returns true when the request
// was accepted, false when it was debounced (same pane inside MinInterval) or
// shed (queue full, poller closed). It never blocks and never runs Collect or
// Emit on the caller's goroutine.
func (p *Poller) Request(pane string) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed {
		return false
	}
	now := p.clock.MonotonicNanos()
	if last, ok := p.last[pane]; ok && p.minInterval > 0 && now-last < p.minInterval.Nanoseconds() {
		p.debounced.Add(1)
		return false
	}
	select {
	case p.jobs <- pane:
		p.last[pane] = now
		return true
	default:
		p.overflow.Add(1)
		return false
	}
}

// Stats returns the drop/error counters.
func (p *Poller) Stats() PollerStats {
	return PollerStats{
		Debounced: p.debounced.Load(),
		Overflow:  p.overflow.Load(),
		Errors:    p.errs.Load(),
	}
}

// Close stops accepting requests, drains in-flight work, and waits for the
// workers to exit. Idempotent.
func (p *Poller) Close() {
	p.mu.Lock()
	if p.closed {
		p.mu.Unlock()
		return
	}
	p.closed = true
	close(p.jobs)
	p.mu.Unlock()
	p.wg.Wait()
}
