package render

import (
	"strings"
	"testing"

	"github.com/amux-run/amux/internal/tui/geometry"
	"github.com/amux-run/amux/internal/tui/model"
)

// snap builds a CellSnapshot from lines of single-width runes (test helper).
func snap(lines ...string) model.CellSnapshot {
	rows := len(lines)
	cols := 0
	for _, l := range lines {
		if n := len([]rune(l)); n > cols {
			cols = n
		}
	}
	cells := make([][]model.Cell, rows)
	for r, l := range lines {
		row := make([]model.Cell, cols)
		for c := range row {
			row[c] = model.Blank()
		}
		for c, ru := range []rune(l) {
			row[c] = model.Cell{Content: string(ru), Width: 1}
		}
		cells[r] = row
	}
	return model.CellSnapshot{Rows: rows, Cols: cols, Cells: cells}
}

func paneView(id string, l geometry.Layout, s model.CellSnapshot) PaneView {
	pl, _ := l.Pane(id)
	return PaneView{Layout: pl, Snapshot: s}
}

func TestSinglePaneBorderAndContent(t *testing.T) {
	l := geometry.Compute(geometry.Leaf("p"), 10, 4, geometry.DefaultConfig())
	pv := paneView("p", l, snap("hi"))
	pv.Title = "sh"
	pv.Focused = true
	sc := Render(4, 10, []PaneView{pv}, Options{Mono: true})
	got := sc.PlainString()
	// Focused heavy border, title in top edge, content "hi" inside.
	if !strings.Contains(got, "hi") {
		t.Fatalf("content missing:\n%s", got)
	}
	if !strings.Contains(got, "sh") {
		t.Fatalf("title missing:\n%s", got)
	}
	lines := strings.Split(got, "\n")
	if len(lines) != 4 {
		t.Fatalf("want 4 rows, got %d:\n%s", len(lines), got)
	}
}

func TestWideCellDoesNotCorruptGeometry(t *testing.T) {
	// A row with a wide CJK head + spacer, then an ASCII 'x'. Total display
	// width must be 3 columns: "世" (2) + "x" (1).
	cells := [][]model.Cell{{
		{Content: "世", Width: 2},
		{Width: 0}, // spacer
		{Content: "x", Width: 1},
	}}
	s := model.CellSnapshot{Rows: 1, Cols: 3, Cells: cells}
	// No border so content maps 1:1 to the screen.
	l := geometry.Compute(geometry.Leaf("p"), 3, 1, Config0())
	pv := paneView("p", l, s)
	sc := Render(1, 3, []PaneView{pv}, Options{Mono: true})
	// Screen: col0 wide head, col1 spacer(width0), col2 'x'.
	if sc.At(0, 0).Content != "世" || sc.At(0, 0).Width != 2 {
		t.Fatalf("wide head misplaced: %+v", sc.At(0, 0))
	}
	if sc.At(1, 0).Width != 0 {
		t.Fatalf("spacer not width0: %+v", sc.At(1, 0))
	}
	if sc.At(2, 0).Content != "x" {
		t.Fatalf("trailing cell shifted: %+v", sc.At(2, 0))
	}
	// PlainString must be exactly "世x" (spacer omitted), width preserved.
	if got := sc.PlainString(); got != "世x" {
		t.Fatalf("plain = %q, want %q", got, "世x")
	}
}

func TestCombiningMarkStaysInOneCell(t *testing.T) {
	// "é" as e + combining acute is one grapheme cluster, width 1.
	combining := "é"
	cells := [][]model.Cell{{
		{Content: combining, Width: 1},
		{Content: "y", Width: 1},
	}}
	s := model.CellSnapshot{Rows: 1, Cols: 2, Cells: cells}
	l := geometry.Compute(geometry.Leaf("p"), 2, 1, Config0())
	pv := paneView("p", l, s)
	sc := Render(1, 2, []PaneView{pv}, Options{Mono: true})
	if sc.At(0, 0).Content != combining {
		t.Fatalf("combining cluster split: %+v", sc.At(0, 0))
	}
	if sc.At(1, 0).Content != "y" {
		t.Fatalf("next cell shifted: %+v", sc.At(1, 0))
	}
}

func TestStoppedStatusRendered(t *testing.T) {
	l := geometry.Compute(geometry.Leaf("p"), 40, 5, geometry.DefaultConfig())
	pv := paneView("p", l, snap("done"))
	pv.Class = model.ClassStopped
	pv.ExitReason = "exit 0"
	sc := Render(5, 40, []PaneView{pv}, Options{Mono: true})
	got := sc.PlainString()
	if !strings.Contains(got, "stopped") || !strings.Contains(got, "exit 0") {
		t.Fatalf("stopped status not shown:\n%s", got)
	}
}

func TestCursorReverseOnFocusedPane(t *testing.T) {
	s := snap("ab")
	s.Cursor = model.Cursor{Row: 0, Col: 1, Visible: true}
	l := geometry.Compute(geometry.Leaf("p"), 2, 1, Config0())
	pv := paneView("p", l, s)
	pv.Focused = true
	pv.ShowCursor = true
	sc := Render(1, 2, []PaneView{pv}, Options{Mono: true})
	if !sc.At(1, 0).Style.Attrs.Has(model.AttrReverse) {
		t.Fatalf("cursor cell not reversed: %+v", sc.At(1, 0))
	}
}

func TestDamageDiffOnlyChangedCells(t *testing.T) {
	a := NewScreen(2, 4)
	a.DrawText(0, 0, "abcd", model.Style{})
	a.DrawText(0, 1, "wxyz", model.Style{})
	b := NewScreen(2, 4)
	b.DrawText(0, 0, "abcd", model.Style{})
	b.DrawText(0, 1, "wXyz", model.Style{}) // one cell changed
	dmg := Diff(a, b)
	if len(dmg) != 1 {
		t.Fatalf("want 1 damage run, got %d: %+v", len(dmg), dmg)
	}
	if dmg[0].Row != 1 || dmg[0].Col != 1 || len(dmg[0].Cells) != 1 {
		t.Fatalf("unexpected damage %+v", dmg[0])
	}
	if dmg[0].Cells[0].Content != "X" {
		t.Fatalf("damage content = %q", dmg[0].Cells[0].Content)
	}
}

func TestDiffSizeChangeIsFullDamage(t *testing.T) {
	a := NewScreen(2, 2)
	b := NewScreen(3, 2)
	dmg := Diff(a, b)
	if len(dmg) != 3 {
		t.Fatalf("size change should be full damage (3 rows), got %d", len(dmg))
	}
}

func TestMonochromeHasNoColor(t *testing.T) {
	st := resolveStyles(Options{Mono: true})
	for _, s := range []model.Style{st.border, st.borderFocus, st.title, st.titleFocus, st.status, st.cursor} {
		if s.FG.Mode != model.ColorDefault || s.BG.Mode != model.ColorDefault {
			t.Fatalf("monochrome style carries color: %+v", s)
		}
	}
}

// Config0 is a borderless min-1 config for content-mapping tests.
func Config0() geometry.Config {
	return geometry.Config{Border: false, MinContentW: 1, MinContentH: 1}
}
