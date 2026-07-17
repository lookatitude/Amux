// Package model defines the immutable, client-facing view models the terminal
// UI renders and the enums it routes on (U1). Every type here is a plain value
// snapshot: the TUI treats them as read-only projections of daemon/client state
// and never mutates authoritative data through them.
//
// The cell types deliberately MIRROR internal/terminal's derived-grid contract
// (ADR-0005: the grid is derived, never authoritative) but do NOT import it.
// That decoupling is intentional — the renderer (internal/tui/render) and the
// pure model layer carry no dependency on the backend VT engine or its ansi
// decoder, so the whole TUI-pure surface builds and is golden-tested without
// the daemon in the graph. internal/tui/clientadapter is the ONE seam that maps
// terminal.CellSnapshot onto model.CellSnapshot; see that package's ask-gate
// note about live cell delivery over the wire.
package model

// ColorMode discriminates a Color payload, mirroring terminal.ColorMode so the
// adapter is a straight field copy.
type ColorMode uint8

const (
	ColorDefault ColorMode = iota // no explicit color
	ColorANSI                     // Index 0..15
	Color256                      // Index 0..255
	ColorRGB                      // R/G/B
)

// Color is one foreground or background color. The zero value is the terminal
// default.
type Color struct {
	Mode    ColorMode
	Index   uint8
	R, G, B uint8
}

// Attr is a bitmask of SGR text attributes.
type Attr uint16

// The SGR attributes the grid models (mirrors terminal.Attr bit order).
const (
	AttrBold Attr = 1 << iota
	AttrFaint
	AttrItalic
	AttrUnderline
	AttrBlink
	AttrReverse
	AttrStrike
)

// Has reports whether attribute a is set.
func (x Attr) Has(a Attr) bool { return x&a != 0 }

// Style is the SGR pen state applied to a cell. The zero value is the default
// style.
type Style struct {
	FG, BG Color
	Attrs  Attr
}

// IsDefault reports whether s is the zero (default) style.
func (s Style) IsDefault() bool { return s == Style{} }

// Cell is one grid cell.
//
//   - Width 1: a normal cell; Content is the grapheme cluster (may combine
//     marks), or "" for blank (rendered as a space).
//   - Width 2: the head of a wide (e.g. CJK) grapheme; the next cell is a
//     Width-0 spacer.
//   - Width 0: the spacer half of a wide cell; Content is empty.
//
// Width is authoritative from the backend snapshot: the renderer honors it and
// never recomputes grapheme widths for grid cells, so it can never disagree
// with the daemon's derived grid (no client-side VT/width ownership).
type Cell struct {
	Content string
	Width   uint8
	Style   Style
}

// Blank is the erased/empty cell value.
func Blank() Cell { return Cell{Width: 1} }

// Cursor is the derived cursor state carried by a snapshot.
type Cursor struct {
	Row, Col int
	Visible  bool
}

// CellSnapshot is an immutable projection of a surface's derived grid at a
// sequence point. Cells holds exactly Rows slices of Cols cells. It is the
// client-facing cell contract the renderer consumes; the TUI never edits it.
type CellSnapshot struct {
	Rows, Cols int
	Cells      [][]Cell
	Cursor     Cursor
	Title      string
	AltScreen  bool
	// UpToSeq is the output sequence the snapshot reflects (attach cutover N or
	// a later refresh point); presentation-only, never a client-owned cursor.
	UpToSeq uint64
}

// Empty reports whether the snapshot carries no grid (no attach handoff yet).
func (s CellSnapshot) Empty() bool { return s.Rows == 0 || s.Cols == 0 || len(s.Cells) == 0 }

// EmptySnapshot returns a blank Rows×Cols snapshot of blank cells. The renderer
// uses it as the deterministic fallback before any backend snapshot arrives so
// geometry never depends on live data.
func EmptySnapshot(rows, cols int) CellSnapshot {
	if rows < 0 {
		rows = 0
	}
	if cols < 0 {
		cols = 0
	}
	cells := make([][]Cell, rows)
	for r := range cells {
		row := make([]Cell, cols)
		for c := range row {
			row[c] = Blank()
		}
		cells[r] = row
	}
	return CellSnapshot{Rows: rows, Cols: cols, Cells: cells}
}
