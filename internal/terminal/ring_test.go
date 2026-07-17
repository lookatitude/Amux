package terminal

import (
	"bytes"
	"errors"
	"fmt"
	"sync"
	"testing"
)

// TestRingBudgetFloor proves construction fails closed below the 16 MiB
// replay floor and never silently clamps.
func TestRingBudgetFloor(t *testing.T) {
	if _, err := NewRing(MinRetentionBytes - 1); !errors.Is(err, ErrBudgetTooSmall) {
		t.Fatalf("budget below floor: got err %v, want ErrBudgetTooSmall", err)
	}
	if _, err := NewRing(MinRetentionBytes); err != nil {
		t.Fatalf("budget at floor must construct: %v", err)
	}
}

// TestRingAppendSequencing proves the monotonic, gap-free sequence contract:
// first chunk = 1, each append +1, accessors track the range.
func TestRingAppendSequencing(t *testing.T) {
	r, err := NewRing(MinRetentionBytes)
	if err != nil {
		t.Fatal(err)
	}
	if got := r.LatestSeq(); got != 0 {
		t.Fatalf("LatestSeq before append = %d, want 0", got)
	}
	if got := r.OldestRetainedSeq(); got != 0 {
		t.Fatalf("OldestRetainedSeq before append = %d, want 0", got)
	}
	for i := 1; i <= 5; i++ {
		seq, err := r.Append([]byte{byte(i)})
		if err != nil {
			t.Fatal(err)
		}
		if seq != uint64(i) {
			t.Fatalf("append %d: seq = %d, want %d", i, seq, i)
		}
	}
	if got := r.LatestSeq(); got != 5 {
		t.Fatalf("LatestSeq = %d, want 5", got)
	}
	if got := r.OldestRetainedSeq(); got != 1 {
		t.Fatalf("OldestRetainedSeq = %d, want 1", got)
	}
	if got := r.RetainedBytes(); got != 5 {
		t.Fatalf("RetainedBytes = %d, want 5", got)
	}
}

// TestRingAppendCopiesData proves chunks are immutable copies: mutating the
// caller's buffer after Append must not affect replayed bytes.
func TestRingAppendCopiesData(t *testing.T) {
	r := mustRing(t)
	buf := []byte("original")
	if _, err := r.Append(buf); err != nil {
		t.Fatal(err)
	}
	copy(buf, "CLOBBER!")
	chunks, err := r.Replay(1)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(chunks[0].Data, []byte("original")) {
		t.Fatalf("chunk mutated through caller buffer: %q", chunks[0].Data)
	}
}

// TestRingEvictionDropsOldestWholeChunks proves eviction removes oldest whole
// chunks once the budget would be exceeded, and only then.
func TestRingEvictionDropsOldestWholeChunks(t *testing.T) {
	r := mustRing(t)
	six := bytes.Repeat([]byte{0xAA}, 6<<20)
	for i := 0; i < 2; i++ { // 12 MiB retained: no eviction
		if _, err := r.Append(six); err != nil {
			t.Fatal(err)
		}
	}
	if got := r.OldestRetainedSeq(); got != 1 {
		t.Fatalf("no eviction expected at 12 MiB; oldest = %d", got)
	}
	if _, err := r.Append(six); err != nil { // 18 MiB: evict seq 1 whole
		t.Fatal(err)
	}
	if got := r.OldestRetainedSeq(); got != 2 {
		t.Fatalf("oldest after eviction = %d, want 2", got)
	}
	if got := r.RetainedBytes(); got != 12<<20 {
		t.Fatalf("RetainedBytes = %d, want %d", got, 12<<20)
	}
	if got := r.LatestSeq(); got != 3 {
		t.Fatalf("LatestSeq = %d, want 3", got)
	}
}

