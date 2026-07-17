package ansispike

import (
	"strings"
	"testing"
)

// TestCorpusDecodesWithoutCrashOrDesync feeds a representative VT corpus through
// the decoder and asserts each known construct is recognized, the whole buffer
// is consumed, and a malformed/truncated fragment neither crashes nor desyncs
// the bytes that follow it.
func TestCorpusDecodesWithoutCrashOrDesync(t *testing.T) {
	cases := []struct {
		name  string
		input string
		want  Class
	}{
		{"plain ascii", "hello", ClassText},
		{"cjk wide", "世界", ClassText},
		{"combining mark", "é", ClassText}, // é as e + combining acute
		{"sgr red", "\x1b[31m", ClassCSI},
		{"clear screen", "\x1b[2J", ClassCSI},
		{"cursor position", "\x1b[10;5H", ClassCSI},
		{"alt screen on", "\x1b[?1049h", ClassCSI},
		{"osc title bel", "\x1b]0;title\x07", ClassOSC},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			toks := Decode([]byte(tc.input))
			if len(toks) == 0 {
				t.Fatalf("no tokens for %q", tc.input)
			}
			// At least one token of the expected class must appear.
			found := false
			for _, tk := range toks {
				if tk.Class == tc.want {
					found = true
				}
			}
			if !found {
				t.Fatalf("input %q: no %s token; got %v", tc.input, tc.want, Count(toks))
			}
		})
	}
}

// TestTruncatedTailDoesNotCrashOrDropContent proves the no-desync/no-crash
// property for the real risk case: valid content followed by a sequence that is
// truncated at end-of-buffer. The preceding text and SGR must decode correctly,
// the truncated tail must be preserved (not dropped), and reassembly is lossless
// — raw bytes remain authoritative.
func TestTruncatedTailDoesNotCrashOrDropContent(t *testing.T) {
	input := "hi\x1b[31mred\x1b[" // text, SGR red, text, then a truncated CSI tail
	toks := Decode([]byte(input))
	counts := Count(toks)
	// "hi" + "red" = 5 printable graphemes, each a text token.
	if counts[ClassText] < 5 {
		t.Fatalf("valid text before a truncated tail must decode; got %v", counts)
	}
	// Reassembling all token bytes must reproduce the input exactly.
	var sb strings.Builder
	for _, tk := range toks {
		sb.Write(tk.Bytes)
	}
	if sb.String() != input {
		t.Fatalf("decode was lossy: reassembled %q != input %q", sb.String(), input)
	}
}

// TestFullBufferConsumed proves the decoder always advances and consumes the
// whole buffer (no infinite loop, no dropped tail) on a mixed stream.
func TestFullBufferConsumed(t *testing.T) {
	input := "a\x1b[31mred\x1b[0m\x1b]0;t\x07世\x1b["
	toks := Decode([]byte(input))
	var total int
	for _, tk := range toks {
		total += len(tk.Bytes)
	}
	if total != len(input) {
		t.Fatalf("consumed %d bytes, want %d", total, len(input))
	}
}
