package terminal

import (
	"errors"
	"fmt"
	"sync"

	"github.com/charmbracelet/x/ansi"
)

// ErrInvalidSize rejects non-positive engine geometry (fail closed).
var ErrInvalidSize = errors.New("terminal: rows and cols must be >= 1")

// Engine is the VT cell engine: a derived rows×cols grid rebuilt
// deterministically from raw bytes (ADR-0005 — the raw ring is the authority;
// this grid is replaceable). Feed is streaming-safe: bytes may arrive split at
// arbitrary boundaries mid-sequence. The engine is guarded for concurrent use
// (PTY reader feeding while attach snapshots).
//
// MVP resize policy (frozen): Resize truncates/clamps WITHOUT reflow. Content
// in surviving cells is preserved, the cursor is clamped into bounds, and the
// scroll region resets to the full screen. Reflow is an explicit non-goal of
// the MVP engine.
type Engine struct {
	mu sync.Mutex

	rows, cols int
	primary    [][]Cell
	alt        [][]Cell
	onAlt      bool

	cur   Cursor
	pen   Style
	saved savedCursor

	// Inclusive 0-based DECSTBM scroll margins. A single region is kept
	// across screen-buffer switches (apps reset it on entry; documented MVP
	// simplification).
	top, bot int

	autowrap bool // DECAWM; default on

	title string
	bell  uint64

	// Last printable-written cell, target for trailing combining marks.
	lastRow, lastCol int
	hasLast          bool

	// Streaming decoder state: undecoded tail bytes held until a sequence or
	// rune completes, plus the bounded-discard mode for oversized sequences.
	parser  *ansi.Parser
	pending []byte
	discard discardMode

	// Unsupported-sequence diagnostics: total count plus a bounded ring of
	// the most recent raw samples (never crash or desync; ADR-0004 principle
	// of explicit, visible anomalies).
	unsupported     uint64
	samples         [][]byte
	samplesStart    int
	samplesOverflow bool

	dirty []bool
}

// savedCursor is the DECSC/DECRC (and SCOSC/SCORC, XTerm 1048/1049) slot. A
// single slot is shared between screen buffers (documented MVP simplification).
type savedCursor struct {
	row, col int
	pen      Style
	valid    bool
}

// NewEngine constructs an engine with a blank rows×cols grid, DECAWM on,
// cursor visible at home, and the scroll region spanning the full screen.
func NewEngine(rows, cols int) (*Engine, error) {
	if rows < 1 || cols < 1 {
		return nil, fmt.Errorf("%w: %dx%d", ErrInvalidSize, rows, cols)
	}
	e := &Engine{
		rows:     rows,
		cols:     cols,
		primary:  newGrid(rows, cols),
		alt:      newGrid(rows, cols),
		cur:      Cursor{Visible: true},
		top:      0,
		bot:      rows - 1,
		autowrap: true,
		parser:   newParser(),
		dirty:    make([]bool, rows),
	}
	e.markAllDirtyLocked()
	return e, nil
}

func newGrid(rows, cols int) [][]Cell {
	g := make([][]Cell, rows)
	for r := range g {
		g[r] = newBlankRow(cols)
	}
	return g
}

func newBlankRow(cols int) []Cell {
	row := make([]Cell, cols)
	for c := range row {
		row[c] = blankCell()
	}
	return row
}

// active returns the grid currently on screen.
func (e *Engine) active() [][]Cell {
	if e.onAlt {
		return e.alt
	}
	return e.primary
}

