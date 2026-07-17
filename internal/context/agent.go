package context

import (
	"bytes"
	"errors"
	"sync"

	"github.com/amux-run/amux/internal/platform"
)

// ============================================================================
// B10 FROZEN adapter contract.
//
// An agent adapter is constructed per surface and connects to exactly two
// things: the surface's output tee (bytes IN via Feed) and an emit callback
// (typed AgentEvents OUT). That is the entire capability surface — by
// construction an adapter has NO process spawn, NO filesystem, NO environment,
// and NO graph access; the interface makes acquiring any of them impossible.
// Provider-specific parsing therefore stays OUT of the core state model: the
// daemon consumes only the typed events (spec: adapters publish ONLY typed
// lifecycle/session/attention events).
//
// Frozen caps (changing any is a B10 contract change):
//   - AgentMaxLineBytes  (64 KiB): longer lines are dropped and counted,
//     never buffered unbounded.
//   - AgentMaxEventsPerSecond (20): token bucket on the injected clock;
//     excess events are dropped and counted.
//   - AgentMaxDetailBytes (4 KiB): Detail is truncated AFTER redaction.
//
// Redaction (RED-1 context "agent_adapter", RED-8 fail closed): Title and
// Detail pass through the injected redactor before emit; a redaction error
// drops the event and counts it — nothing unredacted ever reaches the
// callback.
// ============================================================================

// Frozen adapter caps. See the contract block above.
const (
	AgentMaxLineBytes       = 64 * 1024
	AgentMaxEventsPerSecond = 20
	AgentMaxDetailBytes     = 4 * 1024
)

// AgentRedactionContext is the redaction-policy context for adapter egress
// (mirrors the RED-1 "agent_adapter" fixture context).
const AgentRedactionContext = "agent_adapter"

// AgentEventKind is the closed set of typed events an adapter may emit.
type AgentEventKind string

const (
	AgentLifecycleStarted AgentEventKind = "lifecycle_started"
	AgentLifecycleExited  AgentEventKind = "lifecycle_exited"
	AgentSessionTitle     AgentEventKind = "session_title"
	AgentSessionStatus    AgentEventKind = "session_status"
	AgentAttentionNeeded  AgentEventKind = "attention_needed"
	AgentAttentionDone    AgentEventKind = "attention_done"
	AgentAttentionError   AgentEventKind = "attention_error"
)

// ValidAgentEventKind reports whether k is in the frozen kind set.
func ValidAgentEventKind(k AgentEventKind) bool {
	switch k {
	case AgentLifecycleStarted, AgentLifecycleExited, AgentSessionTitle,
		AgentSessionStatus, AgentAttentionNeeded, AgentAttentionDone,
		AgentAttentionError:
		return true
	}
	return false
}

// AgentEvent is the only thing an adapter can produce.
type AgentEvent struct {
	Provider string
	Surface  string
	Kind     AgentEventKind
	Title    string
	Detail   string
	AtMS     int64
}

// AgentEmit receives redacted, typed events. It is called synchronously from
// Feed (i.e. on the output-tee goroutine); the receiver must not block.
type AgentEmit func(AgentEvent)

// AgentRedact is the injected redaction seam for adapter egress. An error
// means the event must not egress; the adapter drops it (fail closed).
type AgentRedact func(redactionContext string, payload []byte) ([]byte, error)

// AdapterConfig wires one adapter to one surface.
type AdapterConfig struct {
	// Surface identifies the pane/surface whose output tee feeds the adapter.
	Surface string
	// Clock drives the event-rate token bucket and event timestamps.
	Clock platform.Clock
	// Emit receives the typed events.
	Emit AgentEmit
	// Redact guards Title and Detail (context "agent_adapter").
	Redact AgentRedact
}

// AdapterStats are the frozen-cap drop counters.
type AdapterStats struct {
	DroppedLongLines   uint64 // lines over AgentMaxLineBytes
	DroppedRateLimited uint64 // events over AgentMaxEventsPerSecond
	DroppedRedaction   uint64 // events whose redaction failed (fail closed)
}

// parseLine is a provider's line parser: given one complete line it either
// recognizes a typed event (ok=true) or asks the adapter to skip the line
// silently (terminal noise, unknown JSON, partial escape soup).
type parseLine func(line []byte) (kind AgentEventKind, title, detail string, ok bool)

// Adapter is the shared byte-stream engine behind every provider. Construct
// one via NewClaudeCodeAdapter or NewCodexAdapter. Feed is safe for a single
// producer or several (internally serialized); memory is bounded by
// AgentMaxLineBytes regardless of input.
type Adapter struct {
	provider string
	surface  string
	clock    platform.Clock
	emit     AgentEmit
	redact   AgentRedact
	parse    parseLine

	mu         sync.Mutex
	buf        []byte // partial line, always <= AgentMaxLineBytes
	discarding bool   // inside an over-long line, waiting for its newline
	tokens     float64
	lastRefill int64 // monotonic nanos of the last bucket refill

	droppedLongLines   uint64
	droppedRateLimited uint64
	droppedRedaction   uint64
}

