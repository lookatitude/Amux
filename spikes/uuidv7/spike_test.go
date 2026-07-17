package uuidv7spike

import (
	"sort"
	"sync"
	"testing"

	"github.com/google/uuid"
)

// TestGoogleUUIDMonotonicWithinMillisecond proves the production dependency's
// same-millisecond monotonicity: 100k sequential NewV7 values are strictly
// increasing lexicographically and all unique. This is the empirical basis for
// relying on google/uuid rather than the fallback clamp (ADR-0002/0007).
func TestGoogleUUIDMonotonicWithinMillisecond(t *testing.T) {
	const n = 100_000
	prev := ""
	seen := make(map[string]struct{}, n)
	for i := 0; i < n; i++ {
		u, err := uuid.NewV7()
		if err != nil {
			t.Fatalf("NewV7 error at %d: %v", i, err)
		}
		s := u.String()
		if prev != "" && s <= prev {
			t.Fatalf("non-monotonic at %d: %q <= %q", i, s, prev)
		}
		if _, dup := seen[s]; dup {
			t.Fatalf("duplicate UUIDv7 at %d: %q", i, s)
		}
		seen[s] = struct{}{}
		prev = s
	}
}

// TestGoogleUUIDConcurrentUnique proves concurrent generation yields all-unique
// values (no torn counter under contention).
func TestGoogleUUIDConcurrentUnique(t *testing.T) {
	const goroutines, each = 32, 2000
	var mu sync.Mutex
	seen := make(map[string]struct{}, goroutines*each)
	var wg sync.WaitGroup
	for g := 0; g < goroutines; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < each; i++ {
				u, err := uuid.NewV7()
				if err != nil {
					t.Errorf("NewV7 error: %v", err)
					return
				}
				s := u.String()
				mu.Lock()
				if _, dup := seen[s]; dup {
					t.Errorf("duplicate under concurrency: %q", s)
				}
				seen[s] = struct{}{}
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
	if len(seen) != goroutines*each {
		t.Fatalf("expected %d unique, got %d", goroutines*each, len(seen))
	}
}

// TestClampSurvivesClockRegression drives the Amux-owned fallback generator with
// a deliberately regressing clock and asserts the emitted IDs remain strictly
// non-decreasing — the exact invariant that must survive a dependency swap.
func TestClampSurvivesClockRegression(t *testing.T) {
	// Regressing, repeating clock: forward, backward, stall, forward.
	ticks := []int64{1000, 1001, 999, 999, 998, 1000, 1002, 1002, 1002}
	idx := 0
	now := func() int64 {
		v := ticks[idx%len(ticks)]
		idx++
		return v
	}
	// Deterministic entropy so the test is reproducible.
	seed := uint64(0x9E3779B97F4A7C15)
	entropy := func() uint64 {
		seed ^= seed << 13
		seed ^= seed >> 7
		seed ^= seed << 17
		return seed
	}
	g := NewMonotonicV7(now, entropy)

	prev := ""
	var all []string
	for i := 0; i < len(ticks)*3; i++ {
		s := g.Next()
		if prev != "" && s < prev {
			t.Fatalf("clamp regression at %d: %q < %q", i, s, prev)
		}
		prev = s
		all = append(all, s)
	}
	// Sanity: the emitted order already equals sorted order (strictly ordered).
	sorted := append([]string(nil), all...)
	sort.Strings(sorted)
	for i := range all {
		if all[i] != sorted[i] {
			t.Fatalf("emitted order is not sorted order at %d", i)
		}
	}
}
