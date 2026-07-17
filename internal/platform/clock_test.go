package platform

import (
	"testing"
	"time"
)

func TestFakeClockDeterministic(t *testing.T) {
	c := NewFakeClock(1000)
	if c.NowUnixMilli() != 1000 {
		t.Fatal("fake clock start time wrong")
	}
	c.Advance(250 * time.Millisecond)
	if c.NowUnixMilli() != 1250 {
		t.Fatalf("advance wrong: %d", c.NowUnixMilli())
	}
	// Monotonic reading advances with the same delta.
	if c.MonotonicNanos() != int64(250*time.Millisecond) {
		t.Fatalf("monotonic advance wrong: %d", c.MonotonicNanos())
	}
	// Regressing wall time must NOT move monotonic time backward.
	before := c.MonotonicNanos()
	c.SetUnixMilli(500)
	if c.NowUnixMilli() != 500 {
		t.Fatal("SetUnixMilli did not take")
	}
	if c.MonotonicNanos() != before {
		t.Fatal("wall-clock regression must not affect monotonic reading")
	}
}

func TestSystemClockMovesForward(t *testing.T) {
	c := NewSystemClock()
	a := c.MonotonicNanos()
	time.Sleep(time.Millisecond)
	b := c.MonotonicNanos()
	if b < a {
		t.Fatal("system monotonic clock went backward")
	}
	if c.NowUnixMilli() <= 0 {
		t.Fatal("system wall clock nonpositive")
	}
}