func newAdapter(provider string, cfg AdapterConfig, parse parseLine) (*Adapter, error) {
	switch {
	case cfg.Clock == nil:
		return nil, errors.New("context: adapter clock is required")
	case cfg.Emit == nil:
		return nil, errors.New("context: adapter emit func is required")
	case cfg.Redact == nil:
		return nil, errors.New("context: adapter redactor is required (RED-8: no unredacted egress)")
	}
	return &Adapter{
		provider:   provider,
		surface:    cfg.Surface,
		clock:      cfg.Clock,
		emit:       cfg.Emit,
		redact:     cfg.Redact,
		parse:      parse,
		tokens:     AgentMaxEventsPerSecond,
		lastRefill: cfg.Clock.MonotonicNanos(),
	}, nil
}

// Feed consumes the next chunk of the surface's output tee. It splits on
// '\n', enforces the line cap, and hands complete lines to the provider
// parser. Arbitrary garbage is tolerated: unparseable lines are skipped
// silently, over-long lines are dropped and counted, and nothing is ever
// buffered beyond AgentMaxLineBytes.
func (a *Adapter) Feed(p []byte) {
	a.mu.Lock()
	defer a.mu.Unlock()
	for len(p) > 0 {
		idx := bytes.IndexByte(p, '\n')
		if idx < 0 {
			// No line end in this chunk: buffer (bounded) or keep discarding.
			if a.discarding {
				return
			}
			if len(a.buf)+len(p) > AgentMaxLineBytes {
				a.buf = nil
				a.discarding = true
				a.droppedLongLines++
				return
			}
			a.buf = append(a.buf, p...)
			return
		}
		part := p[:idx]
		p = p[idx+1:]
		if a.discarding {
			// The over-long line finally ended; resume normal framing.
			a.discarding = false
			continue
		}
		if len(a.buf)+len(part) > AgentMaxLineBytes {
			a.buf = nil
			a.droppedLongLines++
			continue
		}
		line := append(a.buf, part...)
		a.buf = nil
		a.handleLine(line)
	}
}

// handleLine parses, rate-limits, redacts, truncates, and emits one line.
// Order is load-bearing: redaction runs on the FULL detail before the 4 KiB
// truncation so a boundary cut can never split a secret out of the redactor's
// view (RED-8).
func (a *Adapter) handleLine(line []byte) {
	kind, title, detail, ok := a.parse(line)
	if !ok {
		return // interleaved terminal noise / unknown shapes: skip silently
	}
	if !a.allowEvent() {
		a.droppedRateLimited++
		return
	}
	rt, err := a.redact(AgentRedactionContext, []byte(title))
	if err != nil {
		a.droppedRedaction++
		return
	}
	rd, err := a.redact(AgentRedactionContext, []byte(detail))
	if err != nil {
		a.droppedRedaction++
		return
	}
	if len(rd) > AgentMaxDetailBytes {
		rd = rd[:AgentMaxDetailBytes]
	}
	a.emit(AgentEvent{
		Provider: a.provider,
		Surface:  a.surface,
		Kind:     kind,
		Title:    string(rt),
		Detail:   string(rd),
		AtMS:     a.clock.NowUnixMilli(),
	})
}

// allowEvent is the frozen 20 events/sec token bucket on the injected clock.
func (a *Adapter) allowEvent() bool {
	now := a.clock.MonotonicNanos()
	elapsed := now - a.lastRefill
	if elapsed > 0 {
		a.tokens += float64(elapsed) / 1e9 * AgentMaxEventsPerSecond
		if a.tokens > AgentMaxEventsPerSecond {
			a.tokens = AgentMaxEventsPerSecond
		}
	}
	a.lastRefill = now
	if a.tokens >= 1 {
		a.tokens--
		return true
	}
	return false
}

// Stats returns the drop counters.
func (a *Adapter) Stats() AdapterStats {
	a.mu.Lock()
	defer a.mu.Unlock()
	return AdapterStats{
		DroppedLongLines:   a.droppedLongLines,
		DroppedRateLimited: a.droppedRateLimited,
		DroppedRedaction:   a.droppedRedaction,
	}
}

// pendingBytes exposes the partial-line buffer size for boundedness tests.
func (a *Adapter) pendingBytes() int {
	a.mu.Lock()
	defer a.mu.Unlock()
	return len(a.buf)
}