// TestRingOversizedAppendRejected proves a single append larger than the
// budget is rejected with the typed error and allocates no sequence number
// (bounded input; commit-then-allocate, ADR-0004).
func TestRingOversizedAppendRejected(t *testing.T) {
	r := mustRing(t)
	if _, err := r.Append(make([]byte, MinRetentionBytes+1)); !errors.Is(err, ErrChunkTooLarge) {
		t.Fatalf("oversized append: got err %v, want ErrChunkTooLarge", err)
	}
	seq, err := r.Append([]byte("ok"))
	if err != nil {
		t.Fatal(err)
	}
	if seq != 1 {
		t.Fatalf("rejected append leaked a sequence number: next seq = %d, want 1", seq)
	}
	// An append of exactly the budget is bounded input and must succeed.
	if _, err := r.Append(make([]byte, MinRetentionBytes)); err != nil {
		t.Fatalf("append of exactly the budget: %v", err)
	}
}

// TestRingReplayEndsExactlyAtLatest proves Replay(fromSeq) returns the
// contiguous retained range [fromSeq, LatestSeq] — never short of N, never
// beyond (ADR-0004: replay ends exactly at N).
func TestRingReplayEndsExactlyAtLatest(t *testing.T) {
	r := mustRing(t)
	for i := 1; i <= 4; i++ {
		if _, err := r.Append([]byte(fmt.Sprintf("chunk-%d", i))); err != nil {
			t.Fatal(err)
		}
	}
	chunks, err := r.Replay(2)
	if err != nil {
		t.Fatal(err)
	}
	if len(chunks) != 3 {
		t.Fatalf("replay(2) returned %d chunks, want 3", len(chunks))
	}
	for i, c := range chunks {
		want := uint64(2 + i)
		if c.Seq != want {
			t.Fatalf("chunk %d seq = %d, want %d (contiguous)", i, c.Seq, want)
		}
		if !bytes.Equal(c.Data, []byte(fmt.Sprintf("chunk-%d", want))) {
			t.Fatalf("chunk %d data = %q", i, c.Data)
		}
	}
	if last := chunks[len(chunks)-1].Seq; last != r.LatestSeq() {
		t.Fatalf("replay ends at %d, want exactly LatestSeq %d", last, r.LatestSeq())
	}
	// A cursor just past the newest returns empty (caller is current) …
	if chunks, err := r.Replay(r.LatestSeq() + 1); err != nil || len(chunks) != 0 {
		t.Fatalf("replay(latest+1) = %d chunks, err %v; want empty, nil", len(chunks), err)
	}
	// … and fromSeq 0 is outside the sequence domain.
	if _, err := r.Replay(0); !errors.Is(err, ErrInvalidFromSeq) {
		t.Fatalf("replay(0): got %v, want ErrInvalidFromSeq", err)
	}
}

// TestRingReplayGapTyped proves an evicted cursor yields the typed
// replay_gap boundary carrying the oldest retained seq (ADR-0004: gaps are
// explicit, never silent).
func TestRingReplayGapTyped(t *testing.T) {
	r := mustRing(t)
	six := bytes.Repeat([]byte{0xBB}, 6<<20)
	for i := 0; i < 3; i++ { // third append evicts seq 1
		if _, err := r.Append(six); err != nil {
			t.Fatal(err)
		}
	}
	_, err := r.Replay(1)
	if !errors.Is(err, ErrReplayGap) {
		t.Fatalf("replay of evicted seq: got %v, want ErrReplayGap", err)
	}
	var gap *ReplayGapError
	if !errors.As(err, &gap) {
		t.Fatalf("gap error is not *ReplayGapError: %T", err)
	}
	if gap.FromSeq != 1 || gap.OldestRetained != 2 || gap.LatestSeq != 3 {
		t.Fatalf("gap = %+v, want FromSeq 1, OldestRetained 2, LatestSeq 3", gap)
	}
	// Replay from the oldest retained seq succeeds.
	chunks, err := r.Replay(gap.OldestRetained)
	if err != nil {
		t.Fatal(err)
	}
	if len(chunks) != 2 || chunks[0].Seq != 2 || chunks[1].Seq != 3 {
		t.Fatalf("replay(oldest) seqs wrong: %d chunks", len(chunks))
	}
}

