// Package rpcapi is the shared method contract between the daemon (which
// serves these methods over internal/protocol) and every client (CLI and TUI
// over internal/client). It is a pure, dependency-light package: method name
// constants plus the strictly-decoded request/response payload types for the
// 20 required CLI flows (spec "Required CLI flow contract") and their
// supporting methods. Keeping the wire method names and payload shapes in one
// inward package means the daemon and CLI can never disagree about a method's
// name or its params/result — the single-authority rule applied to the RPC
// surface (ADR-0001: no CLI-only mutation path; both call the same method).
//
// Durable request payloads decode strictly (api/v1.DecodeStrict): an unknown
// field is a contract violation, not an ignorable extension (ADR-0003 §Unknown
// field policy). Result payloads are revision-bearing where a mutation
// committed, so a client can correlate a response with the event stream.
package rpcapi

import "encoding/json"

// Method names. The dotted namespace groups a command family; the CLI command
// tree mirrors it. Adding or renaming a method is a visible contract change.
const (
	MethodDaemonHealth   = "daemon.health"
	MethodDaemonShutdown = "daemon.shutdown"

	MethodSessionCreate  = "session.create"
	MethodSessionList    = "session.list"
	MethodSessionDestroy = "session.destroy"

	MethodWorkspaceCreate  = "workspace.create"
	MethodWorkspaceList    = "workspace.list"
	MethodWorkspaceRename  = "workspace.rename"
	MethodWorkspaceDestroy = "workspace.destroy"

	MethodPaneSplit   = "pane.split"
	MethodPaneFocus   = "pane.focus"
	MethodPaneResize  = "pane.resize"
	MethodPaneClose   = "pane.close"
	MethodPaneInspect = "pane.inspect"

	MethodSurfaceSpawn   = "surface.spawn"
	MethodSurfaceSelect  = "surface.select"
	MethodSurfaceStop    = "surface.stop"
	MethodSurfaceRestart = "surface.restart"

	MethodInputSend    = "input.send"
	MethodInputRelease = "input.release"
	MethodReplayRead   = "replay.read"

	MethodSnapshotSave    = "snapshot.save"
	MethodSnapshotRestore = "snapshot.restore"

	MethodHookList    = "hook.list"
	MethodHookApprove = "hook.approve"
	MethodHookDeny    = "hook.deny"
	MethodHookRevoke  = "hook.revoke"

	MethodNotificationList = "notification.list"
	MethodNotificationRead = "notification.read"

	MethodDiagnosticsDump = "diagnostics.dump"

	// Streaming methods. These open a stream (internal/client.Stream): the
	// daemon emits frames until the client disconnects or the stream ends.
	MethodAttach         = "attach"
	MethodEventSubscribe = "event.subscribe"
)

// --- daemon -----------------------------------------------------------------

// HealthResult reports daemon liveness and identity (flow 1 verification).
type HealthResult struct {
	BootID        string `json:"boot_id"`
	Version       string `json:"version"`
	Protocol      string `json:"protocol"`
	Sessions      int    `json:"sessions"`
	UptimeMS      int64  `json:"uptime_ms"`
	StartedUnixMS int64  `json:"started_unix_ms"`
}

// ShutdownResult acknowledges a clean shutdown request (flow 2). The daemon
// stops accepting connections and reaps every PTY before exiting.
type ShutdownResult struct {
	Accepted bool `json:"accepted"`
}

// --- sessions ---------------------------------------------------------------

// SessionCreateParams creates a session (flow 3).
type SessionCreateParams struct {
	Name string `json:"name,omitempty"`
}

// SessionInfo is one registry entry.
type SessionInfo struct {
	ID        string `json:"id"`
	Name      string `json:"name,omitempty"`
	CreatedMS int64  `json:"created_ms"`
}

// SessionCreateResult returns the new session.
type SessionCreateResult struct {
	Session SessionInfo `json:"session"`
}

