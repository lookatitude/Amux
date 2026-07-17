package terminal

import (
	"github.com/charmbracelet/x/ansi"
)

// Decoder bounds. The parser params buffer must exceed maxCSIParamsGuard by a
// safety margin: x/ansi's DecodeSequence (still true at v0.11.7) indexes the
// params buffer at the unguarded running separator count, so a CSI carrying
// more separators than the buffer holds would panic — the engine detects and
// skips such sequences before decoding (unsupported, never a crash).
const (
	parserParamsSize = 64
	parserDataSize   = 4096

	// maxCSIParamsGuard is the separator count at which a CSI/DCS params
	// section is declared hostile and skipped as unsupported.
	maxCSIParamsGuard = parserParamsSize - 2

	// maxPendingSeq bounds a held-back incomplete CSI/ESC sequence. Beyond it
	// the tail is dropped as unsupported and the remainder is discarded up to
	// the sequence terminator (bounded desync on pathological input only).
	maxPendingSeq = 4 << 10

	// maxPendingString bounds a string sequence (OSC/DCS/APC/PM/SOS) payload.
	// The same rule applies whether the payload arrived in one Feed or many,
	// keeping chunk-size determinism: any string sequence longer than this is
	// counted unsupported and discarded up to its terminator.
	maxPendingString = 64 << 10

	// Unsupported-diagnostics ring bounds.
	maxUnsupportedSamples = 16
	maxSampleBytes        = 64
)

// newParser builds the streaming parser with the engine's bounded buffers.
// x/ansi ≥0.5 replaced the sized-constructor/exported-fields API
// (NewParser(params, data), .Params/.ParamsLen/.Cmd/.Data/.DataLen) with a
// zero-arg constructor plus setters and accessor methods; the buffer sizes and
// decode semantics the engine depends on are unchanged.
func newParser() *ansi.Parser {
	p := ansi.NewParser()
	p.SetParamsSize(parserParamsSize)
	p.SetDataSize(parserDataSize)
	return p
}

// discardMode is the bounded-discard state for oversized sequences.
type discardMode uint8

const (
	discardNone   discardMode = iota
	discardString             // dropping until a string terminator (BEL/ST/CAN/SUB/ESC)
	discardSeq                // dropping CSI/ESC body bytes until a final byte
)

// Feed incrementally decodes raw output bytes into the grid. Bytes may be
// split at arbitrary boundaries mid-sequence or mid-rune: incomplete tails are
// held (bounded) and re-decoded when the next bytes arrive, so any chunking of
// the same stream renders the same grid (see the package determinism
// contract). Feed never panics on arbitrary input.
func (e *Engine) Feed(p []byte) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if len(p) > 0 {
		e.pending = append(e.pending, p...)
	}
	e.drainLocked()
	if len(e.pending) == 0 {
		e.pending = nil
	}
}

