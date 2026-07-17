package observability

// Benchmark entry points for the QA/devops reference profiles. These cover the
// observability package's own hot paths only; subsystem benchmarks (ring
// append, replay cutover, mailbox throughput, ...) live in their own packages
// next to the code they measure (see doc.go).

import (
	"io"
	"log/slog"
	"testing"

	"github.com/amux-run/amux/internal/platform"
)

func BenchmarkCounterAdd(b *testing.B) {
	c := NewRegistry().Counter(MetricEventRingAppends)
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		c.Add(1)
	}
}

func BenchmarkGaugeSet(b *testing.B) {
	g := NewRegistry().Gauge(MetricSessionMailboxDepth)
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		g.Set(int64(i))
	}
}

func BenchmarkDump(b *testing.B) {
	r := NewRegistry()
	r.Counter(MetricEventRingAppends).Add(123)
	r.Gauge(MetricPTYLive).Set(4)
	in := DumpInput{
		BootID:  "boot-bench",
		Version: "bench",
		Clock:   platform.NewFakeClock(1),
		Metrics: r,
		Extra:   map[string]any{"sessions": 2},
	}
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		if err := Dump(io.Discard, in); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkWithSessionLogger(b *testing.B) {
	root := slog.New(slog.NewJSONHandler(io.Discard, nil))
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		WithSession(root, "sess-bench").Info("probe")
	}
}
