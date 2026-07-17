package terminal

import (
	"bytes"

	"github.com/charmbracelet/x/ansi"
)

// param returns the i-th CSI parameter, substituting def for missing or
// absent parameters.
func (e *Engine) param(i, def int) int {
	v, _ := e.parser.Param(i, def)
	return v
}

// paramsLen returns the number of parsed CSI/DCS parameters.
func (e *Engine) paramsLen() int {
	return len(e.parser.Params())
}

// countParam is param clamped to at least 1 (the VT "Pn defaults to 1" rule).
func (e *Engine) countParam(i int) int {
	return max(e.param(i, 1), 1)
}

// handleCSILocked dispatches one complete CSI sequence. Anything outside the
// modeled set (unknown finals, intermediates, non-'?' markers) is counted as
// unsupported and skipped without desync.
func (e *Engine) handleCSILocked(seq []byte) {
	cmd := ansi.Cmd(e.parser.Command())
	final := cmd.Final()
	if final == 0 || cmd.Intermediate() != 0 {
		e.noteUnsupportedLocked(seq)
		return
	}
	if m := cmd.Prefix(); m != 0 {
		if m == '?' && (final == 'h' || final == 'l') {
			e.applyPrivateModesLocked(seq, final == 'h')
			return
		}
		e.noteUnsupportedLocked(seq)
		return
	}
	switch final {
	case 'A': // CUU
		e.moveCursorTo(e.cur.Row-e.countParam(0), e.cur.Col)
	case 'B': // CUD
		e.moveCursorTo(e.cur.Row+e.countParam(0), e.cur.Col)
	case 'C': // CUF
		e.moveCursorTo(e.cur.Row, e.cur.Col+e.countParam(0))
	case 'D': // CUB
		e.moveCursorTo(e.cur.Row, e.cur.Col-e.countParam(0))
	case 'E': // CNL
		e.moveCursorTo(e.cur.Row+e.countParam(0), 0)
	case 'F': // CPL
		e.moveCursorTo(e.cur.Row-e.countParam(0), 0)
	case 'G': // CHA (1-based column)
		e.moveCursorTo(e.cur.Row, e.countParam(0)-1)
	case 'H', 'f': // CUP / HVP (1-based row;col)
		e.moveCursorTo(e.countParam(0)-1, e.countParam(1)-1)
	case 'd': // VPA (1-based row)
		e.moveCursorTo(e.countParam(0)-1, e.cur.Col)
	case 'J': // ED 0/1/2 (3 = clear scrollback: no-op, no scrollback here)
		if m := e.param(0, 0); m <= 2 {
			e.eraseDisplay(m)
		} else if m != 3 {
			e.noteUnsupportedLocked(seq)
		}
	case 'K': // EL 0/1/2
		if m := e.param(0, 0); m <= 2 {
			e.eraseLine(m)
		} else {
			e.noteUnsupportedLocked(seq)
		}
	case 'L': // IL
		e.insertLines(e.countParam(0))
	case 'M': // DL
		e.deleteLines(e.countParam(0))
	case '@': // ICH
		e.insertChars(e.countParam(0))
	case 'P': // DCH
		e.deleteChars(e.countParam(0))
	case 'X': // ECH
		e.eraseChars(e.countParam(0))
	case 'S': // SU
		e.scrollUp(e.countParam(0))
	case 'T': // SD
		e.scrollDown(e.countParam(0))
	case 'r': // DECSTBM (1-based top;bottom); invalid margins are ignored
		top, bot := e.countParam(0), e.param(1, e.rows)
		if top >= 1 && top < bot && bot <= e.rows {
			e.top, e.bot = top-1, bot-1
			e.moveCursorTo(0, 0)
		}
	case 's': // SCOSC
		e.saveCursor()
	case 'u': // SCORC
		e.restoreCursor()
	case 'm': // SGR
		e.applySGRLocked(seq)
	default:
		e.noteUnsupportedLocked(seq)
	}
}

// applyPrivateModesLocked handles CSI ? Pm h/l for the modeled DEC private
// modes; unknown modes in the list are counted (once per sequence).
func (e *Engine) applyPrivateModesLocked(seq []byte, set bool) {
	n := e.paramsLen()
	unknown := n == 0
	for i := 0; i < n; i++ {
		switch e.param(i, -1) {
		case 7: // DECAWM
			e.autowrap = set
			if !set {
				e.cur.WrapNext = false
			}
		case 25: // DECTCEM
			e.cur.Visible = set
		case 1047: // alternate screen; cleared on exit
			if set {
				e.enterAlt(false)
			} else {
				e.exitAlt(true)
			}
		case 1048: // save/restore cursor
			if set {
				e.saveCursor()
			} else {
				e.restoreCursor()
			}
		case 1049: // save cursor + alternate screen cleared on entry
			if set {
				e.saveCursor()
				e.enterAlt(true)
			} else {
				e.exitAlt(false)
				e.restoreCursor()
			}
		default:
			unknown = true
		}
	}
	if unknown {
		e.noteUnsupportedLocked(seq)
	}
}

// skipChain advances i past a colon-joined subparameter chain starting at i.
func (e *Engine) skipChain(i int) int {
	params := e.parser.Params()
	for i < len(params)-1 && params[i].HasMore() {
		i++
	}
	return i
}

