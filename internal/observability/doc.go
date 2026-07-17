// Package observability is the daemon's diagnostics and performance seam
// (ADR-0001 package map: "slog, metrics, profiling, diagnostics"). It provides
// four small, dependency-free surfaces:
//
//   - Correlation logging: WithBoot/WithSession/WithConn/WithSurface/
//     WithActivation derive *slog.Logger children carrying frozen attribute
//     keys (boot_id, session, conn, surface, activation) so every record from a
//     subsystem is greppable by the identity it acted for. Per the daemon
//     logging discipline (PRD F10; cmd/amuxd), diagnostics go to stderr slog
//     only — never to stdout or a protocol stream.
//   - Metrics: a stdlib-only Registry of atomic Counters and Gauges with
//     pre-declared names for the runtime's hot seams (mailboxes, event ring,
//     attach, PTY, hooks, notifications) and a Snapshot for dumps.
//   - Bounded diagnostic dump: Dump writes one JSON document (capped at
//     DumpMaxBytes, fail closed — never truncated mid-JSON) with an injected
//     platform.Clock timestamp, Go runtime stats, the metrics snapshot, and
//     caller sections.
//   - Local-only pprof: StartPprof serves net/http/pprof on an owner-only
//     0600 UNIX socket inside an owner-validated directory. Never TCP
//     (ADR-0006 / PRD F4 owner-gated local posture).
//
// Tracing is a seam only: the Tracer interface with the NopTracer default;
// real OpenTelemetry wiring is gated on ADR-0007 dependency approval.
//
// Benchmark policy: this package benchmarks only its own hot paths
// (BenchmarkCounterAdd, BenchmarkGaugeSet, BenchmarkDump,
// BenchmarkWithSessionLogger). Subsystem benchmarks — ring append, replay
// cutover, mailbox throughput, PTY fan-out — live in their own packages next
// to the code they measure; they consume this package's counters, not the
// other way around.
package observability