// SessionListResult lists sessions (flow 4).
type SessionListResult struct {
	Sessions []SessionInfo `json:"sessions"`
}

// SessionDestroyParams destroys a session and its whole graph.
type SessionDestroyParams struct {
	Session string `json:"session"`
}

// --- workspaces -------------------------------------------------------------

// WorkspaceCreateParams creates a workspace (flow 5).
type WorkspaceCreateParams struct {
	Session      string `json:"session"`
	Name         string `json:"name,omitempty"`
	PrimaryRoot  string `json:"primary_root,omitempty"`
	FirstPaneCwd string `json:"first_pane_cwd,omitempty"`
}

// WorkspaceInfo summarizes a workspace.
type WorkspaceInfo struct {
	ID          string `json:"id"`
	Name        string `json:"name,omitempty"`
	PrimaryRoot string `json:"primary_root,omitempty"`
	Rev         uint64 `json:"rev"`
	PaneCount   int    `json:"pane_count"`
	Focused     string `json:"focused"`
}

// RevResult carries the session revision a mutation committed at, so the
// client can correlate with the event stream (ADR-0003 revision-bearing
// results).
type RevResult struct {
	Rev uint64 `json:"rev"`
}

// WorkspaceCreateResult returns the created workspace + first pane/surface.
type WorkspaceCreateResult struct {
	Workspace    string `json:"workspace"`
	FirstPane    string `json:"first_pane"`
	FirstSurface string `json:"first_surface"`
	Rev          uint64 `json:"rev"`
}

// WorkspaceListParams lists a session's workspaces (flow 6).
type WorkspaceListParams struct {
	Session string `json:"session"`
}

// WorkspaceListResult is the workspace list.
type WorkspaceListResult struct {
	Workspaces []WorkspaceInfo `json:"workspaces"`
}

// WorkspaceRenameParams renames a workspace.
type WorkspaceRenameParams struct {
	Session   string `json:"session"`
	Workspace string `json:"workspace"`
	Name      string `json:"name"`
}

// WorkspaceDestroyParams removes a workspace and its subtree.
type WorkspaceDestroyParams struct {
	Session   string `json:"session"`
	Workspace string `json:"workspace"`
}

// --- panes ------------------------------------------------------------------

// Orientation is the split direction on the wire ("horizontal"|"vertical").
type Orientation string

const (
	OrientHorizontal Orientation = "horizontal"
	OrientVertical   Orientation = "vertical"
)

// PaneSplitParams splits a pane (flows 7 and 8).
type PaneSplitParams struct {
	Session     string      `json:"session"`
	Workspace   string      `json:"workspace"`
	Target      string      `json:"target"`
	Orientation Orientation `json:"orientation"`
	Ratio       float64     `json:"ratio,omitempty"`
	NewPaneCwd  string      `json:"new_pane_cwd,omitempty"`
}

// PaneSplitResult returns the new pane/surface + revision.
type PaneSplitResult struct {
	NewPane    string  `json:"new_pane"`
	NewSurface string  `json:"new_surface"`
	Ratio      float64 `json:"ratio"`
	Rev        uint64  `json:"rev"`
}

// PaneFocusParams focuses a pane (flow 9).
type PaneFocusParams struct {
	Session   string `json:"session"`
	Workspace string `json:"workspace"`
	Pane      string `json:"pane"`
}

// PaneResizeParams resizes a pane's parent split (flow 10).
type PaneResizeParams struct {
	Session   string  `json:"session"`
	Workspace string  `json:"workspace"`
	Pane      string  `json:"pane"`
	Ratio     float64 `json:"ratio"`
}

// PaneCloseParams closes a pane.
type PaneCloseParams struct {
	Session   string `json:"session"`
	Workspace string `json:"workspace"`
	Pane      string `json:"pane"`
}

// PaneInspectParams inspects a pane's state (flow 15).
type PaneInspectParams struct {
	Session   string `json:"session"`
	Workspace string `json:"workspace"`
	Pane      string `json:"pane"`
}

