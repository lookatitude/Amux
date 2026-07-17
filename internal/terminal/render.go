package terminal

import (
	"fmt"
	"strings"
)

// RenderSnapshot renders a CellSnapshot in the package's stable plain-text
// golden format. The format is frozen for the testdata/terminal fixtures:
//
//	<Rows lines>   one line per grid row: each cell's grapheme (blank cells
//	               render as a space; wide-cell spacers render nothing, so a
//	               wide grapheme visually covers its two columns), with
//	               trailing spaces trimmed.
//	--             literal separator line.
//	cursor: row=<r> col=<c> visible=<bool> wrapnext=<bool>
//	pen: fg=<color> bg=<color> attrs=<attrs>
//	title: <title>
//	modes: altscreen=<bool> autowrap=<bool> region=<top>..<bottom> rows=<n> cols=<n>
//	bell=<n> unsupported=<n>
//	styles:
//	row=<r> cols=<a>..<b> fg=<color> bg=<color> attrs=<attrs>   (0 or more)
//
// The styles section lists, per row in order, every maximal run of cells
// whose style differs from the default. <color> is "default", "ansi(n)",
// "256(n)", or "rgb(r,g,b)"; <attrs> is "none" or "+"-joined names in the
// fixed order bold, faint, italic, underline, blink, reverse, strike.
func RenderSnapshot(s CellSnapshot) string {
	var b strings.Builder
	for r := 0; r < s.Rows; r++ {
		var line strings.Builder
		for c := 0; c < s.Cols; c++ {
			cell := s.Cells[r][c]
			switch {
			case cell.Width == 0: // spacer: covered by the wide head
			case cell.Content == "":
				line.WriteByte(' ')
			default:
				line.WriteString(cell.Content)
			}
		}
		b.WriteString(strings.TrimRight(line.String(), " "))
		b.WriteByte('\n')
	}
	b.WriteString("--\n")
	fmt.Fprintf(&b, "cursor: row=%d col=%d visible=%t wrapnext=%t\n",
		s.Cursor.Row, s.Cursor.Col, s.Cursor.Visible, s.Cursor.WrapNext)
	fmt.Fprintf(&b, "pen: fg=%s bg=%s attrs=%s\n",
		colorString(s.Pen.FG), colorString(s.Pen.BG), attrsString(s.Pen.Attrs))
	fmt.Fprintf(&b, "title: %s\n", s.Title)
	fmt.Fprintf(&b, "modes: altscreen=%t autowrap=%t region=%d..%d rows=%d cols=%d\n",
		s.AltScreen, s.Autowrap, s.ScrollTop, s.ScrollBottom, s.Rows, s.Cols)
	fmt.Fprintf(&b, "bell=%d unsupported=%d\n", s.BellCount, s.UnsupportedCount)
	b.WriteString("styles:\n")
	for r := 0; r < s.Rows; r++ {
		c := 0
		for c < s.Cols {
			st := s.Cells[r][c].Style
			if st.isDefault() {
				c++
				continue
			}
			run := c
			for run < s.Cols && s.Cells[r][run].Style == st {
				run++
			}
			fmt.Fprintf(&b, "row=%d cols=%d..%d fg=%s bg=%s attrs=%s\n",
				r, c, run-1, colorString(st.FG), colorString(st.BG), attrsString(st.Attrs))
			c = run
		}
	}
	return b.String()
}

func colorString(c Color) string {
	switch c.Mode {
	case ColorANSI:
		return fmt.Sprintf("ansi(%d)", c.Index)
	case Color256:
		return fmt.Sprintf("256(%d)", c.Index)
	case ColorRGB:
		return fmt.Sprintf("rgb(%d,%d,%d)", c.R, c.G, c.B)
	default:
		return "default"
	}
}

func attrsString(a Attr) string {
	if a == 0 {
		return "none"
	}
	names := []struct {
		bit  Attr
		name string
	}{
		{AttrBold, "bold"},
		{AttrFaint, "faint"},
		{AttrItalic, "italic"},
		{AttrUnderline, "underline"},
		{AttrBlink, "blink"},
		{AttrReverse, "reverse"},
		{AttrStrike, "strike"},
	}
	var parts []string
	for _, n := range names {
		if a&n.bit != 0 {
			parts = append(parts, n.name)
		}
	}
	return strings.Join(parts, "+")
}
