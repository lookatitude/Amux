package terminal

import (
	"bytes"
	"testing"
)

// FuzzEngineFeed feeds arbitrary bytes — in fuzz-chosen geometry and chunk
// sizes, with a mid-stream resize — and asserts the engine never panics and
// the structural grid invariants hold after every Feed (exact row/col counts,
// cursor in bounds, consistent wide-cell spacers). Run the smoke:
//
//	go test ./internal/terminal/ -fuzz FuzzEngineFeed -fuzztime 10s -run xxx
func FuzzEngineFeed(f *testing.F) {
	f.Add([]byte("hello world\r\n"), uint8(6), uint8(20), uint8(3))
	f.Add([]byte("\x1b[31mred\x1b[0m\x1b[2J\x1b[10;5H"), uint8(12), uint8(40), uint8(1))
	f.Add([]byte("\x1b[?1049h世界\x1b[?1049l"), uint8(4), uint8(10), uint8(2))
	f.Add([]byte("\x1b[2;4r\x1b[4;1H\r\nA\x1b[2S\x1bM"), uint8(6), uint8(20), uint8(5))
	f.Add([]byte("\x1b]0;title\x07é\x1b[1;2;3;4;5;6;7;8;9;10m"), uint8(3), uint8(7), uint8(4))
	f.Add([]byte("\x1bP+q\x1b\\\x1b[?9999z\x1b[1@\x1b[2P\x1b[3X"), uint8(2), uint8(2), uint8(1))
	f.Add(bytes.Repeat([]byte("1;"), 80), uint8(5), uint8(5), uint8(9))

	f.Fuzz(func(t *testing.T, data []byte, rows, cols, chunk uint8) {
		e, err := NewEngine(int(rows%24)+1, int(cols%80)+1)
		if err != nil {
			t.Fatal(err)
		}
		step := int(chunk%16) + 1
		half := len(data) / 2
		for i := 0; i < len(data); i += step {
			e.Feed(data[i:min(i+step, len(data))])
			checkInvariants(t, e)
			if i <= half && half < i+step {
				// Mid-stream resize: full invalidation, clamped cursor,
				// truncate-without-reflow — invariants must survive it too.
				if err := e.Resize(int(cols%24)+1, int(rows%80)+1); err != nil {
					t.Fatal(err)
				}
				checkInvariants(t, e)
			}
		}
		// The diagnostics surface must stay bounded regardless of input.
		if n := len(e.UnsupportedSamples()); n > maxUnsupportedSamples {
			t.Fatalf("unsupported sample ring grew to %d", n)
		}
	})
}
