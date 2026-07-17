package snapshot

import (
	"bytes"
	"encoding/binary"
	"fmt"
)

// sidecarMagic identifies a version-01 Amux replay sidecar. The sidecar is
// versioned BINARY — raw output bytes are never base64-embedded in the graph
// JSON (ADR-0005; PRD F7). A future format bump changes the trailing digits,
// and this decoder refuses the unknown magic rather than guessing.
const sidecarMagic = "AMUXRS01"

// MaxSidecarChunkBytes bounds a single sidecar chunk. The decoder validates
// every declared length against this bound and against the remaining input
// BEFORE allocating, so a corrupt or hostile header cannot force a huge
// allocation (fail-closed decoding).
const MaxSidecarChunkBytes = 8 << 20

// Chunk is one framed run of raw surface output: the ADR-0004 output sequence
// number it starts at, plus the bytes.
type Chunk struct {
	Seq  uint64
	Data []byte
}

// EncodeSidecar serializes chunks as magic + varint-framed records
// (uvarint seq, uvarint length, payload). There is deliberately no per-record
// CRC: the generation manifest's SHA-256 over the whole component file is the
// authoritative integrity check (ADR-0005 "manifest checksum/generation
// selects valid bytes"), so an in-file checksum would only duplicate it.
func EncodeSidecar(chunks []Chunk) ([]byte, error) {
	var buf bytes.Buffer
	buf.WriteString(sidecarMagic)
	var tmp [binary.MaxVarintLen64]byte
	for i, c := range chunks {
		if len(c.Data) > MaxSidecarChunkBytes {
			return nil, fmt.Errorf("amux/snapshot: sidecar chunk %d (seq %d) is %d bytes, over the %d-byte bound", i, c.Seq, len(c.Data), MaxSidecarChunkBytes)
		}
		n := binary.PutUvarint(tmp[:], c.Seq)
		buf.Write(tmp[:n])
		n = binary.PutUvarint(tmp[:], uint64(len(c.Data)))
		buf.Write(tmp[:n])
		buf.Write(c.Data)
	}
	return buf.Bytes(), nil
}

// DecodeSidecar streams the framed chunks back out of data, failing closed
// with ErrSidecarCorrupt on a bad magic, a truncated varint or payload, or an
// oversize length claim — a partial chunk list is never returned (ADR-0005
// reject partial/corrupt). Each chunk's bytes are copied, so the result does
// not alias the input.
func DecodeSidecar(data []byte) ([]Chunk, error) {
	var out []Chunk
	if err := decodeSidecar(data, func(c Chunk) { out = append(out, c) }); err != nil {
		return nil, err
	}
	return out, nil
}

// decodeSidecar is the streaming core: it yields each chunk as it is framed
// out, validating bounds before any allocation.
func decodeSidecar(data []byte, yield func(Chunk)) error {
	if len(data) < len(sidecarMagic) || string(data[:len(sidecarMagic)]) != sidecarMagic {
		return fmt.Errorf("amux/snapshot: bad sidecar magic: %w", ErrSidecarCorrupt)
	}
	rest := data[len(sidecarMagic):]
	for record := 0; len(rest) > 0; record++ {
		seq, n := binary.Uvarint(rest)
		if n <= 0 {
			return fmt.Errorf("amux/snapshot: record %d: truncated or overlong seq varint: %w", record, ErrSidecarCorrupt)
		}
		rest = rest[n:]
		length, n := binary.Uvarint(rest)
		if n <= 0 {
			return fmt.Errorf("amux/snapshot: record %d: truncated or overlong length varint: %w", record, ErrSidecarCorrupt)
		}
		rest = rest[n:]
		if length > MaxSidecarChunkBytes {
			return fmt.Errorf("amux/snapshot: record %d claims %d bytes, over the %d-byte bound: %w", record, length, MaxSidecarChunkBytes, ErrSidecarCorrupt)
		}
		if length > uint64(len(rest)) {
			return fmt.Errorf("amux/snapshot: record %d claims %d bytes but only %d remain (truncated): %w", record, length, len(rest), ErrSidecarCorrupt)
		}
		var payload []byte
		if length > 0 {
			payload = append([]byte(nil), rest[:length]...)
		}
		rest = rest[length:]
		yield(Chunk{Seq: seq, Data: payload})
	}
	return nil
}