// Resize applies the frozen MVP truncate/clamp-without-reflow policy to both
// screen buffers: surviving cells keep their content, the cursor and saved
// cursor are clamped, the scroll region resets to the full screen, and every
// line is invalidated for damage.
func (e *Engine) Resize(rows, cols int) error {
	if rows < 1 || cols < 1 {
		return fmt.Errorf("%w: %dx%d", ErrInvalidSize, rows, cols)
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if rows == e.rows && cols == e.cols {
		e.markAllDirtyLocked()
		return nil
	}
	e.primary = resizeGrid(e.primary, rows, cols)
	e.alt = resizeGrid(e.alt, rows, cols)
	e.rows, e.cols = rows, cols
	e.cur.Row = min(e.cur.Row, rows-1)
	e.cur.Col = min(e.cur.Col, cols-1)
	e.cur.WrapNext = false
	if e.saved.valid {
		e.saved.row = min(e.saved.row, rows-1)
		e.saved.col = min(e.saved.col, cols-1)
	}
	e.top, e.bot = 0, rows-1
	e.hasLast = false
	e.dirty = make([]bool, rows)
	e.markAllDirtyLocked()
	return nil
}

func resizeGrid(old [][]Cell, rows, cols int) [][]Cell {
	g := make([][]Cell, rows)
	for r := range g {
		g[r] = newBlankRow(cols)
		if r < len(old) {
			copy(g[r], old[r][:min(cols, len(old[r]))])
		}
		fixRowWide(g[r])
	}
	return g
}

// fixRowWide restores the wide-cell pairing invariant after any operation that
// can split a head/spacer pair (erase, insert/delete chars, truncation): an
// orphaned head or spacer becomes a blank cell.
func fixRowWide(row []Cell) {
	for c := range row {
		if row[c].Width == 2 && (c+1 >= len(row) || row[c+1].Width != 0) {
			row[c] = blankCell()
		}
		if row[c].Width == 0 && (c == 0 || row[c-1].Width != 2) {
			row[c] = blankCell()
		}
	}
}

// Size returns the current geometry.
func (e *Engine) Size() (rows, cols int) {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.rows, e.cols
}

// Title returns the last OSC 0/2 window title.
func (e *Engine) Title() string {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.title
}

// BellCount returns the number of BEL characters seen.
func (e *Engine) BellCount() uint64 {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.bell
}

// UnsupportedCount returns the total number of unknown/unsupported sequences
// skipped so far. The engine never crashes or desyncs on them.
func (e *Engine) UnsupportedCount() uint64 {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.unsupported
}

// UnsupportedSamples returns copies of the most recent unsupported raw
// sequences (oldest first), each truncated to a small bounded length. At most
// maxUnsupportedSamples entries are retained.
func (e *Engine) UnsupportedSamples() [][]byte {
	e.mu.Lock()
	defer e.mu.Unlock()
	out := make([][]byte, 0, len(e.samples))
	n := len(e.samples)
	for i := 0; i < n; i++ {
		s := e.samples[(e.samplesStart+i)%n]
		out = append(out, append([]byte(nil), s...))
	}
	return out
}

// Damage returns the sorted 0-based indices of lines whose content changed
// since the previous Damage call, and resets the tracking. Resize, screen
// switches, and full resets invalidate every line.
func (e *Engine) Damage() []int {
	e.mu.Lock()
	defer e.mu.Unlock()
	var out []int
	for r, d := range e.dirty {
		if d {
			out = append(out, r)
			e.dirty[r] = false
		}
	}
	return out
}

// CellSnapshot returns an immutable deep copy of the active grid plus cursor,
// pen, title, and mode flags — the attach handoff payload (ADR-0004).
func (e *Engine) CellSnapshot() CellSnapshot {
	e.mu.Lock()
	defer e.mu.Unlock()
	grid := e.active()
	cells := make([][]Cell, e.rows)
	for r := range cells {
		cells[r] = append([]Cell(nil), grid[r]...)
	}
	return CellSnapshot{
		Rows:             e.rows,
		Cols:             e.cols,
		Cells:            cells,
		Cursor:           e.cur,
		Pen:              e.pen,
		Title:            e.title,
		AltScreen:        e.onAlt,
		Autowrap:         e.autowrap,
		ScrollTop:        e.top,
		ScrollBottom:     e.bot,
		BellCount:        e.bell,
		UnsupportedCount: e.unsupported,
	}
}

// ---- internal grid operations (callers hold e.mu) ----

func (e *Engine) markDirtyLocked(row int) {
	if row >= 0 && row < len(e.dirty) {
		e.dirty[row] = true
	}
}

func (e *Engine) markDirtyRangeLocked(from, to int) {
	for r := from; r <= to; r++ {
		e.markDirtyLocked(r)
	}
}

func (e *Engine) markAllDirtyLocked() {
	e.markDirtyRangeLocked(0, e.rows-1)
}

// clearWidePairAt blanks the partner of a wide pair when position (r, c) is
// about to be overwritten, keeping the pairing invariant.
func (e *Engine) clearWidePairAt(row []Cell, c int) {
	switch row[c].Width {
	case 2:
		if c+1 < len(row) {
			row[c+1] = blankCell()
		}
	case 0:
		if c > 0 {
			row[c-1] = blankCell()
		}
	}
}

// writeGrapheme places one printable grapheme cluster at the cursor, honoring
// pending wrap (DECAWM), wide-cell spacers, and overwrite semantics. Width is
// clamped to 2 (the grid models at most double-width cells).
func (e *Engine) writeGrapheme(s string, width int) {
	if width <= 0 {
		e.combine(s)
		return
	}
	if width > 2 {
		width = 2
	}
	if width == 2 && e.cols < 2 {
		return // a wide cell cannot exist on a 1-column grid; drop
	}
	if e.cur.WrapNext && e.autowrap {
		e.cur.Col = 0
		e.cur.WrapNext = false
		e.lineFeed()
	}
	if width == 2 && e.cur.Col > e.cols-2 {
		if e.autowrap {
			e.cur.Col = 0
			e.lineFeed()
		} else {
			e.cur.Col = e.cols - 2
		}
	}
	r, c := e.cur.Row, e.cur.Col
	row := e.active()[r]
	e.clearWidePairAt(row, c)
	if width == 2 {
		e.clearWidePairAt(row, c+1)
		row[c+1] = Cell{Width: 0, Style: e.pen}
	}
	row[c] = Cell{Content: s, Width: uint8(width), Style: e.pen}
	e.markDirtyLocked(r)
	e.lastRow, e.lastCol, e.hasLast = r, c, true
	if e.cur.Col+width < e.cols {
		e.cur.Col += width
	} else if e.autowrap {
		e.cur.Col = e.cols - 1
		e.cur.WrapNext = true
	} else {
		e.cur.Col = e.cols - 1
	}
}

// combine appends a zero-width cluster (combining marks, ZWJ tails) to the
// most recently written cell. With no target the cluster is dropped — the
// deterministic MVP rule for a mark with no base.
func (e *Engine) combine(s string) {
	if !e.hasLast || e.lastRow >= e.rows || e.lastCol >= e.cols {
		return
	}
	row := e.active()[e.lastRow]
	if row[e.lastCol].Width == 0 {
		return
	}
	row[e.lastCol].Content += s
	e.markDirtyLocked(e.lastRow)
}

// lineFeed implements LF/VT/FF/IND: scroll when at the bottom margin,
// otherwise move down within the screen.
func (e *Engine) lineFeed() {
	e.cur.WrapNext = false
	switch {
	case e.cur.Row == e.bot:
		e.scrollUp(1)
	case e.cur.Row < e.rows-1:
		e.cur.Row++
	}
}

// reverseIndex implements RI: scroll down when at the top margin, otherwise
// move up within the screen.
func (e *Engine) reverseIndex() {
	e.cur.WrapNext = false
	switch {
	case e.cur.Row == e.top:
		e.scrollDown(1)
	case e.cur.Row > 0:
		e.cur.Row--
	}
}

// scrollUp shifts the scroll region up n lines; blank lines enter at the
// bottom margin.
func (e *Engine) scrollUp(n int) {
	size := e.bot - e.top + 1
	n = min(max(n, 1), size)
	grid := e.active()
	copy(grid[e.top:e.bot+1], grid[e.top+n:e.bot+1])
	for r := e.bot - n + 1; r <= e.bot; r++ {
		grid[r] = newBlankRow(e.cols)
	}
	e.markDirtyRangeLocked(e.top, e.bot)
	e.hasLast = false
}

// scrollDown shifts the scroll region down n lines; blank lines enter at the
// top margin.
func (e *Engine) scrollDown(n int) {
	size := e.bot - e.top + 1
	n = min(max(n, 1), size)
	grid := e.active()
	for r := e.bot; r >= e.top+n; r-- {
		grid[r] = grid[r-n]
	}
	for r := e.top; r < e.top+n; r++ {
		grid[r] = newBlankRow(e.cols)
	}
	e.markDirtyRangeLocked(e.top, e.bot)
	e.hasLast = false
}

// eraseInRow blanks columns [from, to] of row r (inclusive), then repairs any
// wide pair split at the boundaries. BCE (erasing with the pen background) is
// a documented MVP deferral: erased cells are default-styled.
func (e *Engine) eraseInRow(r, from, to int) {
	row := e.active()[r]
	from = max(from, 0)
	to = min(to, e.cols-1)
	for c := from; c <= to; c++ {
		row[c] = blankCell()
	}
	fixRowWide(row)
	e.markDirtyLocked(r)
}

// eraseDisplay implements ED 0/1/2 (mode 3, clear-scrollback, is a no-op: the
// engine has no scrollback).
func (e *Engine) eraseDisplay(mode int) {
	grid := e.active()
	switch mode {
	case 0:
		e.eraseInRow(e.cur.Row, e.cur.Col, e.cols-1)
		for r := e.cur.Row + 1; r < e.rows; r++ {
			grid[r] = newBlankRow(e.cols)
		}
		e.markDirtyRangeLocked(e.cur.Row, e.rows-1)
	case 1:
		for r := 0; r < e.cur.Row; r++ {
			grid[r] = newBlankRow(e.cols)
		}
		e.eraseInRow(e.cur.Row, 0, e.cur.Col)
		e.markDirtyRangeLocked(0, e.cur.Row)
	case 2:
		for r := 0; r < e.rows; r++ {
			grid[r] = newBlankRow(e.cols)
		}
		e.markAllDirtyLocked()
	}
	e.hasLast = false
}

// eraseLine implements EL 0/1/2.
func (e *Engine) eraseLine(mode int) {
	switch mode {
	case 0:
		e.eraseInRow(e.cur.Row, e.cur.Col, e.cols-1)
	case 1:
		e.eraseInRow(e.cur.Row, 0, e.cur.Col)
	case 2:
		e.eraseInRow(e.cur.Row, 0, e.cols-1)
	}
}

// insertLines implements IL: shift lines down within the scroll region from
// the cursor row; no-op outside the region. The cursor moves to column 0.
func (e *Engine) insertLines(n int) {
	if e.cur.Row < e.top || e.cur.Row > e.bot {
		return
	}
	n = min(max(n, 1), e.bot-e.cur.Row+1)
	grid := e.active()
	for r := e.bot; r >= e.cur.Row+n; r-- {
		grid[r] = grid[r-n]
	}
	for r := e.cur.Row; r < e.cur.Row+n; r++ {
		grid[r] = newBlankRow(e.cols)
	}
	e.markDirtyRangeLocked(e.cur.Row, e.bot)
	e.cur.Col = 0
	e.cur.WrapNext = false
	e.hasLast = false
}

// deleteLines implements DL: shift lines up within the scroll region from the
// cursor row; no-op outside the region. The cursor moves to column 0.
func (e *Engine) deleteLines(n int) {
	if e.cur.Row < e.top || e.cur.Row > e.bot {
		return
	}
	n = min(max(n, 1), e.bot-e.cur.Row+1)
	grid := e.active()
	copy(grid[e.cur.Row:e.bot+1], grid[e.cur.Row+n:e.bot+1])
	for r := e.bot - n + 1; r <= e.bot; r++ {
		grid[r] = newBlankRow(e.cols)
	}
	e.markDirtyRangeLocked(e.cur.Row, e.bot)
	e.cur.Col = 0
	e.cur.WrapNext = false
	e.hasLast = false
}

// insertChars implements ICH: shift the cursor row right from the cursor by n
// blanks; cells pushed past the margin are lost.
func (e *Engine) insertChars(n int) {
	row := e.active()[e.cur.Row]
	c := e.cur.Col
	n = min(max(n, 1), e.cols-c)
	copy(row[c+n:], row[c:e.cols-n])
	for i := c; i < c+n; i++ {
		row[i] = blankCell()
	}
	fixRowWide(row)
	e.markDirtyLocked(e.cur.Row)
	e.hasLast = false
}

// deleteChars implements DCH: shift the cursor row left from the cursor by n;
// blanks enter at the margin.
func (e *Engine) deleteChars(n int) {
	row := e.active()[e.cur.Row]
	c := e.cur.Col
	n = min(max(n, 1), e.cols-c)
	copy(row[c:], row[c+n:])
	for i := e.cols - n; i < e.cols; i++ {
		row[i] = blankCell()
	}
	fixRowWide(row)
	e.markDirtyLocked(e.cur.Row)
	e.hasLast = false
}

// eraseChars implements ECH: blank n cells from the cursor without shifting.
func (e *Engine) eraseChars(n int) {
	n = min(max(n, 1), e.cols-e.cur.Col)
	e.eraseInRow(e.cur.Row, e.cur.Col, e.cur.Col+n-1)
	e.hasLast = false
}

// moveCursorTo clamps and sets the absolute cursor position (origin mode is
// not modeled; coordinates are screen-absolute).
func (e *Engine) moveCursorTo(row, col int) {
	e.cur.Row = min(max(row, 0), e.rows-1)
	e.cur.Col = min(max(col, 0), e.cols-1)
	e.cur.WrapNext = false
}

// saveCursor implements DECSC / SCOSC / XTerm 1048h.
func (e *Engine) saveCursor() {
	e.saved = savedCursor{row: e.cur.Row, col: e.cur.Col, pen: e.pen, valid: true}
}

// restoreCursor implements DECRC / SCORC / XTerm 1048l. Without a prior save
// it homes the cursor and resets the pen (xterm behavior).
func (e *Engine) restoreCursor() {
	if e.saved.valid {
		e.moveCursorTo(e.saved.row, e.saved.col)
		e.pen = e.saved.pen
	} else {
		e.moveCursorTo(0, 0)
		e.pen = Style{}
	}
}

// enterAlt switches to the alternate screen buffer, optionally clearing it
// (mode 1049). Re-entering while already on the alternate screen is a no-op.
func (e *Engine) enterAlt(clear bool) {
	if e.onAlt {
		return
	}
	e.onAlt = true
	if clear {
		e.alt = newGrid(e.rows, e.cols)
	}
	e.cur.WrapNext = false
	e.hasLast = false
	e.markAllDirtyLocked()
}

// exitAlt switches back to the primary screen, optionally clearing the
// alternate buffer on the way out (mode 1047). No-op on the primary screen.
func (e *Engine) exitAlt(clear bool) {
	if !e.onAlt {
		return
	}
	if clear {
		e.alt = newGrid(e.rows, e.cols)
	}
	e.onAlt = false
	e.cur.WrapNext = false
	e.hasLast = false
	e.markAllDirtyLocked()
}

// reset implements RIS: both buffers blanked, primary screen active, cursor
// homed and visible, default pen, full scroll region, DECAWM on. The title
// and diagnostic counters survive (they are observability, not screen state).
func (e *Engine) reset() {
	e.primary = newGrid(e.rows, e.cols)
	e.alt = newGrid(e.rows, e.cols)
	e.onAlt = false
	e.cur = Cursor{Visible: true}
	e.pen = Style{}
	e.saved = savedCursor{}
	e.top, e.bot = 0, e.rows-1
	e.autowrap = true
	e.hasLast = false
	e.markAllDirtyLocked()
}
