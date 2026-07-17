// Package redact is the single centralized redaction engine (RED-1) that every
// amux egress boundary funnels through before data leaves a trust boundary.
// The security contract (docs/security/redaction-and-audit.md) forbids
// scattering per-callsite regexes: exactly one Engine is constructed at daemon
// setup, the known secret values are Registered against it, and every egress
// context — the frozen set in securitytest.RedactionContexts (config,
// environment, hook_input, hook_output, error, log, audit, snapshot,
// agent_adapter, notification, diagnostics) — calls Redact before writing or
// delivering bytes.
//
// The engine combines two mechanisms:
//
//   - Registration (primary). amux knows its own secrets — env values from the
//     allowlist store, tokens, credentials — and Registers each exact VALUE
//     against a label-bearing marker. Exact substring replacement is applied
//     longest-first so an overlapping secret can never leak a surviving suffix.
//
//   - Heuristic patterns (RED-2). Built-in credential-shaped patterns catch
//     values the engine was never told about: AWS access-key IDs, bearer
//     tokens, long high-entropy hex/base64 runs, PEM private-key blocks, and
//     the securitytest candidate-secret family. Patterns are a defence in
//     depth, not the primary contract.
//
// Redact never passes a payload through raw. Unknown contexts are redacted with
// the full ruleset (fail-safe, RED-1), oversized payloads fail closed with
// ErrPayloadTooLarge rather than being scanned unbounded, and an internal
// invariant breach fails closed with ErrRedactionFailed (RED-8) — the caller
// drops that egress. Redact is deterministic, idempotent, safe for concurrent
// use, and always returns a fresh copy (never aliases the input).
//
// Dependencies are stdlib only. Normative prose:
// docs/security/redaction-and-audit.md (RED-1..8).
package redact

import (
	"bytes"
	"errors"
	"regexp"
	"sort"
	"sync"
)

// DefaultMaxSizeBytes is the default MaxSizeBytes guard a New engine carries: a
// generous bound (64 MiB) that normal audit/diagnostic dumps stay well under,
// so the RED-8 oversized-payload fail-closed path only trips on pathological
// input, never on legitimate egress.
const DefaultMaxSizeBytes = 64 << 20

// ErrPayloadTooLarge is returned by Redact when a payload exceeds the engine's
// MaxSizeBytes bound. Per RED-8 the caller must drop the egress rather than
// emit an unscanned (and therefore un-redacted) payload.
var ErrPayloadTooLarge = errors.New("redact: payload exceeds MaxSizeBytes guard")

// ErrRedactionFailed is returned by Redact when an internal invariant is
// violated — a registered exact secret still present in the output after its
// replacement rule ran. It should be unreachable in normal operation; if it
// ever fires the engine fails closed (RED-8) rather than emit a partially
// scrubbed payload, and the caller drops the egress.
var ErrRedactionFailed = errors.New("redact: internal redaction invariant violated (fail closed)")

// exactRule maps a known secret value to its replacement marker.
type exactRule struct {
	secret []byte
	marker []byte
}

// patternRule maps a credential-shaped heuristic (RED-2) to its marker.
type patternRule struct {
	re     *regexp.Regexp
	marker []byte
}

// Engine is the single RED-1 centralized redaction engine. The zero value is
// not ready for use — construct with New, which installs the built-in RED-2
// patterns. Register* happen at setup; Redact may be called concurrently from
// many egress paths. All state is guarded by an RWMutex: readers (Redact) take
// the read lock, writers (Register/RegisterPattern) the write lock.
type Engine struct {
	// MaxSizeBytes bounds the payload Redact will scan. A payload above this
	// bound fails closed with ErrPayloadTooLarge (RED-8) rather than egressing
	// unscanned. A value <= 0 disables the guard (unbounded scan). New sets
	// DefaultMaxSizeBytes; tests set a small positive value to exercise the
	// fail-closed path.
	MaxSizeBytes int

	mu       sync.RWMutex
	exact    []exactRule // kept sorted longest-secret-first
	patterns []patternRule
}

// New returns an Engine preloaded with the built-in RED-2 credential-shaped
// patterns. Even with no exact secrets Registered, a New engine already scrubs
// credential-shaped values heuristically.
func New() *Engine {
	e := &Engine{MaxSizeBytes: DefaultMaxSizeBytes}
	for _, b := range builtinPatterns() {
		e.RegisterPattern(b.re, string(b.marker))
	}
	return e
}