// SurfaceInfo describes one surface.
type SurfaceInfo struct {
	ID     string `json:"id"`
	Title  string `json:"title,omitempty"`
	Active bool   `json:"active"`
	// Class is the restore classification when known ("live"|"restarted"|
	// "stopped"|""), so inspect never implies process resurrection.
	Class      string `json:"class,omitempty"`
	ExitReason string `json:"exit_reason,omitempty"`
}

// PaneInspectResult is a pane's full observable state.
type PaneInspectResult struct {
	Pane      string        `json:"pane"`
	Cwd       string        `json:"cwd,omitempty"`
	Project   string        `json:"project,omitempty"`
	Focused   bool          `json:"focused"`
	Surfaces  []SurfaceInfo `json:"surfaces"`
	Active    string        `json:"active"`
	LatestSeq uint64        `json:"latest_seq"`
}

// --- surfaces ---------------------------------------------------------------

// SurfaceSpawnParams spawns a terminal surface on a pane (flow 11).
type SurfaceSpawnParams struct {
	Session   string   `json:"session"`
	Workspace string   `json:"workspace"`
	Pane      string   `json:"pane"`
	Title     string   `json:"title,omitempty"`
	Argv      []string `json:"argv,omitempty"`
	Cwd       string   `json:"cwd,omitempty"`
	Cols      uint16   `json:"cols,omitempty"`
	Rows      uint16   `json:"rows,omitempty"`
	// Env is a non-secret environment allowlist (KEYS only; a value is
	// rejected — ADR-0005 non-secret allowlist).
	Env           []string `json:"env,omitempty"`
	RestartPolicy string   `json:"restart_policy,omitempty"` // "manual"|"automatic"
}

// SurfaceSpawnResult returns the new surface + revision.
type SurfaceSpawnResult struct {
	Surface string `json:"surface"`
	Rev     uint64 `json:"rev"`
}

// SurfaceSelectParams selects a pane's active surface.
type SurfaceSelectParams struct {
	Session   string `json:"session"`
	Workspace string `json:"workspace"`
	Pane      string `json:"pane"`
	Surface   string `json:"surface"`
}

// SurfaceStopParams stops a surface's process (flow 19). Confirm is the
// security-approved confirmation token; a missing confirmation on a
// destructive op fails closed (spec confirmation matrix).
type SurfaceStopParams struct {
	Session   string `json:"session"`
	Workspace string `json:"workspace"`
	Pane      string `json:"pane"`
	Surface   string `json:"surface"`
	Confirm   bool   `json:"confirm"`
}

// SurfaceStopResult reports the terminal exit classification.
type SurfaceStopResult struct {
	Class      string `json:"class"`
	ExitReason string `json:"exit_reason,omitempty"`
}

// SurfaceRestartParams restarts a stopped surface (flow 18).
type SurfaceRestartParams struct {
	Session   string `json:"session"`
	Workspace string `json:"workspace"`
	Pane      string `json:"pane"`
	Surface   string `json:"surface"`
}

// SurfaceRestartResult reports the new classification.
type SurfaceRestartResult struct {
	Class      string `json:"class"`
	ExitReason string `json:"exit_reason,omitempty"`
}

// --- input & replay ---------------------------------------------------------

// InputSendParams sends input bytes to a surface (flow 13). Data is the raw
// input; the daemon rejects it with not_input_lease_holder unless the caller
// holds the surface's input lease (ADR-0004).
type InputSendParams struct {
	Session string `json:"session"`
	Surface string `json:"surface"`
	LeaseID string `json:"lease_id"`
	DataB64 string `json:"data_b64"`
	// Takeover deliberately transfers a held lease to this holder. It is a
	// destructive transition in the security confirmation matrix, so it fails
	// closed unless Confirm accompanies it; acquisition without Takeover never
	// displaces a holder (ADR-0004).
	Takeover bool `json:"takeover,omitempty"`
	Confirm  bool `json:"confirm,omitempty"`
}

