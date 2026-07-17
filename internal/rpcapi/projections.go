// projections.go is the minor-1 read-only projection contract (T4 handoff to
// T5): the wire shapes that let a client render live terminal cells, the full
// frozen hook-trust presentation, daemon-owned pane context, and the
// authoritative workspace split tree WITHOUT parsing raw VT, deciding trust,
// discovering context locally, or keeping a second layout model. Every type
// here is an immutable projection of backend authority — none of these methods
// mutates durable state, and all payloads are bounded (grids by surface
// geometry, lists by the durable rows that already exist).
package rpcapi

// Projection method names (protocol minor 1, additive — ADR-0003). A minor-0
// peer never calls them; an older daemon answers with a typed error, never a
// silent wrong answer.
const (
	MethodSurfaceCells  = "surface.cells"
	MethodHookInspect   = "hook.inspect"
	MethodPaneContext   = "pane.context"
	MethodWorkspaceTree = "workspace.tree"
)

// --- surface.cells -----------------------------------------------------------

// SurfaceCellsParams requests the derived cell-grid projection of one surface
// (terminal.CellSnapshot on the wire). IfChangedSince is the delta gate: when
// non-zero and the surface's latest raw sequence is not beyond it, the daemon
// answers Unchanged=true with no grid — the cheap poll a client issues after a
// raw_output notification or during gap recovery.
type SurfaceCellsParams struct {
	Session        string `json:"session"`
	Surface        string `json:"surface"`
	IfChangedSince uint64 `json:"if_changed_since,omitempty"`
}

// SurfaceCellsResult carries the projection. UpToSeq is a FLOOR: the grid
// reflects the raw output stream through AT LEAST that sequence (the engine
// may already contain slightly newer bytes; re-rendering newer state early is
// harmless because cells are derived presentation, never sequence authority —
// the raw ring and event stream remain the only cursors a client tracks).
type SurfaceCellsResult struct {
	Surface   string `json:"surface"`
	UpToSeq   uint64 `json:"up_to_seq"`
	Unchanged bool   `json:"unchanged,omitempty"`
	// Grid is nil exactly when Unchanged is true.
	Grid *CellGrid `json:"grid,omitempty"`
}

// CellGrid is the wire form of terminal.CellSnapshot: the ACTIVE screen buffer
// as Rows slices of exactly Cols cells, plus cursor, pen, title, and mode
// flags — everything needed for a faithful initial render and recovery.
// Size is bounded by the surface geometry (Rows×Cols cells, each cell one
// grapheme cluster).
type CellGrid struct {
	Rows   int             `json:"rows"`
	Cols   int             `json:"cols"`
	Cells  [][]SurfaceCell `json:"cells"`
	Cursor CellCursor      `json:"cursor"`
	// Pen is the current SGR pen (nil = default style).
	Pen   *CellStyle `json:"pen,omitempty"`
	Title string     `json:"title,omitempty"`

	AltScreen bool `json:"alt_screen,omitempty"`
	Autowrap  bool `json:"autowrap"`
	// ScrollTop/ScrollBottom are the inclusive 0-based DECSTBM margins.
	ScrollTop    int `json:"scroll_top"`
	ScrollBottom int `json:"scroll_bottom"`

	BellCount        uint64 `json:"bell_count,omitempty"`
	UnsupportedCount uint64 `json:"unsupported_count,omitempty"`
}

// CellCursor mirrors terminal.Cursor.
type CellCursor struct {
	Row      int  `json:"row"`
	Col      int  `json:"col"`
	Visible  bool `json:"visible"`
	WrapNext bool `json:"wrap_next,omitempty"`
}

// SurfaceCell is one grid cell. Width semantics mirror terminal.Cell exactly:
// 1 = normal cell ("" text renders as a blank), 2 = head of a wide (e.g. CJK)
// grapheme, 0 = the spacer half following a wide head.
type SurfaceCell struct {
	Text  string     `json:"t,omitempty"`
	Width uint8      `json:"w"`
	Style *CellStyle `json:"s,omitempty"` // nil = default style
}

// CellStyle is the SGR style of a cell or pen. Attrs is the frozen bitmask
// below; colors are nil when the terminal default applies.
type CellStyle struct {
	FG    *CellColor `json:"fg,omitempty"`
	BG    *CellColor `json:"bg,omitempty"`
	Attrs uint16     `json:"attrs,omitempty"`
}

// Frozen attribute bits (wire contract; mirrors the engine's SGR model).
const (
	CellAttrBold uint16 = 1 << iota
	CellAttrFaint
	CellAttrItalic
	CellAttrUnderline
	CellAttrBlink
	CellAttrReverse
	CellAttrStrike
)

// Cell color modes (wire contract).
const (
	CellColorANSI = "ansi" // Index 0..15
	CellColor256  = "256"  // Index 0..255
	CellColorRGB  = "rgb"  // R/G/B
)

// CellColor is one explicit color; a nil *CellColor means terminal default.
type CellColor struct {
	Mode  string `json:"mode"`
	Index uint8  `json:"index,omitempty"`
	R     uint8  `json:"r,omitempty"`
	G     uint8  `json:"g,omitempty"`
	B     uint8  `json:"b,omitempty"`
}

// AttachSnapshotCells is the additive attach integration: when AttachParams
// carries Cells=true, the attach_snapshot event payload includes a "cells"
// field of this shape — the SAME CellGrid, captured under the attach lock, so
// its up_to_seq is EXACT (snapshot-at-N; replay resumes strictly after N,
// ADR-0004). Clients that did not opt in see the unchanged minor-0 payload.
type AttachSnapshotCells struct {
	UpToSeq uint64   `json:"up_to_seq"`
	Grid    CellGrid `json:"grid"`
}

