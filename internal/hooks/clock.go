package hooks

import (
	"sync"
)

// Scheduler is the time seam the hook runtime uses for every bounded-execution
// deadline: the hook timeout kill and the terminate→kill-tree escalation
// (hook-authorization.md §7). Production wires RealScheduler (real timers);
// tests and the securitytest conformance wiring inject a SchedClock so the
// 250 ms and 2000 ms gates are asserted deterministically against fake time,
// never against wall-clock sleeps.
type Scheduler interface {
	// NowUnixMilli is the current fake/real wall time in Unix milliseconds.
	NowUnixMilli() int64
	// MonotonicNanos satisfies platform.Clock for reuse as the daemon clock.
	MonotonicNanos() int64
	// AfterFunc schedules fn to run once delayMS in the future. The returned
	// cancel stops it if it has not fired. delayMS <= 0 fires as soon as time
	// is next observed to reach now.
	AfterFunc(delayMS int64, fn func()) (cancel func())
}

// SchedClock is a deterministic, schedulable clock. Time advances ONLY when a
// test calls Advance; scheduled callbacks fire synchronously during Advance
// with Now() reading exactly their deadline, so a terminate at T and a
// kill-tree scheduled at T+2000 report KilledAtMS-TerminatedAtMS == 2000
// exactly (the frozen escalation boundary). It implements Scheduler,
// platform.Clock, and the securitytest.FakeClock surface.
type SchedClock struct {
	mu     sync.Mutex
	now    int64
	nanos  int64
	nextID uint64
	timers map[uint64]*schedTimer
}

type schedTimer struct {
	id       uint64
	deadline int64
	fn       func()
	fired    bool
}

// NewSchedClock returns a SchedClock started at startMillis.
func NewSchedClock(startMillis int64) *SchedClock {
	return &SchedClock{now: startMillis, timers: map[uint64]*schedTimer{}}
}

// NowUnixMilli returns fake wall time.
func (c *SchedClock) NowUnixMilli() int64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

// MonotonicNanos returns a monotonic reading that never moves backward.
func (c *SchedClock) MonotonicNanos() int64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.nanos
}

// AfterFunc schedules fn at now+delayMS.
func (c *SchedClock) AfterFunc(delayMS int64, fn func()) func() {
	c.mu.Lock()
	id := c.nextID
	c.nextID++
	c.timers[id] = &schedTimer{id: id, deadline: c.now + delayMS, fn: fn}
	c.mu.Unlock()
	return func() {
		c.mu.Lock()
		delete(c.timers, id)
		c.mu.Unlock()
	}
}

// Advance moves time forward by deltaMS, firing every timer whose deadline is
// crossed in deadline order. Now() reads each timer's deadline while its
// callback runs, so escalation math is exact. Callbacks run without the lock
// held so they may schedule further timers.
func (c *SchedClock) Advance(deltaMS int64) {
	c.mu.Lock()
	target := c.now + deltaMS
	c.nanos += deltaMS * 1_000_000
	for {
		var next *schedTimer
		for _, t := range c.timers {
			if t.fired || t.deadline > target {
				continue
			}
			if next == nil || t.deadline < next.deadline || (t.deadline == next.deadline && t.id < next.id) {
				next = t
			}
		}
		if next == nil {
			break
		}
		next.fired = true
		delete(c.timers, next.id)
		c.now = next.deadline
		fn := next.fn
		c.mu.Unlock()
		fn()
		c.mu.Lock()
	}
	c.now = target
	c.mu.Unlock()
}
