package ansispike

import "github.com/charmbracelet/x/ansi"

// Class is the coarse category the spike assigns to each decoded token. Amux's
// real cell engine (T4 B6) will act on these; the spike only proves the parser
// surfaces them without crashing or desyncing.
type Class string

const (
	ClassText    Class = "text"    // printable grapheme(s), width > 0
	ClassCSI     Class = "csi"     // CSI control sequence (cursor, SGR, modes)
	ClassOSC     Class = "osc"     // OSC sequence (e.g. window title)
	ClassESC     Class = "esc"     // bare ESC sequence
	ClassDCS     Class = "dcs"     // DCS sequence
	ClassControl Class = "control" // C0/C1 control byte
	ClassUnknown Class = "unknown" // decoded but unclassified / incomplete tail
)

// Token is one decoded unit.
type Token struct {
	Class Class
	Bytes []byte
	Width int
}

// Decode runs the full input through the streaming decoder and returns the
// ordered token stream. It consumes the entire buffer; a trailing incomplete
// sequence is emitted as ClassUnknown rather than dropped, and decoding of bytes
// after any malformed fragment continues normally (no desync).
func Decode(input []byte) []Token {
	// NewParser allocates the Params and Data buffers DecodeSequence writes into.
	// 32 params comfortably cover the corpus; the real engine (T4 B6) sizes
	// these to its bounded-diagnostic budget. SetDataSize(0) grows the OSC/DCS
	// data buffer as needed (the x/ansi ≥0.5 spelling of DataLen = -1).
	p := ansi.NewParser()
	p.SetParamsSize(32)
	p.SetDataSize(0)

	var out []Token
	state := byte(0)
	rest := input
	for len(rest) > 0 {
		seq, width, n, newState := ansi.DecodeSequence(rest, state, p)
		if n <= 0 {
			// Defensive: never spin. Emit the remaining bytes as unknown.
			out = append(out, Token{Class: ClassUnknown, Bytes: append([]byte(nil), rest...)})
			break
		}
		tok := Token{Bytes: append([]byte(nil), seq...), Width: width}
		switch {
		case width > 0:
			tok.Class = ClassText
		case ansi.HasCsiPrefix(seq):
			tok.Class = ClassCSI
		case ansi.HasOscPrefix(seq):
			tok.Class = ClassOSC
		case ansi.HasDcsPrefix(seq):
			tok.Class = ClassDCS
		case ansi.HasEscPrefix(seq):
			tok.Class = ClassESC
		case len(seq) == 1 && seq[0] < 0x20:
			tok.Class = ClassControl
		default:
			tok.Class = ClassUnknown
		}
		out = append(out, tok)
		state = newState
		rest = rest[n:]
	}
	return out
}

// Count tallies tokens by class for compact assertions.
func Count(tokens []Token) map[Class]int {
	m := map[Class]int{}
	for _, t := range tokens {
		m[t.Class]++
	}
	return m
}
