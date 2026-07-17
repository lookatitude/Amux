package terminal

import (
	"bytes"
	"strings"
	"sync"
	"testing"
)

// mustEngine builds an engine or fails the test.
func mustEngine(t testing.TB, rows, cols int) *Engine {
	t.Helper()
	e, err := NewEngine(rows, cols)
	if err != nil {
		t.Fatal(err)
	}
	return e
}

// checkInvariants asserts the structural grid invariants that must hold after
// ANY input: exact row/col dimensions, cursor in bounds, legal cell widths,
// and consistent wide-cell head/spacer pairing on both screen buffers. The
// fuzz target asserts these after every Feed.
func checkInvariants(t testing.TB, e *Engine) {
	t.Helper()
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.cur.Row < 0 || e.cur.Row >= e.rows || e.cur.Col < 0 || e.cur.Col >= e.cols {
		t.Fatalf("cursor out of bounds: (%d,%d) in %dx%d", e.cur.Row, e.cur.Col, e.rows, e.cols)
	}
	for name, grid := range map[string][][]Cell{"primary": e.primary, "alt": e.alt} {
		if len(grid) != e.rows {
			t.Fatalf("%s: %d rows, want %d", name, len(grid), e.rows)
		}
		for r, row := range grid {
			if len(row) != e.cols {
				t.Fatalf("%s row %d: %d cells, want exactly %d", name, r, len(row), e.cols)
			}
			for c, cell := range row {
				switch cell.Width {
				case 1:
				case 2:
					if c+1 >= e.cols || row[c+1].Width != 0 {
						t.Fatalf("%s (%d,%d): wide head without spacer", name, r, c)
					}
				case 0:
					if c == 0 || row[c-1].Width != 2 {
						t.Fatalf("%s (%d,%d): spacer without wide head", name, r, c)
					}
				default:
					t.Fatalf("%s (%d,%d): illegal width %d", name, r, c, cell.Width)
				}
			}
		}
	}
}

// row renders one row of the active grid as plain text (trailing trimmed).
func row(e *Engine, r int) string {
	snap := e.CellSnapshot()
	var b strings.Builder
	for c := 0; c < snap.Cols; c++ {
		cell := snap.Cells[r][c]
		switch {
		case cell.Width == 0:
		case cell.Content == "":
			b.WriteByte(' ')
		default:
			b.WriteString(cell.Content)
		}
	}
	return strings.TrimRight(b.String(), " ")
}

// TestEngineRejectsInvalidGeometry proves construction and resize fail closed
// on non-positive sizes.
func TestEngineRejectsInvalidGeometry(t *testing.T) {
	if _, err := NewEngine(0, 10); err == nil {
		t.Fatal("NewEngine(0,10) must fail")
	}
	e := mustEngine(t, 2, 2)
	if err := e.Resize(2, 0); err == nil {
		t.Fatal("Resize(2,0) must fail")
	}
}

// TestEnginePendingWrapSemantics proves the DECAWM pending-wrap flag: filling
// a line exactly leaves the cursor on the margin, and a following CR/LF does
// not produce a spurious blank line.
func TestEnginePendingWrapSemantics(t *testing.T) {
	e := mustEngine(t, 4, 5)
	e.Feed([]byte("abcde\r\nxy"))
	if got := row(e, 0); got != "abcde" {
		t.Fatalf("row 0 = %q", got)
	}
	if got := row(e, 1); got != "xy" {
		t.Fatalf("row 1 = %q (spurious wrap line?)", got)
	}
	snap := e.CellSnapshot()
	if snap.Cursor.Row != 1 || snap.Cursor.Col != 2 {
		t.Fatalf("cursor = (%d,%d), want (1,2)", snap.Cursor.Row, snap.Cursor.Col)
	}
	// Without CR, the sixth char wraps.
	e2 := mustEngine(t, 4, 5)
	e2.Feed([]byte("abcdef"))
	if got := row(e2, 1); got != "f" {
		t.Fatalf("wrapped row = %q, want %q", got, "f")
	}
	// With DECAWM off, output sticks to the margin and overwrites.
	e3 := mustEngine(t, 4, 5)
	e3.Feed([]byte("\x1b[?7labcdefg"))
	if got := row(e3, 0); got != "abcdg" {
		t.Fatalf("autowrap-off row = %q, want %q", got, "abcdg")
	}
	if got := row(e3, 1); got != "" {
		t.Fatalf("autowrap-off must not wrap; row 1 = %q", got)
	}
}

