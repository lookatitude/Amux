package observability

import (
	"fmt"
	"sync"
	"sync/atomic"
)

// Pre-declared metric names for the runtime's hot seams. One name is one
// instrument for the whole daemon; subsystems reference these constants rather
// than minting strings so dumps stay stable across boots (pinned by
// TestMetricNamesFrozenAndDistinct).
const (
	// MetricControlMailboxDepth (gauge): pending messages in the control actor's mailbox.
	MetricControlMailboxDepth = "control_mailbox_depth"
	// MetricControlMailboxRejections (counter): control-mailbox submissions rejected at capacity.
	MetricControlMailboxRejections = "control_mailbox_rejections"
	// MetricSessionMailboxDepth (gauge): pending messages across session-actor mailboxes.
	MetricSessionMailboxDepth = "session_mailbox_depth"
	// MetricSessionMailboxRejections (counter): session-mailbox submissions rejected at capacity.
	MetricSessionMailboxRejections = "session_mailbox_rejections"
	// MetricEventRingAppends (counter): events appended to output/event rings.
	MetricEventRingAppends = "event_ring_appends"
	// MetricEventRingEvictions (counter): ring entries evicted to bound memory.
	MetricEventRingEvictions = "event_ring_evictions"
	// MetricAttachSlowConsumerDisconnects (counter): attached clients disconnected for falling behind.
	MetricAttachSlowConsumerDisconnects = "attach_slow_consumer_disconnects"
	// MetricAttachReplayBytesServed (counter): bytes served from replay on attach cutover.
	MetricAttachReplayBytesServed = "attach_replay_bytes_served"
	// MetricPTYSpawns (counter): PTY child processes started.
	MetricPTYSpawns = "pty_spawns"
	// MetricPTYLive (gauge): PTY children currently alive.
	MetricPTYLive = "pty_live"
	// MetricPTYExits (counter): PTY children reaped.
	MetricPTYExits = "pty_exits"
	// MetricAttachObservers (gauge): currently attached observers.
	MetricAttachObservers = "attach_observers"
	// MetricAttachInputLeases (gauge): currently held input leases.
	MetricAttachInputLeases = "attach_input_leases"
	// MetricHookActivationsAllowed (counter): hook activations authorized and launched.
	MetricHookActivationsAllowed = "hook_activations_allowed"
	// MetricHookActivationsDenied (counter): hook activations denied (trust, digest, epoch, grant).
	MetricHookActivationsDenied = "hook_activations_denied"
	// MetricNotifyPublishes (counter): notifications published to the store.
	MetricNotifyPublishes = "notify_publishes"
	// MetricNotifyDeliveryFailures (counter): best-effort desktop deliveries that failed (advisory).
	MetricNotifyDeliveryFailures = "notify_delivery_failures"
)

// Counter is a monotonically increasing atomic counter. The zero value is
// ready to use; all methods are safe for concurrent use.
type Counter struct {
	v atomic.Int64
}

// Add adds delta to the counter.
func (c *Counter) Add(delta int64) { c.v.Add(delta) }

// Inc adds one to the counter.
func (c *Counter) Inc() { c.v.Add(1) }

// Load returns the current value.
func (c *Counter) Load() int64 { return c.v.Load() }

// Gauge is an atomic instantaneous value. The zero value is ready to use; all
// methods are safe for concurrent use.
type Gauge struct {
	v atomic.Int64
}

// Set replaces the gauge value.
func (g *Gauge) Set(v int64) { g.v.Store(v) }

// Add adjusts the gauge by delta (negative deltas decrease it).
func (g *Gauge) Add(delta int64) { g.v.Add(delta) }

// Load returns the current value.
func (g *Gauge) Load() int64 { return g.v.Load() }

// Registry is a dependency-free, concurrency-safe registry of named counters
// and gauges sharing one namespace. Lookups are get-or-create and return the
// same instrument instance for the same name, so hot paths may call
// Counter(name).Inc() directly or cache the instrument once.
type Registry struct {
	mu       sync.Mutex
	counters map[string]*Counter
	gauges   map[string]*Gauge
}

// NewRegistry returns an empty registry.
func NewRegistry() *Registry {
	return &Registry{
		counters: make(map[string]*Counter),
		gauges:   make(map[string]*Gauge),
	}
}

// Counter returns the counter registered under name, creating it on first use.
// It panics if name is already registered as a gauge: one name is one
// instrument kind, and a silent merge would corrupt dumps.
func (r *Registry) Counter(name string) *Counter {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, clash := r.gauges[name]; clash {
		panic(fmt.Sprintf("observability: metric %q already registered as a gauge", name))
	}
	c, ok := r.counters[name]
	if !ok {
		c = &Counter{}
		r.counters[name] = c
	}
	return c
}

// Gauge returns the gauge registered under name, creating it on first use.
// It panics if name is already registered as a counter.
func (r *Registry) Gauge(name string) *Gauge {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, clash := r.counters[name]; clash {
		panic(fmt.Sprintf("observability: metric %q already registered as a counter", name))
	}
	g, ok := r.gauges[name]
	if !ok {
		g = &Gauge{}
		r.gauges[name] = g
	}
	return g
}

// Snapshot returns an independent copy of every registered instrument's
// current value keyed by name. The single namespace guarantees no key
// collision. Encoding the snapshot with encoding/json yields deterministic,
// sorted key order (json.Marshal sorts map keys), which is what Dump relies on.
func (r *Registry) Snapshot() map[string]int64 {
	r.mu.Lock()
	defer r.mu.Unlock()
	snap := make(map[string]int64, len(r.counters)+len(r.gauges))
	for name, c := range r.counters {
		snap[name] = c.Load()
	}
	for name, g := range r.gauges {
		snap[name] = g.Load()
	}
	return snap
}
