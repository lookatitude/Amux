package context

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/amux-run/amux/internal/platform"
	"github.com/amux-run/amux/internal/securitytest"
)

func identityAgentRedact(_ string, p []byte) ([]byte, error) { return p, nil }

type eventSink struct {
	events []AgentEvent
}

func (s *eventSink) emit(ev AgentEvent) { s.events = append(s.events, ev) }

func (s *eventSink) kinds() []AgentEventKind {
	out := make([]AgentEventKind, len(s.events))
	for i, ev := range s.events {
		out[i] = ev.Kind
	}
	return out
}

func newTestAdapter(t *testing.T, provider string, clock platform.Clock, redact AgentRedact) (*Adapter, *eventSink) {
	t.Helper()
	sink := &eventSink{}
	cfg := AdapterConfig{Surface: "pane-1", Clock: clock, Emit: sink.emit, Redact: redact}
	var (
		a   *Adapter
		err error
	)
	switch provider {
	case "claude-code":
		a, err = NewClaudeCodeAdapter(cfg)
	case "codex":
		a, err = NewCodexAdapter(cfg)
	default:
		t.Fatalf("unknown provider %q", provider)
	}
	if err != nil {
		t.Fatalf("new %s adapter: %v", provider, err)
	}
	return a, sink
}

func TestClaudeCodeAdapterTypedEventMapping(t *testing.T) {
	clock := platform.NewFakeClock(5_000)
	a, sink := newTestAdapter(t, "claude-code", clock, identityAgentRedact)

	input := strings.Join([]string{
		`garbage terminal noise \x1b[31m not json`,
		`{"type":"system","subtype":"init","model":"claude-x","session_id":"s"}`,
		`{"type":"assistant","message":{"content":[{"type":"text","text":"working"}]}}`, // no event
		`{"type":"permission_request","tool_name":"Bash"}`,
		`{"type":"result","subtype":"success","is_error":false,"result":"done fine"}`,
		`{"type":"result","is_error":true,"result":"it broke"}`,
		`{"type":"error","message":"stream exploded"}`,
		`{"type":"system","subtype":"hook_ran"}`, // system non-init: no event
		`{not even json`,
	}, "\n") + "\n"

	// Feed in two arbitrary chunks to exercise partial-line buffering.
	half := len(input) / 2
	a.Feed([]byte(input[:half]))
	a.Feed([]byte(input[half:]))

	wantKinds := []AgentEventKind{
		AgentLifecycleStarted, AgentAttentionNeeded,
		AgentAttentionDone, AgentAttentionError, AgentAttentionError,
	}
	got := sink.kinds()
	if len(got) != len(wantKinds) {
		t.Fatalf("events = %v, want kinds %v", sink.events, wantKinds)
	}
	for i := range wantKinds {
		if got[i] != wantKinds[i] {
			t.Fatalf("event[%d].Kind = %s, want %s", i, got[i], wantKinds[i])
		}
	}
	first := sink.events[0]
	if first.Provider != "claude-code" || first.Surface != "pane-1" || first.AtMS != 5_000 || first.Detail != "claude-x" {
		t.Fatalf("lifecycle event = %+v", first)
	}
	if sink.events[1].Detail != "Bash" {
		t.Fatalf("permission detail = %q, want tool name", sink.events[1].Detail)
	}
	if sink.events[2].Detail != "done fine" || sink.events[3].Detail != "it broke" {
		t.Fatalf("result details = %q / %q", sink.events[2].Detail, sink.events[3].Detail)
	}
}

