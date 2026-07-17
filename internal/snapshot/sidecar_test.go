package snapshot

import (
	"bytes"
	"encoding/binary"
	"errors"
	"testing"
)

// TestSidecarRoundtrip proves the replay sidecar codec is lossless for a mix of
// chunk shapes, including an empty payload chunk (ADR-0005 replay_sidecar:
// versioned binary, never base64 in JSON).
func TestSidecarRoundtrip(t *testing.T) {
	in := []Chunk{
		{Seq: 1, Data: []byte("hello")},
		{Seq: 2, Data: nil},
		{Seq: 900_000_000_000, Data: bytes.Repeat([]byte{0xAB}, 4096)},
	}
	enc, err := EncodeSidecar(in)
	if err != nil {
		t.Fatalf("EncodeSidecar: %v", err)
	}
	if !bytes.HasPrefix(enc, []byte(sidecarMagic)) {
		t.Fatalf("encoded sidecar missing magic prefix %q", sidecarMagic)
	}
	out, err := DecodeSidecar(enc)
	if err != nil {
		t.Fatalf("DecodeSidecar: %v", err)
	}
	if len(out) != len(in) {
		t.Fatalf("got %d chunks, want %d", len(out), len(in))
	}
	for i := range in {
		if out[i].Seq != in[i].Seq {
			t.Errorf("chunk %d seq = %d, want %d", i, out[i].Seq, in[i].Seq)
		}
		if !bytes.Equal(out[i].Data, in[i].Data) {
			t.Errorf("chunk %d data mismatch", i)
		}
	}
}

// TestSidecarRoundtripEmpty: zero chunks is a valid sidecar (a surface may have
// produced no output yet).
func TestSidecarRoundtripEmpty(t *testing.T) {
	enc, err := EncodeSidecar(nil)
	if err != nil {
		t.Fatalf("EncodeSidecar(nil): %v", err)
	}
	out, err := DecodeSidecar(enc)
	if err != nil {
		t.Fatalf("DecodeSidecar: %v", err)
	}
	if len(out) != 0 {
		t.Fatalf("got %d chunks, want 0", len(out))
	}
}

// TestSidecarEncodeRejectsOversizeChunk: the encoder refuses a chunk over the
// bound rather than producing a file the decoder must refuse.
func TestSidecarEncodeRejectsOversizeChunk(t *testing.T) {
	_, err := EncodeSidecar([]Chunk{{Seq: 1, Data: make([]byte, MaxSidecarChunkBytes+1)}})
	if err == nil {
		t.Fatal("EncodeSidecar accepted a chunk over MaxSidecarChunkBytes")
	}
}

// TestSidecarDecodeBadMagic fails closed on a wrong or missing magic.
func TestSidecarDecodeBadMagic(t *testing.T) {
	for _, data := range [][]byte{nil, []byte("AMUX"), []byte("AMUXRS02xxxx"), []byte("junkjunkjunk")} {
		if _, err := DecodeSidecar(data); !errors.Is(err, ErrSidecarCorrupt) {
			t.Errorf("DecodeSidecar(%q) err = %v, want ErrSidecarCorrupt", data, err)
		}
	}
}

// TestSidecarDecodeTruncation fails closed when the input is cut anywhere after
// the magic: mid-varint, mid-header, or mid-payload (ADR-0005 reject
// partial/corrupt).
func TestSidecarDecodeTruncation(t *testing.T) {
	enc, err := EncodeSidecar([]Chunk{{Seq: 7, Data: []byte("0123456789")}})
	if err != nil {
		t.Fatal(err)
	}
	for cut := len(sidecarMagic) + 1; cut < len(enc); cut++ {
		if _, err := DecodeSidecar(enc[:cut]); !errors.Is(err, ErrSidecarCorrupt) {
			t.Errorf("truncation at %d: err = %v, want ErrSidecarCorrupt", cut, err)
		}
	}
}

// TestSidecarDecodeOversizeClaim: a header that claims a chunk larger than the
// bound is rejected BEFORE any allocation of that size.
func TestSidecarDecodeOversizeClaim(t *testing.T) {
	var buf bytes.Buffer
	buf.WriteString(sidecarMagic)
	var tmp [binary.MaxVarintLen64]byte
	n := binary.PutUvarint(tmp[:], 1) // seq
	buf.Write(tmp[:n])
	n = binary.PutUvarint(tmp[:], uint64(MaxSidecarChunkBytes)+1) // oversize length claim
	buf.Write(tmp[:n])
	buf.WriteString("tiny")
	if _, err := DecodeSidecar(buf.Bytes()); !errors.Is(err, ErrSidecarCorrupt) {
		t.Fatalf("oversize length claim: err = %v, want ErrSidecarCorrupt", err)
	}
}

// FuzzDecodeSidecar: the decoder never panics and never allocates more chunk
// bytes than the input contains (bounded allocation, ADR-0005 fail-closed).
func FuzzDecodeSidecar(f *testing.F) {
	valid, err := EncodeSidecar([]Chunk{{Seq: 1, Data: []byte("hello")}, {Seq: 2, Data: nil}})
	if err != nil {
		f.Fatal(err)
	}
	f.Add(valid)
	f.Add([]byte(sidecarMagic))
	f.Add([]byte("AMUXRS0"))
	f.Add(append([]byte(sidecarMagic), 0x01, 0xff, 0xff, 0xff, 0xff, 0x7f))
	f.Fuzz(func(t *testing.T, data []byte) {
		chunks, err := DecodeSidecar(data)
		if err != nil {
			return
		}
		total := 0
		for _, c := range chunks {
			if len(c.Data) > MaxSidecarChunkBytes {
				t.Fatalf("decoded chunk of %d bytes, over the %d bound", len(c.Data), MaxSidecarChunkBytes)
			}
			total += len(c.Data)
		}
		if total > len(data) {
			t.Fatalf("decoded %d payload bytes from %d input bytes: allocation not input-bounded", total, len(data))
		}
	})
}
