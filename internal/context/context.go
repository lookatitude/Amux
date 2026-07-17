// Package context implements the pane context collectors (ADR-0001 package
// map: "cwd/git/process/agent collectors"; PRD F8). Each pane's context —
// working directory, git root/branch/dirty, foreground process — is discovered
// INDEPENDENTLY per pane: there is deliberately no workspace-wide repository
// assumption, so one workspace can span multiple repositories (or none).
//
// Collection is pull-based and bounded: the Poller debounces per-pane requests
// on the injected platform.Clock and runs collectors on a capped worker pool,
// pushing snapshots through a callback OUTSIDE any actor goroutine. OS probes
// live behind small seams (CwdProber, CommProber, platform.ProcessInspector)
// with Linux /proc implementations and fail-closed placeholders elsewhere
// (ADR-0006; platform.ErrUnsupportedPlatform). The /proc probers' runtime
// behavior is Linux-only evidence deferred to a Linux host — the author host
// exercises the seams with fakes.
//
// Agent adapters (agent.go) are the other collector family: byte-stream
// parsers that surface typed lifecycle/session/attention events from selected
// agent CLIs, with provider parsing kept OUT of the core state model and every
// emitted payload treated as redaction-covered egress (RED-1 context
// "agent_adapter").
package context

// PaneContext is one pane's collected context snapshot. Fields a collector
// could not determine are zero — a snapshot never guesses. ExitCode is non-nil
// only after the pane's process exited.
type PaneContext struct {
	Pane          string
	Cwd           string
	GitRoot       string
	GitBranch     string
	GitDirty      bool
	ForegroundPID int
	ForegroundCmd string
	ExitCode      *int
	UpdatedMS     int64
}