func TestCodexAdapterTypedEventMapping(t *testing.T) {
	clock := platform.NewFakeClock(6_000)
	a, sink := newTestAdapter(t, "codex", clock, identityAgentRedact)

	input := strings.Join([]string{
		`spinner noise ███`,
		`{"id":"0","msg":{"type":"session_configured","model":"codex-y"}}`,
		`{"id":"1","msg":{"type":"task_started"}}`,
		`{"id":"2","msg":{"type":"exec_approval_request","command":"rm -rf"}}`,
		`{"id":"3","msg":{"type":"agent_message","message":"thinking..."}}`,
		`{"id":"4","msg":{"type":"task_complete","last_agent_message":"all done"}}`,
		`{"id":"5","msg":{"type":"error","message":"sandbox denied"}}`,
		`{"id":"6","msg":{"type":"shutdown_complete"}}`,
		`{"id":"7"}`, // no msg: skipped
	}, "\n") + "\n"
	a.Feed([]byte(input))

	wantKinds := []AgentEventKind{
		AgentLifecycleStarted, AgentSessionStatus, AgentAttentionNeeded,
		AgentSessionStatus, AgentAttentionDone, AgentAttentionError, AgentLifecycleExited,
	}
	got := sink.kinds()
	if len(got) != len(wantKinds) {
		t.Fatalf("events = %v, want kinds %v", sink.events, wantKinds)
	}
	for i := range wantKinds {
		if got[i] != wantKinds[i] {
			t.Fatalf("event[%d].Kind = %s, want %s", i, got[i], wantKinds[i])
		}
	}
	if sink.events[0].Provider != "codex" || sink.events[0].Detail != "codex-y" {
		t.Fatalf("lifecycle event = %+v", sink.events[0])
	}
	if sink.events[4].Detail != "all done" {
		t.Fatalf("task_complete detail = %q", sink.events[4].Detail)
	}
}

func TestAdapterLongLineDroppedAndCounted(t *testing.T) {
	clock := platform.NewFakeClock(0)
	a, sink := newTestAdapter(t, "claude-code", clock, identityAgentRedact)

	// One over-cap line (fed in pieces, no newline yet): must flip to
	// discarding without buffering unbounded.
	chunk := strings.Repeat("x", 32*1024)
	a.Feed([]byte(chunk))
	a.Feed([]byte(chunk))
	a.Feed([]byte(chunk)) // crosses 64 KiB here
	if got := a.pendingBytes(); got > AgentMaxLineBytes {
		t.Fatalf("pending buffer = %d bytes, cap is %d", got, AgentMaxLineBytes)
	}
	a.Feed([]byte("still the same monster line"))
	a.Feed([]byte("\n")) // the long line finally ends
	// A valid event line right after must parse normally.
	a.Feed([]byte(`{"type":"result","result":"ok"}` + "\n"))

	if got := a.Stats().DroppedLongLines; got != 1 {
		t.Fatalf("dropped long lines = %d, want 1", got)
	}
	if len(sink.events) != 1 || sink.events[0].Kind != AgentAttentionDone {
		t.Fatalf("events after long line = %+v, want single attention_done", sink.events)
	}

	// An over-cap line delivered whole in one chunk with its newline.
	a.Feed([]byte(strings.Repeat("y", AgentMaxLineBytes+1) + "\n"))
	if got := a.Stats().DroppedLongLines; got != 2 {
		t.Fatalf("dropped long lines = %d, want 2", got)
	}
}

func TestAdapterEventRateLimited(t *testing.T) {
	clock := platform.NewFakeClock(0)
	a, sink := newTestAdapter(t, "claude-code", clock, identityAgentRedact)

	line := []byte(`{"type":"result","result":"r"}` + "\n")
	for i := 0; i < 25; i++ {
		a.Feed(line)
	}
	if len(sink.events) != AgentMaxEventsPerSecond {
		t.Fatalf("events = %d, want the %d/sec cap", len(sink.events), AgentMaxEventsPerSecond)
	}
	if got := a.Stats().DroppedRateLimited; got != 5 {
		t.Fatalf("rate-limit drops = %d, want 5", got)
	}
	// One second later the bucket refills.
	clock.Advance(time.Second)
	a.Feed(line)
	if len(sink.events) != AgentMaxEventsPerSecond+1 {
		t.Fatalf("events after refill = %d, want %d", len(sink.events), AgentMaxEventsPerSecond+1)
	}
}

