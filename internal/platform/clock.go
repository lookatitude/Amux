package platform

import (
	"sync"
	"time"
)

// systemClock is the production Clock. It is platform-neutral (Go's time package
// abstracts the OS), so it lives in a build-tag-free file.
type systemClock struct{}

// NewSystemClock returns the production monotonic wall clock.
func NewSystemClock() Clock { return systemClock{} }

func (systemClock) NowUnixMilli() int64   { return time.Now().UnixMilli() }
func (systemClock) MonotonicNanos() int64 { return int64(monotonic()) }

// monotonic returns a monotonic nanosecond reading. time.Now() carries a
// monotonic component; subtracting a fixed base yields a stable monotonic value.
var monoBase = time.Now()

func monotonic() time.Duration { return time.Since(monoBase) }

// FakeClock is a deterministic Clock for tests. Wall and monotonic time advance
// only when the test calls Advance or SetUnixMilli, so deadline/heartbeat/
// 250 ms-gate logic is reproducible and free of real sleeps.
type FakeClock struct {
	mu     sync.Mutex
	millis int64
	nanos  int64
}

// NewFakeClock returns a FakeClock started at the given Unix-millisecond time.
func NewFakeClock(startMillis int64) *FakeClock {
	return &FakeClock{millis: startMillis}
}

func (f *FakeClock) NowUnixMilli() int64 {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.millis
}

func (f *FakeClock) MonotonicNanos() int64 {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.nanos
}

// Advance moves both wall and monotonic time forward by d.
func (f *FakeClock) Advance(d time.Duration) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.millis += d.Milliseconds()
	f.nanos += int64(d)
}

// SetUnixMilli sets wall time directly (used to model clock regression). It does
// NOT move monotonic time backward — the monotonic reading only ever advances,
// mirroring a real monotonic clock.
func (f *FakeClock) SetUnixMilli(m int64) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.millis = m
}