// TestRingSnapshotRestoreResumesSequencing proves the B8 sidecar hook:
// a snapshot restored into a fresh ring resumes allocation at NextSeq with no
// sequence reuse, and invalid snapshots fail closed.
func TestRingSnapshotRestoreResumesSequencing(t *testing.T) {
	src := mustRing(t)
	for i := 1; i <= 3; i++ {
		if _, err := src.Append([]byte(fmt.Sprintf("c%d", i))); err != nil {
			t.Fatal(err)
		}
	}
	snap := src.Snapshot()
	if snap.NextSeq != 4 || len(snap.Chunks) != 3 {
		t.Fatalf("snapshot = nextSeq %d, %d chunks; want 4, 3", snap.NextSeq, len(snap.Chunks))
	}

	dst := mustRing(t)
	if err := dst.RestoreFromSnapshot(snap.Chunks, snap.NextSeq); err != nil {
		t.Fatal(err)
	}
	seq, err := dst.Append([]byte("after-restore"))
	if err != nil {
		t.Fatal(err)
	}
	if seq != 4 {
		t.Fatalf("post-restore append seq = %d, want 4 (no reuse)", seq)
	}
	chunks, err := dst.Replay(1)
	if err != nil {
		t.Fatal(err)
	}
	if len(chunks) != 4 || chunks[0].Seq != 1 || chunks[3].Seq != 4 {
		t.Fatalf("restored replay range wrong: %d chunks", len(chunks))
	}

	// Fail closed: restore into a used ring.
	if err := dst.RestoreFromSnapshot(snap.Chunks, snap.NextSeq); !errors.Is(err, ErrBadSnapshot) {
		t.Fatalf("restore into used ring: got %v, want ErrBadSnapshot", err)
	}
	// Fail closed: non-contiguous chunks.
	bad := []Chunk{{Seq: 1, Data: []byte("a")}, {Seq: 3, Data: []byte("b")}}
	if err := mustRing(t).RestoreFromSnapshot(bad, 4); !errors.Is(err, ErrBadSnapshot) {
		t.Fatalf("non-contiguous restore: got %v, want ErrBadSnapshot", err)
	}
	// Fail closed: newest chunk not ending at NextSeq-1 (would hide a gap).
	short := []Chunk{{Seq: 1, Data: []byte("a")}}
	if err := mustRing(t).RestoreFromSnapshot(short, 5); !errors.Is(err, ErrBadSnapshot) {
		t.Fatalf("restore with trailing hole: got %v, want ErrBadSnapshot", err)
	}
	// A restore that skips an evicted prefix is legal (oldest retained > 1).
	tail := []Chunk{{Seq: 2, Data: []byte("b")}, {Seq: 3, Data: []byte("c")}}
	trimmed := mustRing(t)
	if err := trimmed.RestoreFromSnapshot(tail, 4); err != nil {
		t.Fatal(err)
	}
	if _, err := trimmed.Replay(1); !errors.Is(err, ErrReplayGap) {
		t.Fatalf("replay before restored range: got %v, want ErrReplayGap", err)
	}
}

// TestRingConcurrentAppendReplay races appenders against replaying readers
// under -race: appends stay monotonic per goroutine, and every observed
// replay is contiguous and ends at or before the then-latest sequence.
func TestRingConcurrentAppendReplay(t *testing.T) {
	r := mustRing(t)
	const (
		appenders = 8
		perWriter = 200
		readers   = 4
	)
	var wg sync.WaitGroup
	for w := 0; w < appenders; w++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			var last uint64
			payload := bytes.Repeat([]byte{byte(id)}, 512)
			for i := 0; i < perWriter; i++ {
				seq, err := r.Append(payload)
				if err != nil {
					t.Errorf("append: %v", err)
					return
				}
				if seq <= last {
					t.Errorf("writer %d: seq %d not after %d", id, seq, last)
					return
				}
				last = seq
			}
		}(w)
	}
	done := make(chan struct{})
	var rg sync.WaitGroup
	for i := 0; i < readers; i++ {
		rg.Add(1)
		go func() {
			defer rg.Done()
			for {
				select {
				case <-done:
					return
				default:
				}
				oldest := r.OldestRetainedSeq()
				if oldest == 0 {
					continue
				}
				chunks, err := r.Replay(oldest)
				if err != nil {
					// The oldest chunk may have been evicted between the
					// two calls; a typed gap is the correct answer then.
					if !errors.Is(err, ErrReplayGap) {
						t.Errorf("replay: %v", err)
						return
					}
					continue
				}
				for j := 1; j < len(chunks); j++ {
					if chunks[j].Seq != chunks[j-1].Seq+1 {
						t.Errorf("replay not contiguous: %d after %d", chunks[j].Seq, chunks[j-1].Seq)
						return
					}
				}
				if len(chunks) > 0 && chunks[len(chunks)-1].Seq > r.LatestSeq() {
					t.Error("replay returned a sequence past LatestSeq")
					return
				}
			}
		}()
	}
	wg.Wait()
	close(done)
	rg.Wait()
	if got := r.LatestSeq(); got != appenders*perWriter {
		t.Fatalf("LatestSeq = %d, want %d (gap-free allocation)", got, appenders*perWriter)
	}
}