// TestEngineEraseVariants covers ED 1/2 and EL 2 (ED 0 / EL 0 / EL 1 are
// covered by the cursor_erase golden fixture).
func TestEngineEraseVariants(t *testing.T) {
	e := mustEngine(t, 3, 6)
	e.Feed([]byte("aaaaaa\r\nbbbbbb\r\ncccccc"))
	e.Feed([]byte("\x1b[2;3H\x1b[1J")) // erase from start through cursor (1,2)
	if got := row(e, 0); got != "" {
		t.Fatalf("ED1 row 0 = %q, want empty", got)
	}
	if got := row(e, 1); got != "   bbb" {
		t.Fatalf("ED1 row 1 = %q, want %q", got, "   bbb")
	}
	if got := row(e, 2); got != "cccccc" {
		t.Fatalf("ED1 row 2 = %q, want untouched", got)
	}
	e.Feed([]byte("\x1b[3;1H\x1b[2K"))
	if got := row(e, 2); got != "" {
		t.Fatalf("EL2 row 2 = %q, want empty", got)
	}
	e.Feed([]byte("\x1b[2J"))
	for r := 0; r < 3; r++ {
		if got := row(e, r); got != "" {
			t.Fatalf("ED2 row %d = %q, want empty", r, got)
		}
	}
	checkInvariants(t, e)
}

// TestEngineScrollRegionOps covers SU/SD/IND/RI/NEL interacting with DECSTBM.
func TestEngineScrollRegionOps(t *testing.T) {
	e := mustEngine(t, 5, 10)
	e.Feed([]byte("r0\r\nr1\r\nr2\r\nr3\r\nr4"))
	e.Feed([]byte("\x1b[2;4r")) // region rows 1..3, cursor homes
	snap := e.CellSnapshot()
	if snap.ScrollTop != 1 || snap.ScrollBottom != 3 {
		t.Fatalf("region = %d..%d, want 1..3", snap.ScrollTop, snap.ScrollBottom)
	}
	if snap.Cursor.Row != 0 || snap.Cursor.Col != 0 {
		t.Fatal("DECSTBM must home the cursor")
	}
	e.Feed([]byte("\x1b[1S")) // scroll region up: r2,r3 move up, blank enters at row 3
	for r, want := range []string{"r0", "r2", "r3", "", "r4"} {
		if got := row(e, r); got != want {
			t.Fatalf("after SU row %d = %q, want %q", r, got, want)
		}
	}
	e.Feed([]byte("\x1b[1T")) // and back down
	for r, want := range []string{"r0", "", "r2", "r3", "r4"} {
		if got := row(e, r); got != want {
			t.Fatalf("after SD row %d = %q, want %q", r, got, want)
		}
	}
	// NEL from the bottom margin scrolls the region only.
	e.Feed([]byte("\x1b[4;1HX\x1bE")) // "X" over "r3" leaves "X3"
	for r, want := range []string{"r0", "r2", "X3", "", "r4"} {
		if got := row(e, r); got != want {
			t.Fatalf("after NEL row %d = %q, want %q", r, got, want)
		}
	}
	// RI at the top margin scrolls the region down.
	e.Feed([]byte("\x1b[2;1H\x1bM"))
	for r, want := range []string{"r0", "", "r2", "X3", "r4"} {
		if got := row(e, r); got != want {
			t.Fatalf("after RI row %d = %q, want %q", r, got, want)
		}
	}
	// Invalid margins (top >= bottom) are ignored.
	e.Feed([]byte("\x1b[4;2r"))
	snap = e.CellSnapshot()
	if snap.ScrollTop != 1 || snap.ScrollBottom != 3 {
		t.Fatalf("invalid DECSTBM changed region to %d..%d", snap.ScrollTop, snap.ScrollBottom)
	}
	checkInvariants(t, e)
}

