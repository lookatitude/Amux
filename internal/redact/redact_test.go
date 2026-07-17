package redact_test

import (
	"bytes"
	"strings"
	"sync"
	"testing"

	"github.com/amux-run/amux/internal/redact"
	"github.com/amux-run/amux/internal/securitytest"
)

// TestRegisteredExactSecretReplaced proves the primary RED-1 mechanism: a
// registered exact secret value is replaced by its marker.
func TestRegisteredExactSecretReplaced(t *testing.T) {
	e := redact.New()
	e.Register("s3cr3t-value", "[REDACTED:TOKEN]")

	out, err := e.Redact("log", []byte("prefix s3cr3t-value suffix"))
	if err != nil {
		t.Fatalf("Redact: %v", err)
	}
	if got := string(out); got != "prefix [REDACTED:TOKEN] suffix" {
		t.Fatalf("got %q, want the marker substituted", got)
	}
	if strings.Contains(string(out), "s3cr3t-value") {
		t.Fatalf("raw secret survived: %q", out)
	}
}

// TestLongestFirst proves overlapping registered secrets never leak a surviving
// suffix: registering "abc" and "abcdef" must scrub "abcdef" whole.
func TestLongestFirst(t *testing.T) {
	e := redact.New()
	e.Register("abc", "[A]")
	e.Register("abcdef", "[B]")

	out, err := e.Redact("log", []byte("abcdef"))
	if err != nil {
		t.Fatalf("Redact: %v", err)
	}
	if got := string(out); got != "[B]" {
		t.Fatalf("got %q, want [B] (longest-first, no [A]def leftover)", got)
	}
}

// TestBuiltinCandidateFullValue proves the built-in AMUXTEST_ pattern scrubs a
// full derived candidate secret with no prefix remnant (RED-2).
func TestBuiltinCandidateFullValue(t *testing.T) {
	e := redact.New()
	secret := securitytest.DeriveCandidateSecret("full")

	out, err := e.Redact("log", []byte("token="+secret+" end"))
	if err != nil {
		t.Fatalf("Redact: %v", err)
	}
	s := string(out)
	if strings.Contains(s, secret) {
		t.Fatalf("raw candidate secret survived: %q", s)
	}
	if strings.Contains(s, securitytest.CandidateSecretPrefix) {
		t.Fatalf("AMUXTEST_ prefix remnant survived: %q", s)
	}
	if !strings.Contains(s, "[REDACTED:candidate]") {
		t.Fatalf("candidate marker missing: %q", s)
	}
}

// TestBuiltinCandidateTruncatedPrefix proves RED-5: a candidate secret cut
// mid-value (prefix + a few hex chars) is still caught — the surviving prefix
// must not leak.
func TestBuiltinCandidateTruncatedPrefix(t *testing.T) {
	e := redact.New()
	secret := securitytest.DeriveCandidateSecret("trunc")
	// Keep the prefix plus the first 3 hex chars, dropping the rest (as a
	// truncation cap would).
	truncated := secret[:len(securitytest.CandidateSecretPrefix)+3]
	if !strings.Contains(truncated, securitytest.CandidateSecretPrefix) {
		t.Fatalf("test setup: truncated value lost its prefix: %q", truncated)
	}

	out, err := e.Redact("hook_output", []byte("...tail "+truncated))
	if err != nil {
		t.Fatalf("Redact: %v", err)
	}
	if strings.Contains(string(out), securitytest.CandidateSecretPrefix) {
		t.Fatalf("truncated prefix leaked: %q", out)
	}
	if !strings.Contains(string(out), "[REDACTED:candidate]") {
		t.Fatalf("truncated candidate not marked: %q", out)
	}
}

// TestEnvironmentShape proves RED-3 reporting shape: KEY= is preserved while
// the secret-classified value is redacted.
func TestEnvironmentShape(t *testing.T) {
	e := redact.New()
	secret := securitytest.DeriveCandidateSecret("env")

	out, err := e.Redact("environment", []byte("GITHUB_TOKEN="+secret))
	if err != nil {
		t.Fatalf("Redact: %v", err)
	}
	s := string(out)
	if !strings.HasPrefix(s, "GITHUB_TOKEN=") {
		t.Fatalf("key not preserved: %q", s)
	}
	if strings.Contains(s, securitytest.CandidateSecretPrefix) {
		t.Fatalf("value not redacted: %q", s)
	}
}

// TestIdempotence proves Redact(Redact(p)) == Redact(p).
func TestIdempotence(t *testing.T) {
	e := redact.New()
	e.Register("registered-secret", "[REDACTED:REG]")
	secret := securitytest.DeriveCandidateSecret("idem")
	payload := []byte("registered-secret and " + secret + " and AKIA1234567890ABCDEF")

	once, err := e.Redact("audit", payload)
	if err != nil {
		t.Fatalf("Redact: %v", err)
	}
	twice, err := e.Redact("audit", once)
	if err != nil {
		t.Fatalf("Redact (twice): %v", err)
	}
	if !bytes.Equal(once, twice) {
		t.Fatalf("not idempotent:\n once=%q\ntwice=%q", once, twice)
	}
}

