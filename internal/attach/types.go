package attach

import (
	"github.com/amux-run/amux/internal/ordering"
	"github.com/amux-run/amux/internal/terminal"
)

// DefaultBuffer bounds an attachment's live delivery buffer (frames). A slower
// consumer than this depth is disconnected with a receipt rather than blocking
// the surface or buffering without bound (ADR-0004 slow-consumer boundary).
const DefaultBuffer = 256

// ClientID identifies an attached/writing client. It aliases the ordering
// package's type so the lease state machine and the attach manager speak the
// same identity without conversion.
type ClientID = ordering.ClientID

// Frame is one ordered raw-output frame delivered over Attachment.Frames(): the
// output sequence number and an immutable byte slice owned by the receiver.
// Replay frames (seq <= N) and live frames (seq > N) share this type and one
// contiguous, duplicate-free channel (ADR-0004 attach cutover).
type Frame struct {
	Seq  uint64
	Data []byte
}

// PaneMeta is the surface metadata delivered with the attach snapshot. It is
// derived from the surface identity and the cell snapshot captured at N, so a
// fresh attach always reflects the size/title in effect at cutover.
type PaneMeta struct {
	SurfaceID string
	Rows      int
	Cols      int
	Title     string
}

// ReplayGap is the explicit replay_gap boundary reported in an AttachSnapshot
// when the ring had already evicted the client's requested floor (ADR-0004:
// gaps are typed and visible, never a silent bridge). RequestedFrom is the
// floor the client asked to replay from; OldestRetained is where replay
// actually started; LatestSeq is the newest sequence at cutover (== the
// snapshot's UpToSeq).
type ReplayGap struct {
	RequestedFrom  uint64
	OldestRetained uint64
	LatestSeq      uint64
}

// AttachSnapshot is the atomic handoff delivered before any raw frame
// (ADR-0004): the derived cell snapshot and pane metadata captured at output
// sequence UpToSeq (== N) under the surface lock. When non-nil, ReplayGap tells
// the client its raw replay began mid-history at ReplayGap.OldestRetained rather
// than at the requested floor.
type AttachSnapshot struct {
	Cell      terminal.CellSnapshot
	Pane      PaneMeta
	UpToSeq   uint64
	ReplayGap *ReplayGap
}

// AttachOptions configures one Attach call.
type AttachOptions struct {
	// FromSeq is the raw-replay floor. 0 means "everything still retained".
	// A floor older than the oldest retained sequence yields a ReplayGap in
	// the snapshot and replay starting at OldestRetainedSeq.
	FromSeq uint64
	// Buffer bounds the live delivery buffer (default DefaultBuffer).
	Buffer int
}

// LeaseNotice is the lifecycle event emitted on every input-lease transition
// (acquired / taken-over / released / disconnected) via the surface's injected
// callback (ADR-0004: all lease transitions are evented). Prev names the prior
// holder (the client that lost the lease on a takeover or that released it);
// Holder names the new holder, empty when the lease is now free.
type LeaseNotice struct {
	Surface string
	Kind    ordering.LeaseEvent
	Holder  ClientID
	Prev    ClientID
}

// SurfaceConfig wires one Surface.
type SurfaceConfig struct {
	// ID is the surface identity carried in PaneMeta and LeaseNotice.
	ID string
	// Ring is the raw-output authority (required). OnOutput appends here.
	Ring *terminal.Ring
	// Snapshot returns the current derived cell snapshot. nil yields an empty
	// snapshot (raw replay remains authoritative, ADR-0005).
	Snapshot func() terminal.CellSnapshot
	// InputSink is the seam to the PTY supervisor. Write forwards to it only
	// for the lease holder. nil accepts and discards holder writes (a no-op
	// sink), still rejecting non-holders.
	InputSink func([]byte) error
	// OnLease receives every lease transition. nil disables lease events.
	OnLease func(LeaseNotice)
	// Buffer overrides the default live buffer depth for this surface's
	// attachments (may still be overridden per-Attach).
	Buffer int
}