// drainLocked decodes complete tokens from pending, leaving any incomplete
// tail for the next Feed.
func (e *Engine) drainLocked() {
	for len(e.pending) > 0 {
		switch e.discard {
		case discardString:
			if !e.drainDiscardStringLocked() {
				return
			}
			continue
		case discardSeq:
			if !e.drainDiscardSeqLocked() {
				return
			}
			continue
		}
		buf := e.pending
		switch {
		case isStringIntro(buf):
			done, hold := e.guardStringLocked(buf)
			if hold {
				return
			}
			if done {
				continue
			}
			// Complete, in-bounds OSC: fall through to the decoder.
		case ansi.HasCsiPrefix(buf):
			done, hold := e.guardCSILocked(buf)
			if hold {
				return
			}
			if done {
				continue
			}
		case partialUTF8Prefix(buf):
			// The tail is a completable multi-byte rune prefix; wait.
			return
		}
		// Hold back a valid-but-incomplete multi-byte rune prefix at the TAIL
		// of the buffer: x/ansi v0.11.7's grapheme segmentation swallows such
		// a tail into the preceding cluster (v0.4.5 stopped the cluster at the
		// last complete rune), which would break chunk-size determinism. The
		// held bytes stay in pending and decode once completed.
		decodeBuf := buf
		if trim := incompleteTailLen(buf); trim > 0 && trim < len(buf) {
			decodeBuf = buf[:len(buf)-trim]
		}
		seq, width, n, newState := ansi.DecodeSequence(decodeBuf, ansi.NormalState, e.parser)
		if n <= 0 {
			// Defensive resync (spike rule: never spin): skip one byte.
			e.noteUnsupportedLocked(buf[:1])
			e.pending = e.pending[1:]
			continue
		}
		if newState != ansi.NormalState && n == len(decodeBuf) {
			// Ran out of input mid-sequence: hold the raw tail, bounded.
			if len(buf) > maxPendingSeq {
				e.noteUnsupportedLocked(buf)
				e.pending = e.pending[:0]
				e.discard = discardSeq
				continue
			}
			return
		}
		e.handleTokenLocked(seq, width)
		e.pending = e.pending[n:]
	}
}

// handleTokenLocked dispatches one complete decoded token.
func (e *Engine) handleTokenLocked(seq []byte, width int) {
	switch {
	case len(seq) == 0:
		return
	case width > 0:
		e.writeGrapheme(string(seq), width)
	case ansi.HasCsiPrefix(seq):
		e.handleCSILocked(seq)
	case ansi.HasOscPrefix(seq):
		e.handleOSCLocked(seq)
	case ansi.HasDcsPrefix(seq), ansi.HasApcPrefix(seq), ansi.HasPmPrefix(seq), ansi.HasSosPrefix(seq):
		e.noteUnsupportedLocked(seq)
	case len(seq) == 1:
		e.handleControlLocked(seq[0])
	case ansi.HasEscPrefix(seq):
		e.handleESCLocked(seq)
	default:
		// Zero-width printable cluster: combining marks joining the
		// previously written cell.
		e.combine(string(seq))
	}
}

// handleControlLocked executes C0 (and the few modeled C1) control bytes.
// Unmodeled bare control bytes are a deterministic no-op — they are not
// counted as unsupported sequences (only escape-introduced sequences are).
func (e *Engine) handleControlLocked(b byte) {
	switch b {
	case 0x07: // BEL
		e.bell++
	case 0x08: // BS
		if e.cur.Col > 0 {
			e.cur.Col--
		}
		e.cur.WrapNext = false
	case 0x09: // TAB: fixed stops every 8 columns (HTS/TBC are deferred)
		e.cur.Col = min((e.cur.Col/8+1)*8, e.cols-1)
		e.cur.WrapNext = false
	case 0x0A, 0x0B, 0x0C: // LF, VT, FF
		e.lineFeed()
	case 0x0D: // CR
		e.cur.Col = 0
		e.cur.WrapNext = false
	case 0x84: // C1 IND
		e.lineFeed()
	case 0x85: // C1 NEL
		e.cur.Col = 0
		e.lineFeed()
	case 0x8D: // C1 RI
		e.reverseIndex()
	}
}

// noteUnsupportedLocked counts an unknown/unsupported sequence and retains a
// bounded, truncated raw sample for diagnostics.
func (e *Engine) noteUnsupportedLocked(seq []byte) {
	e.unsupported++
	sample := append([]byte(nil), seq[:min(len(seq), maxSampleBytes)]...)
	if len(e.samples) < maxUnsupportedSamples {
		e.samples = append(e.samples, sample)
		return
	}
	e.samples[e.samplesStart] = sample
	e.samplesStart = (e.samplesStart + 1) % maxUnsupportedSamples
	e.samplesOverflow = true
}