// TestDeterminism proves the same input yields the same output across runs.
func TestDeterminism(t *testing.T) {
	e := redact.New()
	e.Register("alpha", "[A]")
	e.Register("beta", "[B]")
	payload := []byte("alpha beta " + securitytest.DeriveCandidateSecret("det"))

	first, err := e.Redact("diagnostics", payload)
	if err != nil {
		t.Fatalf("Redact: %v", err)
	}
	for i := 0; i < 100; i++ {
		out, err := e.Redact("diagnostics", payload)
		if err != nil {
			t.Fatalf("Redact iter %d: %v", i, err)
		}
		if !bytes.Equal(first, out) {
			t.Fatalf("non-deterministic at iter %d:\nwant %q\ngot  %q", i, first, out)
		}
	}
}

// TestNoAliasing proves mutating the output cannot change the input — Redact
// always returns a fresh copy, even when no rule matches.
func TestNoAliasing(t *testing.T) {
	e := redact.New()
	input := []byte("no secrets here at all")
	snapshot := append([]byte(nil), input...)

	out, err := e.Redact("config", input)
	if err != nil {
		t.Fatalf("Redact: %v", err)
	}
	if len(out) == 0 {
		t.Fatal("empty output")
	}
	for i := range out {
		out[i] ^= 0xFF
	}
	if !bytes.Equal(input, snapshot) {
		t.Fatalf("mutating output changed the input: %q != %q", input, snapshot)
	}
}

// TestErrPayloadTooLarge proves the RED-8 oversized-egress fail-closed path.
func TestErrPayloadTooLarge(t *testing.T) {
	e := redact.New()
	e.MaxSizeBytes = 16

	out, err := e.Redact("log", bytes.Repeat([]byte("x"), 17))
	if err != redact.ErrPayloadTooLarge {
		t.Fatalf("err = %v, want ErrPayloadTooLarge", err)
	}
	if out != nil {
		t.Fatalf("fail-closed must return nil payload, got %q", out)
	}

	// At or below the bound still redacts normally.
	if _, err := e.Redact("log", bytes.Repeat([]byte("x"), 16)); err != nil {
		t.Fatalf("payload at bound errored: %v", err)
	}
}

// TestEveryRedactionContext proves RED-1 context-agnostic coverage: every
// frozen egress context redacts a candidate secret with the full ruleset —
// unknown contexts are never pass-through.
func TestEveryRedactionContext(t *testing.T) {
	e := redact.New()
	secret := securitytest.DeriveCandidateSecret("ctx")

	for _, ctx := range securitytest.RedactionContexts {
		out, err := e.Redact(ctx, []byte("value="+secret))
		if err != nil {
			t.Fatalf("context %q: Redact: %v", ctx, err)
		}
		s := string(out)
		if strings.Contains(s, secret) {
			t.Fatalf("context %q leaked the raw secret: %q", ctx, s)
		}
		if strings.Contains(s, securitytest.CandidateSecretPrefix) {
			t.Fatalf("context %q leaked the prefix: %q", ctx, s)
		}
		if !strings.Contains(s, "[REDACTED:candidate]") {
			t.Fatalf("context %q missing marker: %q", ctx, s)
		}
	}

	// An unknown context is still redacted (fail-safe, never pass-through).
	out, err := e.Redact("some-future-context", []byte("value="+secret))
	if err != nil {
		t.Fatalf("unknown context: Redact: %v", err)
	}
	if strings.Contains(string(out), securitytest.CandidateSecretPrefix) {
		t.Fatalf("unknown context was pass-through: %q", out)
	}
}

// TestConcurrentRedact exercises the RWMutex under -race: many goroutines
// redact concurrently against a shared engine.
func TestConcurrentRedact(t *testing.T) {
	e := redact.New()
	e.Register("shared-secret", "[REDACTED:SHARED]")
	secret := securitytest.DeriveCandidateSecret("conc")
	payload := []byte("shared-secret " + secret)

	var wg sync.WaitGroup
	for i := 0; i < 64; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 100; j++ {
				out, err := e.Redact("hook_input", payload)
				if err != nil {
					t.Errorf("Redact: %v", err)
					return
				}
				if bytes.Contains(out, []byte("shared-secret")) ||
					bytes.Contains(out, []byte(securitytest.CandidateSecretPrefix)) {
					t.Errorf("leak under concurrency: %q", out)
					return
				}
			}
		}()
	}
	wg.Wait()
}

// TestBuiltinGenericFamilies spot-checks the generic RED-2 heuristics.
func TestBuiltinGenericFamilies(t *testing.T) {
	e := redact.New()
	pem := "-----BEGIN RSA PRIVATE KEY-----\nMIIBOwIBAAJBAKj34\nabc/def+ghi=\n-----END RSA PRIVATE KEY-----"
	cases := []struct {
		name, in, notWant string
	}{
		{"aws", "id AKIAIOSFODNN7EXAMPLE here", "AKIAIOSFODNN7EXAMPLE"},
		{"bearer", "Authorization: Bearer abc123.DEF456_ghi-789", "abc123.DEF456_ghi-789"},
		{"pem", pem, "MIIBOwIBAAJBAKj34"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			out, err := e.Redact("error", []byte(c.in))
			if err != nil {
				t.Fatalf("Redact: %v", err)
			}
			if strings.Contains(string(out), c.notWant) {
				t.Fatalf("credential material survived: %q", out)
			}
		})
	}
}