// InputSendResult acknowledges accepted input.
type InputSendResult struct {
	Bytes int `json:"bytes"`
}

// InputReleaseParams releases a held input lease. Releasing a lease the caller
// does not hold is a typed error, never a silent no-op.
type InputReleaseParams struct {
	Session string `json:"session"`
	Surface string `json:"surface"`
	LeaseID string `json:"lease_id"`
}

// ReplayReadParams reads bounded raw replay (flow 14). FromSeq is the cursor;
// a cursor older than the retained window returns a replay_gap error whose
// details are a ReplayGapDetails. MaxBytes bounds the raw DECODED payload of
// one page: 0 means the server default page bound, a positive value caps the
// page at min(max_bytes, server bound) — the returned bytes never exceed the
// caller's ask — and a negative value is a typed invalid_argument. Chunks are
// never split across pages: a positive bound smaller than the next whole
// chunk fails typed (invalid_argument, details ReplayBoundDetails) instead of
// tearing a sequence number.
type ReplayReadParams struct {
	Session  string `json:"session"`
	Surface  string `json:"surface"`
	FromSeq  uint64 `json:"from_seq"`
	MaxBytes int64  `json:"max_bytes,omitempty"`
}

// ReplayChunk is one replay record.
type ReplayChunk struct {
	Seq     uint64 `json:"seq"`
	DataB64 string `json:"data_b64"`
}

// ReplayReadResult carries the replayed chunks and the resulting cursor.
// NextSeq is the first sequence NOT returned: on a partial (bounded) page it
// is last-returned-seq+1, so continuing with from_seq = next_seq pages the
// retained window contiguously with no duplicates; when the page reached the
// newest retained output it is latest_seq+1 (the caller is current). Chunks,
// LatestSeq, and NextSeq all derive from ONE ring snapshot: an empty page
// means the cursor was ahead of LatestSeq at that snapshot, so output
// appended after it always sits at or past next_seq. A sequence evicted
// between pages surfaces as a typed replay_gap on the next call, never as a
// silent skip.
type ReplayReadResult struct {
	Chunks    []ReplayChunk `json:"chunks"`
	NextSeq   uint64        `json:"next_seq"`
	LatestSeq uint64        `json:"latest_seq"`
}

// ReplayGapDetails is the structured `details` payload of a replay_gap error:
// the requested cursor was evicted, and [OldestRetained, LatestSeq] is the
// still-replayable range (OldestRetained 0 when nothing is retained).
// Automation branches on these fields — the human message is diagnostics,
// never a contract (ADR-0003).
type ReplayGapDetails struct {
	FromSeq        uint64 `json:"from_seq"`
	OldestRetained uint64 `json:"oldest_retained"`
	LatestSeq      uint64 `json:"latest_seq"`
}

// ReplayBoundDetails is the structured `details` payload of the typed
// invalid_argument error replay.read returns when a positive max_bytes cannot
// fit even the next whole chunk (chunks are never split). NextChunkBytes is
// the minimum bound that makes progress from this cursor.
type ReplayBoundDetails struct {
	MaxBytes       int64 `json:"max_bytes"`
	NextChunkBytes int64 `json:"next_chunk_bytes"`
}

// --- snapshots --------------------------------------------------------------

// SnapshotSaveParams saves a snapshot (flow 16).
type SnapshotSaveParams struct {
	Session string `json:"session"`
}

// SnapshotSaveResult returns the committed checkpoint.
type SnapshotSaveResult struct {
	CheckpointID string `json:"checkpoint_id"`
	Cursor       uint64 `json:"cursor"`
}

// SnapshotRestoreParams restores a snapshot (flow 17).
type SnapshotRestoreParams struct {
	Session string `json:"session"`
}

// RestoredSurface reports one surface's restore classification.
type RestoredSurface struct {
	Surface string `json:"surface"`
	Class   string `json:"class"`
	Reason  string `json:"reason"`
}