// Register records a known exact secret VALUE mapped to its replacement marker.
// This is the primary RED-1 mechanism: amux registers the secrets it owns
// (allowlist-store env values, tokens) with a label-bearing marker such as
// "[REDACTED:GITHUB_TOKEN]". Replacement is exact-substring and applied
// longest-first across all registered secrets, so a secret that is a suffix of
// another can never leak. Registering an empty secret is a no-op. Safe to call
// only at setup (takes the write lock).
func (e *Engine) Register(secret, marker string) {
	if secret == "" {
		return
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	e.exact = append(e.exact, exactRule{secret: []byte(secret), marker: []byte(marker)})
	// Longest-first so overlapping secrets never leak a surviving suffix; ties
	// broken by value for deterministic ordering.
	sort.Slice(e.exact, func(i, j int) bool {
		if len(e.exact[i].secret) != len(e.exact[j].secret) {
			return len(e.exact[i].secret) > len(e.exact[j].secret)
		}
		return bytes.Compare(e.exact[i].secret, e.exact[j].secret) < 0
	})
}

// RegisterPattern records a credential-shaped heuristic (RED-2) mapped to its
// marker. Built-in patterns are installed by New; callers may add more.
// Registration order is preserved and is the application order in Redact, so
// register broader-consuming patterns (e.g. PEM blocks, prefixed families)
// before the generic high-entropy catch-alls. A nil regexp is a no-op. Safe to
// call only at setup (takes the write lock).
func (e *Engine) RegisterPattern(re *regexp.Regexp, marker string) {
	if re == nil {
		return
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	e.patterns = append(e.patterns, patternRule{re: re, marker: []byte(marker)})
}

// Redact returns a scrubbed copy of payload with every registered exact secret
// (longest-first) and every RED-2 pattern replaced by its marker. The context
// string is advisory — reserved for future per-context policy and logging;
// today every context (including unknown ones) is redacted with the full
// ruleset, so redaction is never pass-through (RED-1 fail-safe).
//
// Guarantees:
//   - deterministic: the same input yields the same output across runs;
//   - idempotent: Redact(Redact(p)) == Redact(p) for the built-in markers;
//   - no aliasing: the returned slice is always a fresh copy, so mutating it
//     cannot affect payload;
//   - fail closed (RED-8): a payload above MaxSizeBytes returns
//     (nil, ErrPayloadTooLarge) and an internal invariant breach returns
//     (nil, ErrRedactionFailed) — never a partially scrubbed payload.
//
// Redact is safe for concurrent use (takes the read lock).
func (e *Engine) Redact(context string, payload []byte) ([]byte, error) {
	_ = context // advisory today; reserved for per-context policy + logging.

	e.mu.RLock()
	defer e.mu.RUnlock()

	if e.MaxSizeBytes > 0 && len(payload) > e.MaxSizeBytes {
		return nil, ErrPayloadTooLarge
	}

	// Always start from a copy so the result never aliases the input, even when
	// no rule matches.
	out := append([]byte(nil), payload...)

	// Exact registered secrets first, longest-first (slice is pre-sorted).
	for _, r := range e.exact {
		out = bytes.ReplaceAll(out, r.secret, r.marker)
	}
	// Then heuristic patterns, in registration order. ReplaceAllLiteral avoids
	// $-expansion so a marker is emitted verbatim (deterministic).
	for _, p := range e.patterns {
		out = p.re.ReplaceAllLiteral(out, p.marker)
	}

	// RED-8 invariant: no registered exact secret may survive its replacement.
	// Unreachable in normal operation (ReplaceAll removes every occurrence);
	// if it ever trips, fail closed rather than egress a leak.
	for _, r := range e.exact {
		if bytes.Contains(out, r.secret) {
			return nil, ErrRedactionFailed
		}
	}
	return out, nil
}

// builtinPattern is a documented RED-2 pattern shipped by New.
type builtinPattern struct {
	re     *regexp.Regexp
	marker []byte
}

// builtinPatterns returns the built-in RED-2 credential-shaped patterns in
// application order. Order matters: broader-consuming families (the candidate
// prefix, PEM blocks) run before the generic high-entropy catch-alls so a
// prefixed value is consumed whole and never leaves its prefix behind.
func builtinPatterns() []builtinPattern {
	return []builtinPattern{
		// securitytest candidate-secret family (AMUXTEST_ + hex). The trailing
		// hex may be cut mid-value by truncation (RED-5), so match the prefix
		// followed by ZERO-or-more hex — a surviving "AMUXTEST_dead" prefix is
		// still caught. Runs first so the generic hex/token patterns below can
		// never strip only the hex and orphan the prefix.
		{regexp.MustCompile(`AMUXTEST_[0-9a-fA-F]*`), []byte("[REDACTED:candidate]")},

		// PEM private-key blocks (any *PRIVATE KEY variant). (?s) lets . span
		// newlines so the whole armored block is one match; runs before the
		// generic base64 catch-all so the body is never redacted piecemeal.
		{regexp.MustCompile(`(?s)-----BEGIN [A-Z0-9 ]*PRIVATE KEY-----.*?-----END [A-Z0-9 ]*PRIVATE KEY-----`), []byte("[REDACTED:private-key]")},

		// AWS access-key IDs: the AKIA prefix plus 16 upper/digit chars.
		{regexp.MustCompile(`AKIA[0-9A-Z]{16}`), []byte("[REDACTED:aws-access-key-id]")},

		// Bearer / Authorization-style tokens: the "Bearer" keyword
		// (case-insensitive) plus a base64ish credential.
		{regexp.MustCompile(`(?i)Bearer\s+[A-Za-z0-9._~+/-]+=*`), []byte("[REDACTED:bearer]")},

		// Long high-entropy hex runs (>=32 chars) that look like keys/digests.
		{regexp.MustCompile(`[0-9a-fA-F]{32,}`), []byte("[REDACTED:hex]")},

		// Long high-entropy base64/base64url runs (>=32 chars). Runs last as the
		// generic catch-all; hex runs are already handled above.
		{regexp.MustCompile(`[A-Za-z0-9+/_-]{32,}={0,2}`), []byte("[REDACTED:token]")},
	}
}