// isStringIntro reports whether buf starts a string-type sequence
// (OSC/DCS/APC/PM/SOS), in either 7-bit (ESC-prefixed) or 8-bit C1 form.
func isStringIntro(buf []byte) bool {
	return ansi.HasOscPrefix(buf) || ansi.HasDcsPrefix(buf) ||
		ansi.HasApcPrefix(buf) || ansi.HasPmPrefix(buf) || ansi.HasSosPrefix(buf)
}

// stringIntroLen returns the intro length: 1 for a C1 byte, 2 for ESC+char.
func stringIntroLen(buf []byte) int {
	if buf[0] == ansi.ESC {
		return 2
	}
	return 1
}

// findStringTerminator scans for a string-sequence terminator from offset
// skip. It returns the number of bytes a manual consumer should take
// (including BEL/ST, excluding a cancelling CAN/SUB/ESC, mirroring the
// decoder) and whether a terminator was found.
func findStringTerminator(buf []byte, skip int) (end int, found bool) {
	for i := skip; i < len(buf); i++ {
		switch buf[i] {
		case 0x07, 0x9C: // BEL, C1 ST
			return i + 1, true
		case 0x18, 0x1A: // CAN, SUB cancel; the byte itself is re-decoded
			return i, true
		case ansi.ESC:
			if i+1 >= len(buf) {
				return 0, false // may become ESC \ ; need more bytes
			}
			if buf[i+1] == '\\' {
				return i + 2, true
			}
			return i, true // ESC cancels the string; re-decode from it
		}
	}
	return 0, false
}

// guardStringLocked pre-handles a string-type sequence at the head of pending.
// DCS/APC/PM/SOS are not modeled: they are consumed whole as unsupported
// without touching the decoder (this also sidesteps the decoder's DCS params
// overflow). OSC within the size bound is left to the decoder (for the title);
// an oversized or unterminated-beyond-bound payload is counted once and
// discarded up to its terminator regardless of chunking.
func (e *Engine) guardStringLocked(buf []byte) (done, hold bool) {
	skip := stringIntroLen(buf)
	if len(buf) < skip {
		return false, true // lone ESC tail
	}
	end, found := findStringTerminator(buf, skip)
	if !found {
		if len(buf)-skip > maxPendingString {
			e.noteUnsupportedLocked(buf)
			e.pending = e.pending[:0]
			e.discard = discardString
			return true, false
		}
		return false, true
	}
	if ansi.HasOscPrefix(buf) && end-skip <= maxPendingString {
		return false, false // decoder path
	}
	e.noteUnsupportedLocked(buf[:end])
	e.pending = e.pending[end:]
	return true, false
}

// scanCSI walks a CSI body counting parameter separators. It reports the
// separator count, the byte length of the sequence (through its final byte,
// or up to an aborting byte), and whether the sequence is complete within buf.
func scanCSI(buf []byte) (seps, end int, complete bool) {
	i := stringIntroLen(buf) // CSI intro sizing matches string intros
	for ; i < len(buf); i++ {
		b := buf[i]
		switch {
		case b >= 0x30 && b <= 0x3F:
			if b == ';' || b == ':' {
				seps++
			}
		case b >= 0x20 && b <= 0x2F: // intermediates, then a final byte
			for i < len(buf) && buf[i] >= 0x20 && buf[i] <= 0x2F {
				i++
			}
			if i >= len(buf) {
				return seps, i, false
			}
			if buf[i] >= 0x40 && buf[i] <= 0x7E {
				return seps, i + 1, true
			}
			return seps, i, true // aborted; the byte is re-decoded
		case b >= 0x40 && b <= 0x7E:
			return seps, i + 1, true
		default:
			return seps, i, true // aborted by a control/invalid byte
		}
	}
	return seps, i, false
}