// applySGRLocked folds one SGR sequence into the pen. Unknown SGR codes are
// skipped (with their colon subparameter chains) and counted once per
// sequence.
func (e *Engine) applySGRLocked(seq []byte) {
	n := e.paramsLen()
	if n == 0 { // bare CSI m = reset
		e.pen = Style{}
		return
	}
	unknown := false
	for i := 0; i < n; i++ {
		switch v := e.param(i, 0); v {
		case 0:
			e.pen = Style{}
		case 1:
			e.pen.Attrs |= AttrBold
		case 2:
			e.pen.Attrs |= AttrFaint
		case 3:
			e.pen.Attrs |= AttrItalic
		case 4:
			e.pen.Attrs |= AttrUnderline
			if e.parser.Params()[i].HasMore() && i+1 < n {
				if e.param(i+1, 1) == 0 { // 4:0 = underline off
					e.pen.Attrs &^= AttrUnderline
				}
				i = e.skipChain(i)
			}
		case 5:
			e.pen.Attrs |= AttrBlink
		case 7:
			e.pen.Attrs |= AttrReverse
		case 9:
			e.pen.Attrs |= AttrStrike
		case 22:
			e.pen.Attrs &^= AttrBold | AttrFaint
		case 23:
			e.pen.Attrs &^= AttrItalic
		case 24:
			e.pen.Attrs &^= AttrUnderline
		case 25:
			e.pen.Attrs &^= AttrBlink
		case 27:
			e.pen.Attrs &^= AttrReverse
		case 29:
			e.pen.Attrs &^= AttrStrike
		case 39:
			e.pen.FG = Color{}
		case 49:
			e.pen.BG = Color{}
		case 38, 48:
			color, adv, ok := e.extendedColor(i)
			if ok {
				if v == 38 {
					e.pen.FG = color
				} else {
					e.pen.BG = color
				}
				i += adv
			} else {
				unknown = true
				i = e.skipChain(i)
			}
		default:
			switch {
			case v >= 30 && v <= 37:
				e.pen.FG = Color{Mode: ColorANSI, Index: uint8(v - 30)}
			case v >= 40 && v <= 47:
				e.pen.BG = Color{Mode: ColorANSI, Index: uint8(v - 40)}
			case v >= 90 && v <= 97:
				e.pen.FG = Color{Mode: ColorANSI, Index: uint8(v - 90 + 8)}
			case v >= 100 && v <= 107:
				e.pen.BG = Color{Mode: ColorANSI, Index: uint8(v - 100 + 8)}
			default:
				unknown = true
				i = e.skipChain(i)
			}
		}
	}
	if unknown {
		e.noteUnsupportedLocked(seq)
	}
}

// extendedColor parses the SGR 38/48 extended color forms at index i:
// ;5;n and ;2;r;g;b (semicolon), :5:n, :2:r:g:b, and :2::r:g:b with a
// colorspace slot (colon). It returns the color, the number of extra
// parameters consumed, and whether the form was recognized.
func (e *Engine) extendedColor(i int) (Color, int, bool) {
	n := e.paramsLen()
	if i+1 >= n {
		return Color{}, 0, false
	}
	clamp255 := func(v int) uint8 { return uint8(min(max(v, 0), 255)) }
	switch e.param(i+1, -1) {
	case 5:
		if i+2 >= n {
			return Color{}, 0, false
		}
		return Color{Mode: Color256, Index: clamp255(e.param(i+2, 0))}, 2, true
	case 2:
		params := e.parser.Params()
		if params[i].HasMore() {
			chain := 0 // colon subparams following the 38/48 introducer
			for j := i; j < n-1 && params[j].HasMore(); j++ {
				chain++
			}
			if chain >= 5 { // 38:2:<colorspace>:r:g:b
				return Color{
					Mode: ColorRGB,
					R:    clamp255(e.param(i+3, 0)),
					G:    clamp255(e.param(i+4, 0)),
					B:    clamp255(e.param(i+5, 0)),
				}, 5, true
			}
			if chain >= 4 { // 38:2:r:g:b
				return Color{
					Mode: ColorRGB,
					R:    clamp255(e.param(i+2, 0)),
					G:    clamp255(e.param(i+3, 0)),
					B:    clamp255(e.param(i+4, 0)),
				}, 4, true
			}
			return Color{}, 0, false
		}
		if i+4 >= n {
			return Color{}, 0, false
		}
		return Color{
			Mode: ColorRGB,
			R:    clamp255(e.param(i+2, 0)),
			G:    clamp255(e.param(i+3, 0)),
			B:    clamp255(e.param(i+4, 0)),
		}, 4, true
	}
	return Color{}, 0, false
}

// handleOSCLocked records OSC 0/2 window titles (exposed via Title). OSC 1
// (icon name) is recognized and ignored; everything else is unsupported.
func (e *Engine) handleOSCLocked(seq []byte) {
	data := e.parser.Data()
	switch e.parser.Command() {
	case 0, 2:
		if idx := bytes.IndexByte(data, ';'); idx >= 0 {
			e.title = string(data[idx+1:])
		}
	case 1:
		// icon name: recognized, not modeled
	default:
		e.noteUnsupportedLocked(seq)
	}
}

// handleESCLocked dispatches bare ESC sequences. Charset designations and
// other intermediates are counted as unsupported (not modeled).
func (e *Engine) handleESCLocked(seq []byte) {
	cmd := ansi.Cmd(e.parser.Command())
	if cmd.Intermediate() != 0 {
		e.noteUnsupportedLocked(seq)
		return
	}
	switch cmd.Final() {
	case '7': // DECSC
		e.saveCursor()
	case '8': // DECRC
		e.restoreCursor()
	case 'D': // IND
		e.lineFeed()
	case 'E': // NEL
		e.cur.Col = 0
		e.lineFeed()
	case 'M': // RI
		e.reverseIndex()
	case 'c': // RIS
		e.reset()
	case '\\': // stray ST (e.g. after a cancelled string): ignore
	default:
		e.noteUnsupportedLocked(seq)
	}
}
