package render

import (
	"strconv"
	"strings"

	"github.com/amux-run/amux/internal/tui/model"
)

// This file turns a Screen into bytes for a real terminal, and supports the
// damage-aware rendering strategy the performance lane (U7) benchmarks against
// full-frame rendering. Both strategies are pure string builders over the cell
// buffer; neither parses input VT — they only EMIT SGR + text.

// AnsiString renders the whole screen as a positioned, styled ANSI frame:
// home the cursor, then for each row emit styled cells. It always repositions
// per row so a caller can blit it into a fresh alt-screen deterministically.
func (s *Screen) AnsiString() string {
	var b strings.Builder
	b.WriteString("\x1b[H") // cursor home
	var cur model.Style
	first := true
	for y := 0; y < s.Rows; y++ {
		if y > 0 {
			b.WriteString("\r\n")
		}
		b.WriteString("\x1b[K") // clear line
		for x := 0; x < s.Cols; x++ {
			c := s.cells[y][x]
			if c.Width == 0 {
				continue
			}
			if first || c.Style != cur {
				b.WriteString(sgr(c.Style))
				cur = c.Style
				first = false
			}
			b.WriteString(cellGlyph(c))
		}
	}
	b.WriteString(sgr(model.Style{})) // reset at end
	return b.String()
}

// StyledString renders the screen as inline-styled text for embedding in a
// host renderer (Bubble Tea) View: one line per row joined by "\n", each cell's
// SGR style emitted inline, with NO cursor motion or clear-line control codes
// (the host owns positioning). Trailing default-style blanks are trimmed per
// row for compact, stable frames; a reset is emitted whenever the pen leaves
// the default style so a trimmed line never bleeds color into the next.
func (s *Screen) StyledString() string {
	var b strings.Builder
	for y := 0; y < s.Rows; y++ {
		lastNonBlank := -1
		for x := 0; x < s.Cols; x++ {
			c := s.cells[y][x]
			if c.Width == 0 {
				continue
			}
			if !(c.Content == "" && c.Style.IsDefault()) {
				lastNonBlank = x
			}
		}
		var cur model.Style
		first := true
		for x := 0; x <= lastNonBlank; x++ {
			c := s.cells[y][x]
			if c.Width == 0 {
				continue
			}
			if first || c.Style != cur {
				b.WriteString(sgr(c.Style))
				cur = c.Style
				first = false
			}
			b.WriteString(cellGlyph(c))
		}
		if !first && !cur.IsDefault() {
			b.WriteString(sgr(model.Style{})) // reset so a trimmed line does not bleed
		}
		if y < s.Rows-1 {
			b.WriteByte('\n')
		}
	}
	return b.String()
}

// Damage is one changed run on a row: a starting column and the styled cells to
// repaint. The damage-aware strategy emits only these runs.
type Damage struct {
	Row, Col int
	Cells    []model.Cell
}

// Diff computes the row/column runs that differ between prev and s (same
// dimensions required; a size change is full damage). It coalesces adjacent
// changed cells into runs so the emitter writes one cursor move per run.
func Diff(prev, s *Screen) []Damage {
	if prev == nil || prev.Rows != s.Rows || prev.Cols != s.Cols {
		return fullDamage(s)
	}
	var out []Damage
	for y := 0; y < s.Rows; y++ {
		x := 0
		for x < s.Cols {
			if cellsEqual(prev.cells[y][x], s.cells[y][x]) {
				x++
				continue
			}
			start := x
			var run []model.Cell
			for x < s.Cols && !cellsEqual(prev.cells[y][x], s.cells[y][x]) {
				run = append(run, s.cells[y][x])
				x++
			}
			out = append(out, Damage{Row: y, Col: start, Cells: run})
		}
	}
	return out
}

func fullDamage(s *Screen) []Damage {
	out := make([]Damage, 0, s.Rows)
	for y := 0; y < s.Rows; y++ {
		row := make([]model.Cell, s.Cols)
		copy(row, s.cells[y])
		out = append(out, Damage{Row: y, Col: 0, Cells: row})
	}
	return out
}

// EmitDamage serialises damage runs to positioned ANSI, one cursor move per run.
func EmitDamage(dmg []Damage) string {
	var b strings.Builder
	for _, d := range dmg {
		// 1-based cursor address
		b.WriteString("\x1b[")
		b.WriteString(strconv.Itoa(d.Row + 1))
		b.WriteByte(';')
		b.WriteString(strconv.Itoa(d.Col + 1))
		b.WriteByte('H')
		var cur model.Style
		first := true
		for _, c := range d.Cells {
			if c.Width == 0 {
				continue
			}
			if first || c.Style != cur {
				b.WriteString(sgr(c.Style))
				cur = c.Style
				first = false
			}
			b.WriteString(cellGlyph(c))
		}
		b.WriteString(sgr(model.Style{}))
	}
	return b.String()
}

func cellsEqual(a, b model.Cell) bool {
	return a.Content == b.Content && a.Width == b.Width && a.Style == b.Style
}

func cellGlyph(c model.Cell) string {
	if c.Content == "" {
		return " "
	}
	return c.Content
}

// sgr encodes a style as an SGR sequence (reset when default). Colors emit the
// ANSI/256/RGB forms per mode.
func sgr(st model.Style) string {
	if st.IsDefault() {
		return "\x1b[0m"
	}
	parts := []string{"0"}
	if st.Attrs.Has(model.AttrBold) {
		parts = append(parts, "1")
	}
	if st.Attrs.Has(model.AttrFaint) {
		parts = append(parts, "2")
	}
	if st.Attrs.Has(model.AttrItalic) {
		parts = append(parts, "3")
	}
	if st.Attrs.Has(model.AttrUnderline) {
		parts = append(parts, "4")
	}
	if st.Attrs.Has(model.AttrBlink) {
		parts = append(parts, "5")
	}
	if st.Attrs.Has(model.AttrReverse) {
		parts = append(parts, "7")
	}
	if st.Attrs.Has(model.AttrStrike) {
		parts = append(parts, "9")
	}
	parts = append(parts, colorSGR(st.FG, true)...)
	parts = append(parts, colorSGR(st.BG, false)...)
	return "\x1b[" + strings.Join(parts, ";") + "m"
}

func colorSGR(c model.Color, fg bool) []string {
	switch c.Mode {
	case model.ColorANSI:
		base := 30
		if !fg {
			base = 40
		}
		if c.Index < 8 {
			return []string{strconv.Itoa(base + int(c.Index))}
		}
		// bright 8..15 → 90..97 / 100..107
		bright := 90
		if !fg {
			bright = 100
		}
		return []string{strconv.Itoa(bright + int(c.Index-8))}
	case model.Color256:
		lead := "38"
		if !fg {
			lead = "48"
		}
		return []string{lead, "5", strconv.Itoa(int(c.Index))}
	case model.ColorRGB:
		lead := "38"
		if !fg {
			lead = "48"
		}
		return []string{lead, "2", strconv.Itoa(int(c.R)), strconv.Itoa(int(c.G)), strconv.Itoa(int(c.B))}
	default:
		return nil
	}
}