// SnapshotRestoreResult reports the restored session and every surface's class
// (spec success criterion 5: every surface is visibly live|restarted|stopped).
type SnapshotRestoreResult struct {
	Session  string            `json:"session"`
	Cursor   uint64            `json:"cursor"`
	Surfaces []RestoredSurface `json:"surfaces"`
}

// --- hooks ------------------------------------------------------------------

// HookListParams lists hook grants for a project.
type HookListParams struct {
	Project string `json:"project"`
}

// HookGrantInfo summarizes one grant.
type HookGrantInfo struct {
	ID         string   `json:"id"`
	HookID     string   `json:"hook_id"`
	Events     []string `json:"events"`
	Scope      string   `json:"scope"`
	Active     bool     `json:"active"`
	BoundEpoch uint64   `json:"bound_epoch"`
}

// HookListResult is the grant list (active + retained inactive history).
type HookListResult struct {
	Grants []HookGrantInfo `json:"grants"`
}

// HookApproveParams grants project trust (trust-granting; requires the
// security-approved confirmation — a missing confirmation fails closed).
type HookApproveParams struct {
	Project string `json:"project"`
	Confirm bool   `json:"confirm"`
}

// HookDenyParams records an explicit operator denial for a project.
type HookDenyParams struct {
	Project string `json:"project"`
}

// HookRevokeParams revokes project trust (destructive; requires confirmation).
type HookRevokeParams struct {
	Project string `json:"project"`
	Confirm bool   `json:"confirm"`
}

// EpochResult carries a project's post-transition trust epoch.
type EpochResult struct {
	Epoch uint64 `json:"epoch"`
}

// --- notifications ----------------------------------------------------------

// NotificationListParams lists notifications for a session.
type NotificationListParams struct {
	Session    string `json:"session"`
	UnreadOnly bool   `json:"unread_only,omitempty"`
	Limit      int    `json:"limit,omitempty"`
}

// NotificationInfo is one notification.
type NotificationInfo struct {
	ID        string `json:"id"`
	Kind      string `json:"kind"`
	Title     string `json:"title"`
	Body      string `json:"body"`
	CreatedMS int64  `json:"created_ms"`
	Read      bool   `json:"read"`
}

// NotificationListResult is the notification list + unread count.
type NotificationListResult struct {
	Notifications []NotificationInfo `json:"notifications"`
	Unread        int                `json:"unread"`
}

// NotificationReadParams marks a notification read.
type NotificationReadParams struct {
	ID string `json:"id"`
}

// NotificationReadResult acknowledges the read-state change.
type NotificationReadResult struct {
	Read bool `json:"read"`
}

// --- diagnostics --------------------------------------------------------------

// DiagnosticsDumpResult carries the bounded diagnostic document verbatim
// (observability.Dump output: one deterministic JSON object).
type DiagnosticsDumpResult struct {
	Dump json.RawMessage `json:"dump"`
}

// --- streams ----------------------------------------------------------------

// AttachParams opens an attach stream to a surface (flow 12).
//
// Cells (minor 1, additive) requests the derived cell grid in the
// attach_snapshot payload: when true, the payload's "cells" field carries an
// AttachSnapshotCells captured under the attach lock (exact snapshot-at-N,
// ADR-0004). Minor-0 clients never send it and see the unchanged payload.
type AttachParams struct {
	Session string `json:"session"`
	Surface string `json:"surface"`
	FromSeq uint64 `json:"from_seq,omitempty"`
	Cells   bool   `json:"cells,omitempty"`
}

// EventSubscribeParams opens the event stream (flow 20). FromSeq requests
// bounded replay before live; a cursor past the retained window yields a typed
// event_gap boundary the client recovers from with a fresh snapshot.
type EventSubscribeParams struct {
	Session   string `json:"session"`
	Workspace string `json:"workspace,omitempty"`
	FromSeq   uint64 `json:"from_seq,omitempty"`
}