// TestEngineWideOverwriteSemantics proves the wide-cell pair rules: writing
// over a spacer blanks the head, writing over a head blanks the spacer, and a
// wide char never straddles the right margin.
func TestEngineWideOverwriteSemantics(t *testing.T) {
	e := mustEngine(t, 3, 6)
	e.Feed([]byte("世界"))
	if got := row(e, 0); got != "世界" {
		t.Fatalf("row 0 = %q", got)
	}
	e.Feed([]byte("\x1b[1;2HX")) // overwrite 世's spacer
	if got := row(e, 0); got != " X界" {
		t.Fatalf("spacer overwrite: row 0 = %q, want %q", got, " X界")
	}
	e.Feed([]byte("\x1b[1;3HY")) // overwrite 界's head
	if got := row(e, 0); got != " XY" {
		t.Fatalf("head overwrite: row 0 = %q, want %q", got, " XY")
	}
	// Wide char at the last column wraps rather than straddling the margin.
	e.Feed([]byte("\x1b[2;6H好"))
	if got := row(e, 1); got != "" {
		t.Fatalf("row 1 = %q, want empty (wide wrapped)", got)
	}
	if got := row(e, 2); got != "好" {
		t.Fatalf("row 2 = %q, want %q", got, "好")
	}
	checkInvariants(t, e)
}

// TestEngineCombiningAcrossFeeds proves a combining mark arriving in a later
// chunk joins the previously written cell (grapheme split determinism).
func TestEngineCombiningAcrossFeeds(t *testing.T) {
	e := mustEngine(t, 2, 10)
	e.Feed([]byte("e"))
	e.Feed([]byte("́")) // COMBINING ACUTE ACCENT
	snap := e.CellSnapshot()
	if got := snap.Cells[0][0].Content; got != "é" {
		t.Fatalf("cell content = %q, want %q", got, "é")
	}
	if snap.Cursor.Col != 1 {
		t.Fatalf("combining must not advance the cursor: col = %d", snap.Cursor.Col)
	}
}

// TestEngineAltScreenLifecycle proves 1049 enter/exit: alt content is
// isolated, exit restores the primary grid and saved cursor, and the switch
// invalidates all lines.
func TestEngineAltScreenLifecycle(t *testing.T) {
	e := mustEngine(t, 3, 10)
	e.Feed([]byte("primary"))
	e.Damage() // drain
	e.Feed([]byte("\x1b[?1049h"))
	snap := e.CellSnapshot()
	if !snap.AltScreen {
		t.Fatal("1049h must switch to the alternate screen")
	}
	if got := row(e, 0); got != "" {
		t.Fatalf("alt screen must be cleared on entry; row 0 = %q", got)
	}
	if d := e.Damage(); len(d) != 3 {
		t.Fatalf("alt switch damage = %v, want all 3 lines", d)
	}
	e.Feed([]byte("\x1b[2;1Halt stuff"))
	if got := row(e, 1); got != "alt stuff" {
		t.Fatalf("alt row 1 = %q", got)
	}
	e.Feed([]byte("\x1b[?1049l"))
	snap = e.CellSnapshot()
	if snap.AltScreen {
		t.Fatal("1049l must return to the primary screen")
	}
	if got := row(e, 0); got != "primary" {
		t.Fatalf("primary content lost: row 0 = %q", got)
	}
	if snap.Cursor.Row != 0 || snap.Cursor.Col != 7 {
		t.Fatalf("cursor not restored: (%d,%d), want (0,7)", snap.Cursor.Row, snap.Cursor.Col)
	}
	checkInvariants(t, e)
}

