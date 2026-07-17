package redact_test

import (
	"bytes"
	"testing"

	"github.com/amux-run/amux/internal/redact"
)

// FuzzRedact drives the engine with random payloads and random registered
// secrets: Redact must never panic, must never leak a registered exact secret
// on success, and must keep output length bounded.
func FuzzRedact(f *testing.F) {
	f.Add([]byte("hello example-secret world"), "hello", "world")
	f.Add([]byte("AKIAIOSFODNN7EXAMPLE Bearer tok"), "", "tok")
	f.Add([]byte(""), "x", "")

	f.Fuzz(func(t *testing.T, payload []byte, s1, s2 string) {
		e := redact.New()
		// Short markers keep the exact-replacement expansion factor small and
		// bounded for the length assertion below.
		if s1 != "" {
			e.Register(s1, "[R1]")
		}
		if s2 != "" {
			e.Register(s2, "[R2]")
		}

		out, err := e.Redact("fuzz", payload)
		if err != nil {
			// Fail-closed paths (oversized / invariant) return nil + error and
			// are a conformant refusal, not a leak.
			return
		}

		// On success no registered exact secret may survive. (A secret that is a
		// substring of a marker can be reintroduced and would fail the RED-8
		// invariant, returning an error above — so if we are here, it is clean.)
		if s1 != "" && bytes.Contains(out, []byte(s1)) {
			t.Fatalf("registered secret s1 %q leaked into %q", s1, out)
		}
		if s2 != "" && bytes.Contains(out, []byte(s2)) {
			t.Fatalf("registered secret s2 %q leaked into %q", s2, out)
		}

		// Output length is bounded: every input byte can be replaced by a marker
		// of at most a small constant length across the (finite) rule passes.
		if max := 64 * (len(payload) + 1); len(out) > max {
			t.Fatalf("output length %d exceeds bound %d", len(out), max)
		}
	})
}