// guardCSILocked protects the decoder from CSI params overflow (an unguarded
// buffer index — still a panic at x/ansi v0.11.7) and bounds held-back
// incomplete CSI tails.
func (e *Engine) guardCSILocked(buf []byte) (done, hold bool) {
	seps, end, complete := scanCSI(buf)
	if seps < maxCSIParamsGuard {
		if complete {
			return false, false // safe for the decoder
		}
		if len(buf) > maxPendingSeq {
			e.noteUnsupportedLocked(buf)
			e.pending = e.pending[:0]
			e.discard = discardSeq
			return true, false
		}
		return false, true
	}
	// Hostile parameter count: never reaches the decoder.
	if complete {
		e.noteUnsupportedLocked(buf[:end])
		e.pending = e.pending[end:]
		return true, false
	}
	e.noteUnsupportedLocked(buf)
	e.pending = e.pending[:0]
	e.discard = discardSeq
	return true, false
}

// drainDiscardStringLocked drops bytes of an oversized string sequence until
// its terminator. Returns false when pending is exhausted and more input is
// needed.
func (e *Engine) drainDiscardStringLocked() bool {
	buf := e.pending
	for i := 0; i < len(buf); i++ {
		switch buf[i] {
		case 0x07, 0x9C:
			e.pending = buf[i+1:]
			e.discard = discardNone
			return true
		case 0x18, 0x1A:
			e.pending = buf[i:]
			e.discard = discardNone
			return true
		case ansi.ESC:
			if i+1 >= len(buf) {
				e.pending = buf[i:]
				return false // ESC tail; stay discarding
			}
			if buf[i+1] == '\\' {
				e.pending = buf[i+2:]
			} else {
				e.pending = buf[i:]
			}
			e.discard = discardNone
			return true
		}
	}
	e.pending = e.pending[:0]
	return false
}

// drainDiscardSeqLocked drops CSI/ESC body bytes (params + intermediates)
// until a final byte (consumed) or any out-of-band byte (kept for re-decode).
func (e *Engine) drainDiscardSeqLocked() bool {
	buf := e.pending
	for i := 0; i < len(buf); i++ {
		b := buf[i]
		if b >= 0x20 && b <= 0x3F {
			continue
		}
		if b >= 0x40 && b <= 0x7E {
			i++ // the final byte belongs to the discarded sequence
		}
		e.pending = buf[i:]
		e.discard = discardNone
		return true
	}
	e.pending = e.pending[:0]
	return false
}

// incompleteTailLen returns the length of a valid-but-incomplete multi-byte
// UTF-8 rune prefix at the end of buf, or 0 when the tail is a complete rune,
// ASCII, or invalid bytes (which decode deterministically regardless of
// chunking). Used to keep an incomplete rune out of the decoder's grapheme
// clustering (see drainLocked).
func incompleteTailLen(buf []byte) int {
	for back := 1; back <= 3 && back <= len(buf); back++ {
		c := buf[len(buf)-back]
		if c&0xC0 == 0x80 { // continuation byte: keep scanning left
			continue
		}
		var need int
		switch {
		case c >= 0xC2 && c <= 0xDF:
			need = 2
		case c >= 0xE0 && c <= 0xEF:
			need = 3
		case c >= 0xF0 && c <= 0xF4:
			need = 4
		default:
			return 0 // ASCII or invalid lead byte: nothing to hold
		}
		if back < need {
			return back
		}
		return 0
	}
	return 0
}

// partialUTF8Prefix reports whether buf is entirely a valid-but-incomplete
// multi-byte UTF-8 rune prefix that later bytes could complete. Invalid
// encodings return false and flow to the decoder, which handles them
// identically regardless of chunking.
func partialUTF8Prefix(buf []byte) bool {
	c := buf[0]
	var need int
	switch {
	case c >= 0xC2 && c <= 0xDF:
		need = 2
	case c >= 0xE0 && c <= 0xEF:
		need = 3
	case c >= 0xF0 && c <= 0xF4:
		need = 4
	default:
		return false
	}
	if len(buf) >= need {
		return false
	}
	for _, cc := range buf[1:] {
		if cc&0xC0 != 0x80 {
			return false
		}
	}
	return true
}