func mustRing(t *testing.T) *Ring {
	t.Helper()
	r, err := NewRing(MinRetentionBytes)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

// TestRingReplayLimitPagesThroughRetained proves the bounded fetch keeps
// Replay's cursor semantics: pages are contiguous, in order, and a page never
// exceeds the requested size, while max<=0 stays unbounded.
func TestRingReplayLimitPagesThroughRetained(t *testing.T) {
	r, err := NewRing(MinRetentionBytes)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 10; i++ {
		if _, err := r.Append([]byte{byte(i)}); err != nil {
			t.Fatal(err)
		}
	}
	var got []uint64
	cursor := uint64(1)
	for {
		chunks, err := r.ReplayLimit(cursor, 3)
		if err != nil {
			t.Fatalf("ReplayLimit(%d): %v", cursor, err)
		}
		if len(chunks) == 0 {
			break
		}
		if len(chunks) > 3 {
			t.Fatalf("page of %d chunks exceeds max 3", len(chunks))
		}
		for _, c := range chunks {
			got = append(got, c.Seq)
		}
		cursor = chunks[len(chunks)-1].Seq + 1
	}
	if len(got) != 10 {
		t.Fatalf("paged %d chunks, want 10", len(got))
	}
	for i, s := range got {
		if s != uint64(i+1) {
			t.Fatalf("page order broke at %d: seq %d, want %d", i, s, i+1)
		}
	}
	// Unbounded form matches Replay.
	all, err := r.ReplayLimit(1, 0)
	if err != nil || len(all) != 10 {
		t.Fatalf("ReplayLimit(1, 0) = %d chunks, err %v; want 10, nil", len(all), err)
	}
	// An evicted cursor still reports the typed gap.
	if _, err := r.ReplayLimit(0, 3); !errors.Is(err, ErrInvalidFromSeq) {
		t.Fatalf("cursor 0: got %v, want ErrInvalidFromSeq", err)
	}
}

// TestRingRetainedBytesFrom proves the range accounting used for the attach
// replay-bytes metric.
func TestRingRetainedBytesFrom(t *testing.T) {
	r, err := NewRing(MinRetentionBytes)
	if err != nil {
		t.Fatal(err)
	}
	if got := r.RetainedBytesFrom(1); got != 0 {
		t.Fatalf("empty ring RetainedBytesFrom = %d, want 0", got)
	}
	for i := 0; i < 4; i++ {
		if _, err := r.Append(bytes.Repeat([]byte{'x'}, 5)); err != nil {
			t.Fatal(err)
		}
	}
	if got := r.RetainedBytesFrom(1); got != 20 {
		t.Fatalf("RetainedBytesFrom(1) = %d, want 20", got)
	}
	if got := r.RetainedBytesFrom(3); got != 10 {
		t.Fatalf("RetainedBytesFrom(3) = %d, want 10", got)
	}
	if got := r.RetainedBytesFrom(5); got != 0 {
		t.Fatalf("RetainedBytesFrom(5) = %d, want 0", got)
	}
}

// TestRingReplayLimitBytesBoundsPage proves the byte-budgeted replay form:
// each page is the longest contiguous chunk prefix whose summed payload fits
// the budget, chunks are never split, and paging by NextSeq = last+1 walks the
// retained window contiguously with no duplicates.
func TestRingReplayLimitBytesBoundsPage(t *testing.T) {
	r, err := NewRing(MinRetentionBytes)
	if err != nil {
		t.Fatal(err)
	}
	// 10 chunks of 100 bytes each, distinct content per chunk.
	for i := 0; i < 10; i++ {
		if _, err := r.Append(bytes.Repeat([]byte{byte('a' + i)}, 100)); err != nil {
			t.Fatal(err)
		}
	}

	// A 250-byte budget fits exactly two whole 100-byte chunks — never a split.
	page, err := r.ReplayLimitBytes(1, 0, 250)
	if err != nil {
		t.Fatal(err)
	}
	if len(page) != 2 || page[0].Seq != 1 || page[1].Seq != 2 {
		t.Fatalf("250-byte page = %d chunks (seqs %v), want exactly 2 whole chunks", len(page), seqsOf(page))
	}

	// Paging to exhaustion is contiguous and duplicate-free and returns all bytes.
	var seqs []uint64
	total := 0
	cursor := uint64(1)
	for {
		chunks, err := r.ReplayLimitBytes(cursor, 0, 250)
		if err != nil {
			t.Fatalf("ReplayLimitBytes(%d): %v", cursor, err)
		}
		if len(chunks) == 0 {
			break
		}
		for _, c := range chunks {
			seqs = append(seqs, c.Seq)
			total += len(c.Data)
		}
		cursor = chunks[len(chunks)-1].Seq + 1
	}
	if len(seqs) != 10 || total != 1000 {
		t.Fatalf("paged %d chunks / %d bytes, want 10 / 1000", len(seqs), total)
	}
	for i, s := range seqs {
		if s != uint64(i+1) {
			t.Fatalf("byte paging broke contiguity at %d: seq %d, want %d", i, s, i+1)
		}
	}

	// The chunk-count bound composes with the byte bound (whichever is tighter).
	page, err = r.ReplayLimitBytes(1, 1, 250)
	if err != nil || len(page) != 1 {
		t.Fatalf("maxChunks=1 page = %d chunks, err %v; want 1, nil", len(page), err)
	}

	// maxBytes <= 0 means unbounded bytes (ReplayLimit compatibility).
	all, err := r.ReplayLimitBytes(1, 0, 0)
	if err != nil || len(all) != 10 {
		t.Fatalf("unbounded page = %d chunks, err %v; want 10, nil", len(all), err)
	}
}

// TestRingReplayLimitBytesBoundTooSmall proves a budget smaller than the next
// whole chunk fails typed instead of splitting the chunk (splitting would put
// two wire chunks under one sequence number and break sequence truth).
func TestRingReplayLimitBytesBoundTooSmall(t *testing.T) {
	r, err := NewRing(MinRetentionBytes)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := r.Append(bytes.Repeat([]byte{'x'}, 100)); err != nil {
		t.Fatal(err)
	}
	_, err = r.ReplayLimitBytes(1, 0, 99)
	if !errors.Is(err, ErrBoundTooSmall) {
		t.Fatalf("99-byte budget over a 100-byte chunk: got %v, want ErrBoundTooSmall", err)
	}
	var bound *BoundTooSmallError
	if !errors.As(err, &bound) {
		t.Fatalf("bound error is not typed: %v", err)
	}
	if bound.MaxBytes != 99 || bound.NextChunkBytes != 100 || bound.FromSeq != 1 {
		t.Fatalf("bound error fields = %+v", bound)
	}

	// Gap and cursor semantics are unchanged by the byte bound.
	if _, err := r.ReplayLimitBytes(0, 0, 99); !errors.Is(err, ErrInvalidFromSeq) {
		t.Fatalf("cursor 0: got %v, want ErrInvalidFromSeq", err)
	}
	if got, err := r.ReplayLimitBytes(2, 0, 99); err != nil || len(got) != 0 {
		t.Fatalf("cursor past newest = %v, %v; want empty, nil", got, err)
	}
}

// TestRingReplayLimitBytesGapTyped proves an evicted cursor still reports the
// typed replay gap regardless of the byte budget.
func TestRingReplayLimitBytesGapTyped(t *testing.T) {
	r, err := NewRing(MinRetentionBytes)
	if err != nil {
		t.Fatal(err)
	}
	// Two appends sized so the second evicts the first.
	if _, err := r.Append(bytes.Repeat([]byte{'a'}, MinRetentionBytes-1)); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Append(bytes.Repeat([]byte{'b'}, 2)); err != nil {
		t.Fatal(err)
	}
	_, err = r.ReplayLimitBytes(1, 0, 10)
	var gap *ReplayGapError
	if !errors.As(err, &gap) {
		t.Fatalf("evicted cursor: got %v, want *ReplayGapError", err)
	}
	if gap.OldestRetained != 2 || gap.LatestSeq != 2 || gap.FromSeq != 1 {
		t.Fatalf("gap = %+v", gap)
	}
}

func seqsOf(chunks []Chunk) []uint64 {
	out := make([]uint64, 0, len(chunks))
	for _, c := range chunks {
		out = append(out, c.Seq)
	}
	return out
}

// TestRingReplayPageBytesSnapshotSemantics pins the atomic page contract:
// chunks and LatestSeq come from ONE lock acquisition, and every
// ReplayLimitBytes cursor semantic (current, ahead-of-latest, partial page,
// gap, tiny bound, invalid cursor) is preserved with the snapshot's latest
// attached.
func TestRingReplayPageBytesSnapshotSemantics(t *testing.T) {
	r := mustRing(t)

	// Before any append: cursor 1 is ahead of latest 0 — empty page, latest 0.
	page, err := r.ReplayPageBytes(1, 0, 0)
	if err != nil || len(page.Chunks) != 0 || page.LatestSeq != 0 {
		t.Fatalf("empty ring page = %+v, %v; want no chunks, latest 0", page, err)
	}
	if _, err := r.ReplayPageBytes(0, 0, 0); !errors.Is(err, ErrInvalidFromSeq) {
		t.Fatalf("cursor 0: got %v, want ErrInvalidFromSeq", err)
	}

	// 5 chunks of 100 bytes.
	for i := 0; i < 5; i++ {
		if _, err := r.Append(bytes.Repeat([]byte{byte('a' + i)}, 100)); err != nil {
			t.Fatal(err)
		}
	}

	// Partial page: bounds select whole chunks; LatestSeq is the snapshot's.
	page, err = r.ReplayPageBytes(1, 0, 250)
	if err != nil || len(page.Chunks) != 2 || page.LatestSeq != 5 {
		t.Fatalf("partial page = %+v, %v; want 2 chunks, latest 5", page, err)
	}
	if page.Chunks[0].Seq != 1 || page.Chunks[1].Seq != 2 {
		t.Fatalf("partial page seqs = %v, want [1 2]", seqsOf(page.Chunks))
	}

	// Current cursor (latest+1): empty page carrying the exact latest.
	page, err = r.ReplayPageBytes(6, 0, 250)
	if err != nil || len(page.Chunks) != 0 || page.LatestSeq != 5 {
		t.Fatalf("current-cursor page = %+v, %v; want no chunks, latest 5", page, err)
	}

	// The append-after-empty-snapshot barrier: a chunk appended after that
	// snapshot is returned by the next page taken from LatestSeq+1 — the
	// snapshot's latest never advertises past it.
	if _, err := r.Append([]byte("fresh")); err != nil {
		t.Fatal(err)
	}
	page, err = r.ReplayPageBytes(6, 0, 0)
	if err != nil || len(page.Chunks) != 1 || page.Chunks[0].Seq != 6 || page.LatestSeq != 6 {
		t.Fatalf("post-append page = %+v, %v; the appended seq 6 must surface", page, err)
	}
	if !bytes.Equal(page.Chunks[0].Data, []byte("fresh")) {
		t.Fatalf("post-append payload = %q, want \"fresh\"", page.Chunks[0].Data)
	}

	// Ahead-of-latest cursor: empty page, snapshot latest.
	page, err = r.ReplayPageBytes(100, 0, 0)
	if err != nil || len(page.Chunks) != 0 || page.LatestSeq != 6 {
		t.Fatalf("ahead-of-latest page = %+v, %v; want no chunks, latest 6", page, err)
	}

	// Tiny bound stays typed.
	if _, err := r.ReplayPageBytes(1, 0, 99); !errors.Is(err, ErrBoundTooSmall) {
		t.Fatalf("tiny bound: got %v, want ErrBoundTooSmall", err)
	}

	// Evicted cursor stays a typed gap whose fields match the same snapshot.
	if _, err := r.Append(bytes.Repeat([]byte{'z'}, MinRetentionBytes-1)); err != nil {
		t.Fatal(err)
	}
	_, err = r.ReplayPageBytes(1, 0, 0)
	var gap *ReplayGapError
	if !errors.As(err, &gap) {
		t.Fatalf("evicted cursor: got %v, want *ReplayGapError", err)
	}
	if gap.OldestRetained != 7 || gap.LatestSeq != 7 || gap.FromSeq != 1 {
		t.Fatalf("gap = %+v", gap)
	}
}

// TestRingReplayPageBytesConcurrentProperty hammers one ring with a writer
// whose appends force eviction through the 16 MiB budget while readers take
// pages at pseudo-random cursors and bounds, and asserts the invariants that
// only hold when the page and LatestSeq come from one snapshot:
//
//   - empty page  ⇒ fromSeq > LatestSeq at the snapshot (the atomicity crux)
//   - non-empty   ⇒ starts exactly at fromSeq, contiguous, ends ≤ LatestSeq
//   - replay gap  ⇒ OldestRetained in (fromSeq, LatestSeq]
func TestRingReplayPageBytesConcurrentProperty(t *testing.T) {
	r := mustRing(t)
	const (
		appends    = 640
		chunkBytes = 48 << 10 // 640 × 48 KiB = 30 MiB through the 16 MiB budget
		readers    = 4
	)
	done := make(chan struct{})
	go func() {
		defer close(done)
		payload := bytes.Repeat([]byte{'p'}, chunkBytes)
		for i := 0; i < appends; i++ {
			if _, err := r.Append(payload); err != nil {
				t.Errorf("append: %v", err)
				return
			}
		}
	}()

	var wg sync.WaitGroup
	for w := 0; w < readers; w++ {
		wg.Add(1)
		go func(seed uint64) {
			defer wg.Done()
			rnd := seed
			for {
				select {
				case <-done:
					return
				default:
				}
				// xorshift keeps the reader deterministic per seed and free of
				// shared rand state.
				rnd ^= rnd << 13
				rnd ^= rnd >> 7
				rnd ^= rnd << 17
				fromSeq := 1 + rnd%(appends+2)
				maxChunks := int(rnd % 7)           // 0 = unbounded
				maxBytes := int(rnd%5) * chunkBytes // 0 = unbounded, else whole multiples of one chunk
				page, err := r.ReplayPageBytes(fromSeq, maxChunks, maxBytes)
				if err != nil {
					var gap *ReplayGapError
					if !errors.As(err, &gap) {
						t.Errorf("ReplayPageBytes(%d,%d,%d): %v", fromSeq, maxChunks, maxBytes, err)
						return
					}
					if gap.OldestRetained <= gap.FromSeq || gap.LatestSeq < gap.OldestRetained {
						t.Errorf("gap fields inconsistent: %+v", gap)
						return
					}
					continue
				}
				if len(page.Chunks) == 0 {
					if fromSeq <= page.LatestSeq {
						t.Errorf("empty page at cursor %d with snapshot latest %d: cursor was NOT ahead — atomicity broken",
							fromSeq, page.LatestSeq)
						return
					}
					continue
				}
				if page.Chunks[0].Seq != fromSeq {
					t.Errorf("page starts at %d, want cursor %d", page.Chunks[0].Seq, fromSeq)
					return
				}
				for i := 1; i < len(page.Chunks); i++ {
					if page.Chunks[i].Seq != page.Chunks[i-1].Seq+1 {
						t.Errorf("page not contiguous at %d: %v", i, seqsOf(page.Chunks))
						return
					}
				}
				if last := page.Chunks[len(page.Chunks)-1].Seq; last > page.LatestSeq {
					t.Errorf("page ends at %d past snapshot latest %d", last, page.LatestSeq)
					return
				}
			}
		}(uint64(w) + 0x9E3779B97F4A7C15)
	}
	wg.Wait()
}