// TestEngineDECSCDECRC proves ESC 7/8 save/restore including the pen, and the
// no-save default (home + default pen).
func TestEngineDECSCDECRC(t *testing.T) {
	e := mustEngine(t, 3, 10)
	e.Feed([]byte("\x1b[31m\x1b[2;5H\x1b7\x1b[1;1H\x1b[0mmoved\x1b8"))
	snap := e.CellSnapshot()
	if snap.Cursor.Row != 1 || snap.Cursor.Col != 4 {
		t.Fatalf("DECRC cursor = (%d,%d), want (1,4)", snap.Cursor.Row, snap.Cursor.Col)
	}
	if snap.Pen.FG != (Color{Mode: ColorANSI, Index: 1}) {
		t.Fatalf("DECRC pen = %+v, want restored red", snap.Pen)
	}
	e2 := mustEngine(t, 3, 10)
	e2.Feed([]byte("\x1b[2;5H\x1b8")) // restore without save
	snap = e2.CellSnapshot()
	if snap.Cursor.Row != 0 || snap.Cursor.Col != 0 {
		t.Fatal("DECRC without a save must home the cursor")
	}
}

// TestEngineDamageTracking proves per-line damage collection resets on read
// and resize invalidates everything.
func TestEngineDamageTracking(t *testing.T) {
	e := mustEngine(t, 4, 10)
	if d := e.Damage(); len(d) != 4 {
		t.Fatalf("initial damage = %v, want all lines", d)
	}
	e.Feed([]byte("hi"))
	if d := e.Damage(); len(d) != 1 || d[0] != 0 {
		t.Fatalf("damage after write = %v, want [0]", d)
	}
	if d := e.Damage(); len(d) != 0 {
		t.Fatalf("damage must reset after collect; got %v", d)
	}
	e.Feed([]byte("\x1b[3;1Hthere"))
	if d := e.Damage(); len(d) != 1 || d[0] != 2 {
		t.Fatalf("damage = %v, want [2]", d)
	}
	if err := e.Resize(3, 8); err != nil {
		t.Fatal(err)
	}
	if d := e.Damage(); len(d) != 3 {
		t.Fatalf("resize damage = %v, want all 3 lines", d)
	}
}

// TestEngineResizePolicy proves the frozen MVP truncate/clamp-without-reflow
// policy: surviving cells keep content, the cursor clamps, the scroll region
// resets, and a truncated wide pair is repaired.
func TestEngineResizePolicy(t *testing.T) {
	e := mustEngine(t, 4, 10)
	e.Feed([]byte("0123456789\r\nab世\r\n\x1b[2;3r\x1b[4;8H"))
	// Note DECSTBM homed the cursor; move it back out.
	e.Feed([]byte("\x1b[4;8H"))
	if err := e.Resize(2, 4); err != nil {
		t.Fatal(err)
	}
	snap := e.CellSnapshot()
	if got := row(e, 0); got != "0123" {
		t.Fatalf("row 0 = %q, want truncated %q", got, "0123")
	}
	// "ab世" at cols 0,1,2-3: the wide head at col 2 keeps its spacer at 3.
	if got := row(e, 1); got != "ab世" {
		t.Fatalf("row 1 = %q, want %q", got, "ab世")
	}
	if snap.Cursor.Row != 1 || snap.Cursor.Col != 3 {
		t.Fatalf("cursor = (%d,%d), want clamped (1,3)", snap.Cursor.Row, snap.Cursor.Col)
	}
	if snap.ScrollTop != 0 || snap.ScrollBottom != 1 {
		t.Fatalf("region = %d..%d, want reset 0..1", snap.ScrollTop, snap.ScrollBottom)
	}
	// Now cut the spacer off: the orphaned head must blank.
	if err := e.Resize(2, 3); err != nil {
		t.Fatal(err)
	}
	if got := row(e, 1); got != "ab" {
		t.Fatalf("row 1 after spacer cut = %q, want %q", got, "ab")
	}
	checkInvariants(t, e)
}

