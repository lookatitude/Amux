// Package render composes backend-provided cell snapshots and pane decorations
// into a deterministic terminal frame (U3). It is dependency-free: it consumes
// internal/tui/model value types and internal/tui/geometry rects and produces a
// Screen (a cell buffer) plus stable text/ANSI serialisations. It never parses
// raw VT bytes and never owns an authoritative grid — cell content and widths
// come straight from the model snapshot (which the daemon derived), so the
// renderer can never disagree with the daemon about a cell.
package render

import (
	"strings"

	"github.com/amux-run/amux/internal/tui/model"
)

// Screen is a mutable Rows×Cols cell buffer the renderer draws into. Cells hold
// model.Cell values so wide/combining content round-trips without width
// recomputation. Out-of-bounds writes are clipped, never panic.
type Screen struct {
	Rows, Cols int
	cells      [][]model.Cell
}

// NewScreen allocates a blank screen. Negative dimensions clamp to zero.
func NewScreen(rows, cols int) *Screen {
	if rows < 0 {
		rows = 0
	}
	if cols < 0 {
		cols = 0
	}
	cells := make([][]model.Cell, rows)
	for r := range cells {
		row := make([]model.Cell, cols)
		for c := range row {
			row[c] = model.Blank()
		}
		cells[r] = row
	}
	return &Screen{Rows: rows, Cols: cols, cells: cells}
}

// At returns the cell at (x,y) or a blank cell when out of bounds.
func (s *Screen) At(x, y int) model.Cell {
	if y < 0 || y >= s.Rows || x < 0 || x >= s.Cols {
		return model.Blank()
	}
	return s.cells[y][x]
}

// Set writes a cell at (x,y), clipping out-of-bounds. When the cell is a wide
// head (Width 2) it also writes a Width-0 spacer to the right so the buffer
// never leaves a stale glyph under a wide cell (geometry-safe wide handling).
func (s *Screen) Set(x, y int, c model.Cell) {
	if y < 0 || y >= s.Rows || x < 0 || x >= s.Cols {
		return
	}
	s.cells[y][x] = c
	if c.Width == 2 && x+1 < s.Cols {
		s.cells[y][x+1] = model.Cell{Width: 0, Style: c.Style}
	}
}

// SetRune writes a single-width rune-string cell with style st.
func (s *Screen) SetRune(x, y int, content string, st model.Style) {
	s.Set(x, y, model.Cell{Content: content, Width: 1, Style: st})
}

// DrawText writes s runewise starting at (x,y), advancing by each rune's
// display width; it stops at the right edge. Wide graphemes consume two
// columns. Used for borders, titles, and status text — never for grid content.
func (s *Screen) DrawText(x, y int, text string, st model.Style) int {
	col := x
	for _, g := range graphemes(text) {
		if col >= s.Cols {
			break
		}
		w := displayWidth(g)
		if w == 0 {
			w = 1
		}
		s.Set(col, y, model.Cell{Content: g, Width: uint8(w), Style: st})
		col += w
	}
	return col - x
}

// PlainString renders the screen to text with no styling: one line per row,
// blank cells as spaces, wide-cell spacers omitted (the wide head already
// occupies two display columns). Trailing blanks on each line are trimmed for
// stable, diff-friendly goldens.
func (s *Screen) PlainString() string {
	var b strings.Builder
	for y := 0; y < s.Rows; y++ {
		var line strings.Builder
		for x := 0; x < s.Cols; x++ {
			c := s.cells[y][x]
			if c.Width == 0 {
				continue // spacer half of a wide cell
			}
			if c.Content == "" {
				line.WriteByte(' ')
				continue
			}
			line.WriteString(c.Content)
		}
		b.WriteString(strings.TrimRight(line.String(), " "))
		if y < s.Rows-1 {
			b.WriteByte('\n')
		}
	}
	return b.String()
}
