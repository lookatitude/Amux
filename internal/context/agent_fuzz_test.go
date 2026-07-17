package context

import (
	"strings"
	"testing"

	"github.com/amux-run/amux/internal/platform"
)

// FuzzAdapterFeed drives both provider adapters with arbitrary bytes, split
// into chunks to exercise partial-line buffering. Invariants: no panic, the
// partial-line buffer stays bounded by AgentMaxLineBytes, and everything
// emitted is a typed event with a bounded Detail — regardless of input.
func FuzzAdapterFeed(f *testing.F) {
	f.Add([]byte(`{"type":"system","subtype":"init","model":"m"}` + "\n"))
	f.Add([]byte(`{"type":"result","is_error":true,"result":"boom"}` + "\n"))
	f.Add([]byte(`{"id":"1","msg":{"type":"task_complete","last_agent_message":"ok"}}` + "\n"))
	f.Add([]byte(`{"id":"2","msg":{"type":"exec_approval_request"}}` + "\n"))
	f.Add([]byte("plain terminal noise\r\n\x1b[31mcolor\x1b[0m\n"))
	f.Add([]byte(`{"type":`))
	f.Add([]byte(strings.Repeat("A", 70_000)))
	f.Add([]byte("\n\n\n{}\n{\"msg\":42}\n"))

	f.Fuzz(func(t *testing.T, data []byte) {
		clock := platform.NewFakeClock(0)
		check := func(ev AgentEvent) {
			if !ValidAgentEventKind(ev.Kind) {
				t.Fatalf("untyped event kind %q", ev.Kind)
			}
			if len(ev.Detail) > AgentMaxDetailBytes {
				t.Fatalf("detail exceeds cap: %d bytes", len(ev.Detail))
			}
			if ev.Provider != "claude-code" && ev.Provider != "codex" {
				t.Fatalf("unexpected provider %q", ev.Provider)
			}
		}
		cc, err := NewClaudeCodeAdapter(AdapterConfig{
			Surface: "fuzz", Clock: clock, Emit: check, Redact: identityAgentRedact,
		})
		if err != nil {
			t.Fatal(err)
		}
		cx, err := NewCodexAdapter(AdapterConfig{
			Surface: "fuzz", Clock: clock, Emit: check, Redact: identityAgentRedact,
		})
		if err != nil {
			t.Fatal(err)
		}
		for _, a := range []*Adapter{cc, cx} {
			// Feed in three chunks to hit the buffering paths, then whole.
			third := len(data) / 3
			a.Feed(data[:third])
			a.Feed(data[third : 2*third])
			a.Feed(data[2*third:])
			a.Feed(data)
			if got := a.pendingBytes(); got > AgentMaxLineBytes {
				t.Fatalf("unbounded buffering: %d pending bytes", got)
			}
		}
	})
}