func TestAdapterDetailTruncatedAfterRedaction(t *testing.T) {
	clock := platform.NewFakeClock(0)
	a, sink := newTestAdapter(t, "claude-code", clock, identityAgentRedact)

	big := strings.Repeat("d", 3*AgentMaxDetailBytes)
	a.Feed([]byte(`{"type":"result","result":"` + big + `"}` + "\n"))
	if len(sink.events) != 1 {
		t.Fatalf("events = %d, want 1", len(sink.events))
	}
	if got := len(sink.events[0].Detail); got != AgentMaxDetailBytes {
		t.Fatalf("detail length = %d, want truncation at %d", got, AgentMaxDetailBytes)
	}
}

func TestAdapterRedactionFailureDropsEvent(t *testing.T) {
	clock := platform.NewFakeClock(0)
	failing := func(string, []byte) ([]byte, error) { return nil, errors.New("policy down") }
	a, sink := newTestAdapter(t, "codex", clock, failing)

	a.Feed([]byte(`{"id":"1","msg":{"type":"task_complete","last_agent_message":"x"}}` + "\n"))
	if len(sink.events) != 0 {
		t.Fatalf("event egressed despite redaction failure: %+v", sink.events)
	}
	if got := a.Stats().DroppedRedaction; got != 1 {
		t.Fatalf("redaction drops = %d, want 1", got)
	}
}

// TestAgentAdapterSecretNeverEgressesUnredacted mirrors the RED-1
// "agent_adapter" fixture: a derived candidate secret embedded in an agent
// output line must never reach the emit callback unredacted when the adapter
// is wired with a marker-substituting redactor.
func TestAgentAdapterSecretNeverEgressesUnredacted(t *testing.T) {
	const label = "agent-adapter-token"
	secret := securitytest.DeriveCandidateSecret(label)
	marker := securitytest.RedactionMarker(label)
	redact := func(rc string, p []byte) ([]byte, error) {
		if rc != AgentRedactionContext {
			t.Errorf("redaction context = %q, want %q", rc, AgentRedactionContext)
		}
		return []byte(strings.ReplaceAll(string(p), secret, marker)), nil
	}

	lines := map[string]string{
		"claude-code": `{"type":"result","result":"leaked ` + secret + ` in output"}` + "\n",
		"codex":       `{"id":"1","msg":{"type":"agent_message","message":"token is ` + secret + `"}}` + "\n",
	}
	for provider, line := range lines {
		t.Run(provider, func(t *testing.T) {
			clock := platform.NewFakeClock(0)
			a, sink := newTestAdapter(t, provider, clock, redact)
			a.Feed([]byte(line))
			if len(sink.events) != 1 {
				t.Fatalf("events = %d, want 1", len(sink.events))
			}
			ev := sink.events[0]
			if strings.Contains(ev.Title, secret) || strings.Contains(ev.Detail, secret) {
				t.Fatalf("candidate secret egressed unredacted: %+v", ev)
			}
			if !strings.Contains(ev.Detail, marker) {
				t.Fatalf("redaction marker missing from detail: %q", ev.Detail)
			}
		})
	}
}

func TestNewAdapterValidatesDependencies(t *testing.T) {
	clock := platform.NewFakeClock(0)
	emit := func(AgentEvent) {}
	if _, err := NewClaudeCodeAdapter(AdapterConfig{Emit: emit, Redact: identityAgentRedact}); err == nil {
		t.Fatal("nil clock accepted")
	}
	if _, err := NewClaudeCodeAdapter(AdapterConfig{Clock: clock, Redact: identityAgentRedact}); err == nil {
		t.Fatal("nil emit accepted")
	}
	if _, err := NewCodexAdapter(AdapterConfig{Clock: clock, Emit: emit}); err == nil {
		t.Fatal("nil redactor accepted (RED-8 requires the seam)")
	}
}