// TestEngineUnsupportedDiagnostics proves unknown sequences are counted with
// bounded, truncated samples and never disturb surrounding output.
func TestEngineUnsupportedDiagnostics(t *testing.T) {
	e := mustEngine(t, 2, 40)
	e.Feed([]byte("ok\x1b[?9999z!"))
	if got := row(e, 0); got != "ok!" {
		t.Fatalf("row 0 = %q; unsupported sequence disturbed output", got)
	}
	if got := e.UnsupportedCount(); got != 1 {
		t.Fatalf("UnsupportedCount = %d, want 1", got)
	}
	samples := e.UnsupportedSamples()
	if len(samples) != 1 || !bytes.Equal(samples[0], []byte("\x1b[?9999z")) {
		t.Fatalf("samples = %q", samples)
	}
	// The sample ring is bounded: newest are retained, oldest dropped.
	for i := 0; i < maxUnsupportedSamples+5; i++ {
		e.Feed([]byte{0x1b, '[', '?', '9', byte('0' + i%10), 'z'})
	}
	samples = e.UnsupportedSamples()
	if len(samples) != maxUnsupportedSamples {
		t.Fatalf("sample ring size = %d, want %d", len(samples), maxUnsupportedSamples)
	}
	if got := e.UnsupportedCount(); got != uint64(1+maxUnsupportedSamples+5) {
		t.Fatalf("UnsupportedCount = %d", got)
	}
	if !bytes.Equal(samples[len(samples)-1], []byte("\x1b[?90z")) {
		t.Fatalf("newest sample = %q", samples[len(samples)-1])
	}
}

// TestEngineHostileParamsNoPanic proves the guard against x/ansi's unguarded
// params indexing (present in v0.4.5 and still at v0.11.7): a CSI with a
// hostile parameter count is skipped as unsupported instead of panicking,
// split across feeds or not.
func TestEngineHostileParamsNoPanic(t *testing.T) {
	var hostile bytes.Buffer
	hostile.WriteString("\x1b[")
	for i := 0; i < 100; i++ {
		hostile.WriteString("1;")
	}
	hostile.WriteString("1m")
	for name, feed := range map[string]func(e *Engine){
		"whole": func(e *Engine) { e.Feed(append(hostile.Bytes(), []byte("after")...)) },
		"split": func(e *Engine) {
			for _, b := range append(hostile.Bytes(), []byte("after")...) {
				e.Feed([]byte{b})
			}
		},
	} {
		e := mustEngine(t, 2, 10)
		feed(e)
		if got := row(e, 0); got != "after" {
			t.Fatalf("%s: row 0 = %q, want %q", name, got, "after")
		}
		if got := e.UnsupportedCount(); got != 1 {
			t.Fatalf("%s: UnsupportedCount = %d, want 1", name, got)
		}
		checkInvariants(t, e)
	}
}

// TestEngineOversizedStringDiscard proves a string sequence beyond the 64 KiB
// bound is counted once and discarded up to its terminator, with the stream
// resuming cleanly — under any chunking (the documented determinism rule).
func TestEngineOversizedStringDiscard(t *testing.T) {
	payload := bytes.Repeat([]byte{'x'}, maxPendingString+512)
	stream := append([]byte("\x1b]0;"), payload...)
	stream = append(stream, 0x07) // BEL terminator
	stream = append(stream, []byte("after")...)
	for name, chunk := range map[string]int{"whole": len(stream), "4k": 4096, "byte": 1} {
		e := mustEngine(t, 2, 10)
		for i := 0; i < len(stream); i += chunk {
			e.Feed(stream[i:min(i+chunk, len(stream))])
		}
		if got := row(e, 0); got != "after" {
			t.Fatalf("%s: row 0 = %q, want %q (payload leaked as text?)", name, got, "after")
		}
		if got := e.Title(); got != "" {
			t.Fatalf("%s: oversized title must be dropped, got %d bytes", name, len(got))
		}
		if got := e.UnsupportedCount(); got != 1 {
			t.Fatalf("%s: UnsupportedCount = %d, want 1", name, got)
		}
	}
}

// TestEngineTitleAndBell covers OSC 0/2 titles (BEL and ST terminated) and
// the bell counter.
func TestEngineTitleAndBell(t *testing.T) {
	e := mustEngine(t, 2, 10)
	e.Feed([]byte("\x1b]0;first\x07"))
	if got := e.Title(); got != "first" {
		t.Fatalf("title = %q", got)
	}
	e.Feed([]byte("\x1b]2;second\x1b\\"))
	if got := e.Title(); got != "second" {
		t.Fatalf("ST-terminated title = %q", got)
	}
	// OSC BEL terminators are sequence bytes, not bells: only the two bare
	// BELs below may count.
	e.Feed([]byte("\x07\x07"))
	if got := e.BellCount(); got != 2 {
		t.Fatalf("BellCount = %d, want 2", got)
	}
}

