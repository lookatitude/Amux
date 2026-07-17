package terminal

// ColorMode discriminates the Color payload.
type ColorMode uint8

// Color modes: terminal default, the 16 ANSI colors, the 256-color palette,
// and 24-bit RGB — the full SGR color surface the MVP models.
const (
	ColorDefault ColorMode = iota // no explicit color; Index/R/G/B unused
	ColorANSI                     // Index 0..15 (30-37/90-97 and 40-47/100-107)
	Color256                      // Index 0..255 (SGR 38;5;n / 48;5;n)
	ColorRGB                      // R/G/B (SGR 38;2;r;g;b / 48;2;r;g;b)
)

// Color is one foreground or background color. The zero value is the
// terminal default.
type Color struct {
	Mode    ColorMode
	Index   uint8 // ColorANSI (0..15) and Color256 (0..255)
	R, G, B uint8 // ColorRGB
}

// Attr is a bitmask of SGR text attributes.
type Attr uint16

// The SGR attributes the MVP grid models.
const (
	AttrBold Attr = 1 << iota
	AttrFaint
	AttrItalic
	AttrUnderline
	AttrBlink
	AttrReverse
	AttrStrike
)

// Style is the SGR pen state applied to written cells. The zero value is the
// default style.
type Style struct {
	FG, BG Color
	Attrs  Attr
}

// isDefault reports whether s is the zero (default) style.
func (s Style) isDefault() bool { return s == Style{} }

// Cell is one grid cell.
//
//   - Width 1: a normal cell. Content is the grapheme cluster occupying it, or
//     "" for a blank cell (rendered as a space).
//   - Width 2: the head of a wide (e.g. CJK) grapheme; the following cell is
//     its spacer.
//   - Width 0: the spacer half of a wide cell; Content is empty and Style
//     mirrors the head.
type Cell struct {
	Content string
	Width   uint8
	Style   Style
}

// blankCell is the erased/empty cell value.
func blankCell() Cell { return Cell{Width: 1} }

// Cursor is the engine cursor state.
type Cursor struct {
	Row, Col int
	// Visible tracks DECTCEM (CSI ?25 h/l).
	Visible bool
	// WrapNext is the pending-wrap flag: the cursor sits on the last column
	// after filling it, and the next printable wraps before writing (DECAWM).
	WrapNext bool
}

// CellSnapshot is an immutable copy of the derived grid for attach handoff
// (ADR-0004 snapshot-at-N; ADR-0005: the grid is derived, never authoritative).
// Cells always holds Rows slices of exactly Cols cells from the ACTIVE screen
// buffer (primary or alternate, per AltScreen).
type CellSnapshot struct {
	Rows, Cols int
	Cells      [][]Cell
	Cursor     Cursor
	Pen        Style
	Title      string

	// Mode flags.
	AltScreen bool
	Autowrap  bool
	// ScrollTop/ScrollBottom are the inclusive 0-based DECSTBM margins.
	ScrollTop, ScrollBottom int

	BellCount        uint64
	UnsupportedCount uint64
}