// --- hook.inspect ------------------------------------------------------------

// HookInspectParams requests the read-only trust projection for a project
// root. Inspection NEVER makes or changes an authorization decision: the
// daemon-side control actor remains the only authority, and a client renders
// exactly what this returns (absent data fails closed as UNAVAILABLE on the
// presentation side).
type HookInspectParams struct {
	Project string `json:"project"`
}

// HookProjectTrust is the project identity + trust state.
type HookProjectTrust struct {
	// Key is the content-addressed project identity (hex SHA-256 of the
	// canonical (realpath, dev, inode) tuple).
	Key  string `json:"key"`
	Root string `json:"root"`
	// State is ""|"approved"|"denied"|"revoked" — only "approved" confers
	// anything; every other value fails closed.
	State string `json:"state"`
	// Epoch is the monotonic trust epoch; grants bound to an older epoch are
	// stale and never authorize.
	Epoch uint64 `json:"epoch"`
}

// HookGrantDetail is the FULL frozen trust presentation for one grant — the
// fields the confirmation UI must show verbatim. Env holds environment KEY
// names only; values never cross the wire (ADR-0005 non-secret allowlist).
type HookGrantDetail struct {
	ID           string   `json:"id"`
	HookID       string   `json:"hook_id"`
	ExecPath     string   `json:"exec_path"`
	ExecSHA256   string   `json:"exec_sha256"`
	ConfigSHA256 string   `json:"config_sha256,omitempty"`
	Events       []string `json:"events"`
	Scope        string   `json:"scope"` // none|fixed|workspace-primary|pane
	FixedPath    string   `json:"fixed_path,omitempty"`
	EnvKeys      []string `json:"env_keys,omitempty"`
	TimeoutMS    int64    `json:"timeout_ms"`
	OutputCap    int64    `json:"output_cap_bytes"`
	BoundEpoch   uint64   `json:"bound_epoch"`
	Active       bool     `json:"active"`
}

// HookInspectResult is the projection: project trust + every grant row
// (active and retained inactive history, mirroring hook.list's window).
type HookInspectResult struct {
	Project HookProjectTrust  `json:"project"`
	Grants  []HookGrantDetail `json:"grants"`
}

// --- pane.context ------------------------------------------------------------

// PaneContextParams requests the daemon-owned pane context (B10 collectors:
// cwd, git, foreground process). The daemon collects; the client only renders
// — there is no UI-local discovery.
type PaneContextParams struct {
	Session   string `json:"session"`
	Workspace string `json:"workspace"`
	Pane      string `json:"pane"`
}

// PaneContextResult mirrors internal/context.PaneContext. Zero-valued fields
// mean "not determined" (collector unavailable on this platform, probe failed,
// or nothing to report) — absence is honest and fails closed; it is never
// fabricated. ExitCode is set only after the active surface's process exited.
type PaneContextResult struct {
	Pane string `json:"pane"`
	Cwd  string `json:"cwd,omitempty"`

	GitRoot   string `json:"git_root,omitempty"`
	GitBranch string `json:"git_branch,omitempty"`
	GitDirty  bool   `json:"git_dirty,omitempty"`

	ForegroundPID int    `json:"foreground_pid,omitempty"`
	ForegroundCmd string `json:"foreground_cmd,omitempty"`
	ExitCode      *int   `json:"exit_code,omitempty"`

	// UpdatedMS is when the daemon collected this snapshot.
	UpdatedMS int64 `json:"updated_ms"`
}

// --- workspace.tree ----------------------------------------------------------

// WorkspaceTreeParams requests the authoritative split tree of one workspace.
type WorkspaceTreeParams struct {
	Session   string `json:"session"`
	Workspace string `json:"workspace"`
}

// TreeNode is one node of the binary split tree. Exactly one of Pane or Split
// is set (leaf or split) — the same invariant the domain enforces.
type TreeNode struct {
	Pane  *TreePane  `json:"pane,omitempty"`
	Split *TreeSplit `json:"split,omitempty"`
}

// TreeSplit is an internal node: Ratio is the fraction of the axis given to
// First (left for horizontal, top for vertical).
type TreeSplit struct {
	Orientation Orientation `json:"orientation"`
	Ratio       float64     `json:"ratio"`
	First       *TreeNode   `json:"first"`
	Second      *TreeNode   `json:"second"`
}

// TreePane is a leaf: one pane with its ordered surfaces.
type TreePane struct {
	ID      string `json:"id"`
	Cwd     string `json:"cwd,omitempty"`
	Project string `json:"project,omitempty"`
	Focused bool   `json:"focused,omitempty"`
	// Active is the pane's active surface ID.
	Active   string        `json:"active"`
	Surfaces []SurfaceInfo `json:"surfaces"`
}

// WorkspaceTreeResult is the projection: geometry-bearing tree plus the
// deterministic orders a renderer needs (left-to-right pane order and the
// recency-ordered focus history, most-recent last). Rev correlates the tree
// with the event stream, so a client knows exactly which mutations it has
// already folded in.
type WorkspaceTreeResult struct {
	Workspace    string    `json:"workspace"`
	Name         string    `json:"name,omitempty"`
	Rev          uint64    `json:"rev"`
	Focused      string    `json:"focused"`
	PaneOrder    []string  `json:"pane_order"`
	FocusHistory []string  `json:"focus_history,omitempty"`
	Root         *TreeNode `json:"root"`
}