// TestEngineRIS proves ESC c resets screen state but keeps diagnostics.
func TestEngineRIS(t *testing.T) {
	e := mustEngine(t, 3, 10)
	e.Feed([]byte("\x1b[31mstuff\x1b[2;5r\x1b[?25l\x1b[Zjunk")) // CSI Z = CBT, unsupported
	e.Feed([]byte("\x1bc"))
	snap := e.CellSnapshot()
	if got := row(e, 0); got != "" {
		t.Fatalf("RIS must clear the grid; row 0 = %q", got)
	}
	if !snap.Cursor.Visible || snap.Cursor.Row != 0 || snap.Cursor.Col != 0 {
		t.Fatalf("RIS cursor = %+v", snap.Cursor)
	}
	if !snap.Pen.isDefault() {
		t.Fatalf("RIS pen = %+v", snap.Pen)
	}
	if snap.ScrollTop != 0 || snap.ScrollBottom != 2 {
		t.Fatalf("RIS region = %d..%d", snap.ScrollTop, snap.ScrollBottom)
	}
	if snap.UnsupportedCount != 1 {
		t.Fatalf("diagnostics must survive RIS; count = %d", snap.UnsupportedCount)
	}
}

// TestEngineConcurrentFeedSnapshot races a feeder against snapshot/damage
// readers under -race (the engine is fed by the PTY reader goroutine while
// attach snapshots).
func TestEngineConcurrentFeedSnapshot(t *testing.T) {
	e := mustEngine(t, 10, 40)
	var feeder sync.WaitGroup
	feeder.Add(1)
	go func() {
		defer feeder.Done()
		for i := 0; i < 500; i++ {
			e.Feed([]byte("line of text \x1b[31mred\x1b[0m 世界\r\n"))
		}
	}()
	done := make(chan struct{})
	var readers sync.WaitGroup
	for i := 0; i < 3; i++ {
		readers.Add(1)
		go func() {
			defer readers.Done()
			for {
				select {
				case <-done:
					return
				default:
					snap := e.CellSnapshot()
					if snap.Rows != 10 || len(snap.Cells) != 10 {
						t.Error("snapshot geometry corrupt")
						return
					}
					e.Damage()
					_ = e.Title()
				}
			}
		}()
	}
	feeder.Wait()
	close(done)
	readers.Wait()
	checkInvariants(t, e)
}

// TestSplitRuneClusterBoundary pins the x/ansi ≥0.5 adapter in drainLocked:
// the upstream grapheme segmentation swallows a valid-but-incomplete trailing
// rune prefix into the preceding cluster, so the engine must hold that tail
// back until it completes. Every split point of a CJK pair must render
// identically to the unsplit feed (chunk-size determinism, ADR-0005).
func TestSplitRuneClusterBoundary(t *testing.T) {
	raw := []byte("你好") // two 3-byte wide runes
	whole := mustEngine(t, 2, 10)
	whole.Feed(raw)
	want := RenderSnapshot(whole.CellSnapshot())
	for cut := 1; cut < len(raw); cut++ {
		e := mustEngine(t, 2, 10)
		e.Feed(raw[:cut])
		e.Feed(raw[cut:])
		checkInvariants(t, e)
		if got := RenderSnapshot(e.CellSnapshot()); got != want {
			t.Errorf("cut at %d differs.\n--- got ---\n%s\n--- want ---\n%s", cut, got, want)
		}
	}
	// The tail-hold must not stall: an incomplete prefix that never completes
	// is still surfaced deterministically once invalid bytes follow it.
	e := mustEngine(t, 2, 10)
	e.Feed([]byte{0xE4, 0xBD}) // held: prefix of 你
	e.Feed([]byte{'A'})        // invalidates the prefix; must not be swallowed
	checkInvariants(t, e)
	found := false
	snap := e.CellSnapshot()
	for _, row := range snap.Cells {
		for _, c := range row {
			if c.Content == "A" {
				found = true
			}
		}
	}
	if !found {
		t.Fatalf("printable after an abandoned rune prefix was lost:\n%s", RenderSnapshot(snap))
	}
}
