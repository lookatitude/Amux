package snapshot

import (
	"errors"
	"testing"

	"github.com/amux-run/amux/internal/persist"
)

// commitWithSections writes one generation whose replay-sidecar component is
// two concatenated per-surface sidecar streams, returning the loaded
// generation plus the two surface runtimes describing the sections.
func commitWithSections(t *testing.T) (*Loaded, SurfaceRuntime, SurfaceRuntime) {
	t.Helper()
	secA, err := EncodeSidecar([]Chunk{{Seq: 1, Data: []byte("alpha-1")}, {Seq: 2, Data: []byte("alpha-2")}})
	if err != nil {
		t.Fatal(err)
	}
	secB, err := EncodeSidecar([]Chunk{{Seq: 1, Data: []byte("bravo-1")}})
	if err != nil {
		t.Fatal(err)
	}
	sidecar := append(append([]byte(nil), secA...), secB...)

	doc := testGraphDoc()
	srA := SurfaceRuntime{
		Surface:       "srf-a",
		SidecarPath:   ComponentFileName(persist.ComponentReplaySidecar),
		SidecarOffset: 0,
		SidecarLength: int64(len(secA)),
		ReplayNextSeq: 3,
	}
	srB := SurfaceRuntime{
		Surface:       "srf-b",
		SidecarPath:   ComponentFileName(persist.ComponentReplaySidecar),
		SidecarOffset: int64(len(secA)),
		SidecarLength: int64(len(secB)),
		ReplayNextSeq: 2,
	}
	doc.Surfaces = []SurfaceRuntime{srA, srB}
	g, err := EncodeGraph(doc)
	if err != nil {
		t.Fatal(err)
	}

	dir := t.TempDir()
	w := &Writer{}
	if _, err := w.Commit(dir, string(doc.Graph.Session), map[persist.ComponentKind][]byte{
		persist.ComponentGraph:         g,
		persist.ComponentReplaySidecar: sidecar,
	}); err != nil {
		t.Fatalf("Commit: %v", err)
	}
	loaded, _, err := OpenLatest(dir, string(doc.Graph.Session))
	if err != nil {
		t.Fatalf("OpenLatest: %v", err)
	}
	return loaded, loaded.Graph.Surfaces[0], loaded.Graph.Surfaces[1]
}

// Two surfaces share one sidecar component but each section decodes to only
// that surface's own chunks — overlapping per-surface sequence spaces never
// bleed into each other.
func TestSurfaceReplayChunksPartitionsPerSurface(t *testing.T) {
	loaded, srA, srB := commitWithSections(t)

	a, err := loaded.SurfaceReplayChunks(srA)
	if err != nil {
		t.Fatalf("section A: %v", err)
	}
	if len(a) != 2 || string(a[0].Data) != "alpha-1" || string(a[1].Data) != "alpha-2" {
		t.Fatalf("section A decoded %+v", a)
	}
	b, err := loaded.SurfaceReplayChunks(srB)
	if err != nil {
		t.Fatalf("section B: %v", err)
	}
	if len(b) != 1 || b[0].Seq != 1 || string(b[0].Data) != "bravo-1" {
		t.Fatalf("section B decoded %+v", b)
	}
}

// A surface with no recorded section yields no chunks and no error: the gap is
// explicit, never invented.
func TestSurfaceReplayChunksNoSection(t *testing.T) {
	loaded, _, _ := commitWithSections(t)
	chunks, err := loaded.SurfaceReplayChunks(SurfaceRuntime{Surface: "srf-none"})
	if err != nil || chunks != nil {
		t.Fatalf("no-section surface: chunks=%v err=%v", chunks, err)
	}
}

// A recorded section outside the verified component bytes fails closed with
// the sidecar-corruption error rather than decoding garbage.
func TestSurfaceReplayChunksOutOfBoundsFailsClosed(t *testing.T) {
	loaded, srA, _ := commitWithSections(t)
	bad := srA
	bad.SidecarOffset = 1 << 20
	if _, err := loaded.SurfaceReplayChunks(bad); !errors.Is(err, ErrSidecarCorrupt) {
		t.Fatalf("out-of-bounds section must fail closed, got %v", err)
	}
	// A section that slices mid-stream decodes as framing corruption.
	shifted := srA
	shifted.SidecarOffset++
	shifted.SidecarLength--
	if _, err := loaded.SurfaceReplayChunks(shifted); !errors.Is(err, ErrSidecarCorrupt) {
		t.Fatalf("misaligned section must fail closed, got %v", err)
	}
}
