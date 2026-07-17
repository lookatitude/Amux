package terminal

import (
	"errors"
	"fmt"
	"sync"
)

// MinRetentionBytes is the floor for a Ring's retention budget: 16 MiB of raw
// output per surface (spec replay floor). It is deliberately independent of
// the protocol's 8 MiB per-frame body cap (api/v1 MaxBodyBytes): a replay
// spans as many frames as it needs. A budget below this floor is a
// construction error — fail closed, never silently clamped (ADR-0004
// principle: no silent state drift).
const MinRetentionBytes = 16 << 20

var (
	// ErrBudgetTooSmall rejects a Ring constructed below MinRetentionBytes.
	ErrBudgetTooSmall = errors.New("terminal: retention budget below the 16 MiB replay floor")
	// ErrChunkTooLarge rejects a single Append larger than the whole budget
	// (bounded input); the rejected append allocates no sequence number.
	ErrChunkTooLarge = errors.New("terminal: chunk exceeds retention budget")
	// ErrReplayGap is the typed replay_gap boundary (ADR-0004): the requested
	// cursor was evicted, so the caller must snapshot instead of replaying.
	// Returned wrapped in a *ReplayGapError carrying the retained range.
	ErrReplayGap = errors.New("terminal: replay gap")
	// ErrInvalidFromSeq rejects Replay cursors outside the sequence domain
	// (sequences start at 1).
	ErrInvalidFromSeq = errors.New("terminal: replay fromSeq must be >= 1")
	// ErrBoundTooSmall rejects a ReplayLimitBytes budget smaller than the next
	// whole retained chunk. Chunks are never split: a split would present two
	// wire chunks under one sequence number and break sequence truth, so a
	// too-small budget fails typed instead. Returned wrapped in a
	// *BoundTooSmallError carrying the sizes.
	ErrBoundTooSmall = errors.New("terminal: replay byte bound below the next whole chunk")
	// ErrBadSnapshot rejects a RestoreFromSnapshot whose chunks are not
	// contiguous, exceed the budget, or conflict with the ring's state.
	ErrBadSnapshot = errors.New("terminal: invalid ring snapshot")
)

// ReplayGapError reports that chunks in [FromSeq, OldestRetained) existed but
// were evicted. It wraps ErrReplayGap so callers match with errors.Is and read
// the boundary data with errors.As (ADR-0004: gaps are explicit and typed).
type ReplayGapError struct {
	// FromSeq is the cursor the caller asked to replay from.
	FromSeq uint64
	// OldestRetained is the oldest sequence still replayable; 0 when nothing
	// is retained at all.
	OldestRetained uint64
	// LatestSeq is the newest sequence ever appended.
	LatestSeq uint64
}

func (e *ReplayGapError) Error() string {
	return fmt.Sprintf("terminal: replay gap: fromSeq %d evicted (oldest retained %d, latest %d)",
		e.FromSeq, e.OldestRetained, e.LatestSeq)
}

// Unwrap makes errors.Is(err, ErrReplayGap) hold.
func (e *ReplayGapError) Unwrap() error { return ErrReplayGap }

// BoundTooSmallError reports that the next whole chunk at FromSeq does not fit
// the caller's byte budget. It wraps ErrBoundTooSmall so callers match with
// errors.Is and read the sizes with errors.As.
type BoundTooSmallError struct {
	// FromSeq is the cursor the caller asked to replay from.
	FromSeq uint64
	// MaxBytes is the byte budget that was too small.
	MaxBytes int
	// NextChunkBytes is the size of the whole chunk at FromSeq — the minimum
	// budget that makes progress from this cursor.
	NextChunkBytes int
}

func (e *BoundTooSmallError) Error() string {
	return fmt.Sprintf("terminal: replay byte bound %d below the next whole chunk (%d bytes at seq %d)",
		e.MaxBytes, e.NextChunkBytes, e.FromSeq)
}

// Unwrap makes errors.Is(err, ErrBoundTooSmall) hold.
func (e *BoundTooSmallError) Unwrap() error { return ErrBoundTooSmall }

// Chunk is one immutable appended output chunk. Seq is the monotonic output
// sequence number (first chunk of a surface = 1, gap-free thereafter). Data is
// an isolated copy owned by the receiver.
type Chunk struct {
	Seq  uint64
	Data []byte
}

// RingSnapshot is the raw-authority handoff for the B8 sidecar writer
// (ADR-0005: checksummed replay sidecars). Chunks are isolated copies covering
// the full retained, contiguous range; NextSeq is the next sequence the ring
// would allocate, so a restore resumes allocation without reuse.
type RingSnapshot struct {
	Chunks  []Chunk
	NextSeq uint64
}

// Ring is the bounded raw replay ring for one surface. It is the durable-raw
// authority feed (ADR-0005): a PTY reader goroutine appends while attach
// goroutines replay concurrently, so every method is safe for concurrent use.
// Eviction drops oldest whole chunks once retained bytes would exceed the
// budget; chunks are never split.
type Ring struct {
	mu       sync.Mutex
	budget   int
	chunks   []Chunk // oldest first; contiguous seqs
	retained int
	nextSeq  uint64 // next sequence to allocate; starts at 1
}

// NewRing constructs a ring with the given retention budget in bytes. Budgets
// below MinRetentionBytes are rejected with ErrBudgetTooSmall — fail closed,
// never silently clamped.
func NewRing(budgetBytes int) (*Ring, error) {
	if budgetBytes < MinRetentionBytes {
		return nil, fmt.Errorf("%w: %d < %d", ErrBudgetTooSmall, budgetBytes, MinRetentionBytes)
	}
	return &Ring{budget: budgetBytes, nextSeq: 1}, nil
}

// Append stores an immutable copy of data as the next chunk and returns its
// sequence number. A chunk larger than the whole budget is rejected with
// ErrChunkTooLarge and allocates no sequence number (mirroring ADR-0004's
// allocate-only-after-commit rule). Appending may evict oldest chunks to stay
// within budget; the newest chunk always fits by construction.
func (r *Ring) Append(data []byte) (uint64, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(data) > r.budget {
		return 0, fmt.Errorf("%w: %d bytes > budget %d", ErrChunkTooLarge, len(data), r.budget)
	}
	seq := r.nextSeq
	r.nextSeq++
	r.chunks = append(r.chunks, Chunk{Seq: seq, Data: append([]byte(nil), data...)})
	r.retained += len(data)
	r.evictLocked()
	return seq, nil
}

// evictLocked drops oldest whole chunks until retained bytes fit the budget.
func (r *Ring) evictLocked() {
	dropped := 0
	for r.retained > r.budget {
		r.retained -= len(r.chunks[dropped].Data)
		r.chunks[dropped] = Chunk{} // release the backing array
		dropped++
	}
	if dropped > 0 {
		r.chunks = r.chunks[dropped:]
		// Compact once the dead head dominates the backing array.
		if len(r.chunks)*2 < cap(r.chunks) {
			r.chunks = append([]Chunk(nil), r.chunks...)
		}
	}
}

// Replay returns copies of the retained chunks with seq >= fromSeq, in order,
// ending exactly at the newest retained sequence (ADR-0004: replay ends
// exactly at N). A cursor past the newest sequence returns an empty result and
// no error (the caller is already current). A cursor that was evicted returns
// a *ReplayGapError — the explicit replay_gap boundary telling the caller to
// snapshot instead.
func (r *Ring) Replay(fromSeq uint64) ([]Chunk, error) {
	return r.ReplayLimit(fromSeq, 0)
}

// ReplayLimit is Replay bounded to at most max chunks (max <= 0 means
// unbounded). It keeps Replay's cursor and replay-gap semantics; the bounded
// form lets a streaming consumer (attach fan-out) page through retained
// output without copying the whole window per fetch.
func (r *Ring) ReplayLimit(fromSeq uint64, max int) ([]Chunk, error) {
	return r.ReplayLimitBytes(fromSeq, max, 0)
}

// ReplayLimitBytes is ReplayLimit additionally bounded to a cumulative payload
// budget in bytes (maxBytes <= 0 means unbounded bytes). The page is the
// longest contiguous prefix of retained chunks from fromSeq whose summed data
// fits both bounds; chunks are never split (a split would break sequence
// truth), so a budget below the next whole chunk fails typed with a
// *BoundTooSmallError instead of returning a torn chunk. Only the selected
// chunks are copied, so a bounded page over a large retained window allocates
// proportionally to the page, not the window.
func (r *Ring) ReplayLimitBytes(fromSeq uint64, maxChunks, maxBytes int) ([]Chunk, error) {
	page, err := r.ReplayPageBytes(fromSeq, maxChunks, maxBytes)
	return page.Chunks, err
}

// ReplayPage is one atomically selected replay page: the chunks plus the exact
// latest sequence observed under the same lock acquisition that selected them.
// Deriving both from one snapshot is what lets a caller advertise an
// empty-page continuation cursor (LatestSeq+1) without racing a concurrent
// append — with separate calls, a chunk appended between them would sit inside
// the advertised cursor and be silently skipped (flow-14 no-silent-skip).
type ReplayPage struct {
	// Chunks is the selected whole-chunk page (isolated copies), oldest first;
	// empty when fromSeq was ahead of LatestSeq at the snapshot (the caller is
	// current).
	Chunks []Chunk
	// LatestSeq is the newest sequence ever appended, observed under the same
	// lock that selected Chunks; 0 before any append.
	LatestSeq uint64
}

// ReplayPageBytes is ReplayLimitBytes returning the page together with the
// latest sequence from the SAME ring snapshot. It keeps every ReplayLimitBytes
// semantic: cursor 0 fails typed, a cursor ahead of the latest sequence
// returns an empty page (the caller is current as of LatestSeq), an evicted
// cursor returns a *ReplayGapError, and a positive byte budget below the next
// whole chunk returns a *BoundTooSmallError.
func (r *Ring) ReplayPageBytes(fromSeq uint64, maxChunks, maxBytes int) (ReplayPage, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if fromSeq == 0 {
		return ReplayPage{}, ErrInvalidFromSeq
	}
	latest := r.nextSeq - 1
	page := ReplayPage{LatestSeq: latest}
	if fromSeq > latest {
		return page, nil
	}
	if len(r.chunks) == 0 || fromSeq < r.chunks[0].Seq {
		var oldest uint64
		if len(r.chunks) > 0 {
			oldest = r.chunks[0].Seq
		}
		return ReplayPage{}, &ReplayGapError{FromSeq: fromSeq, OldestRetained: oldest, LatestSeq: latest}
	}
	n := int(latest - fromSeq + 1)
	if maxChunks > 0 && n > maxChunks {
		n = maxChunks
	}
	avail := r.chunks[fromSeq-r.chunks[0].Seq:]
	if maxBytes > 0 && len(avail[0].Data) > maxBytes {
		return ReplayPage{}, &BoundTooSmallError{FromSeq: fromSeq, MaxBytes: maxBytes, NextChunkBytes: len(avail[0].Data)}
	}
	out := make([]Chunk, 0, n)
	budget := maxBytes
	for _, c := range avail {
		if len(out) == n {
			break
		}
		if maxBytes > 0 {
			if len(c.Data) > budget {
				break
			}
			budget -= len(c.Data)
		}
		out = append(out, Chunk{Seq: c.Seq, Data: append([]byte(nil), c.Data...)})
	}
	page.Chunks = out
	return page, nil
}

// RetainedBytesFrom returns the total bytes of retained chunks with
// seq >= fromSeq. It is the attach-time accounting for how many replay bytes
// a new attachment committed to serve.
func (r *Ring) RetainedBytesFrom(fromSeq uint64) int {
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.chunks) == 0 || fromSeq > r.nextSeq-1 {
		return 0
	}
	total := 0
	for _, c := range r.chunks {
		if c.Seq >= fromSeq {
			total += len(c.Data)
		}
	}
	return total
}

// LatestSeq returns the newest sequence ever appended; 0 before any append.
func (r *Ring) LatestSeq() uint64 {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.nextSeq - 1
}

// OldestRetainedSeq returns the oldest replayable sequence; 0 when nothing is
// retained.
func (r *Ring) OldestRetainedSeq() uint64 {
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.chunks) == 0 {
		return 0
	}
	return r.chunks[0].Seq
}

// RetainedBytes returns the total bytes currently retained.
func (r *Ring) RetainedBytes() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.retained
}

// Snapshot returns an isolated copy of the retained chunks plus the next
// sequence to allocate, for the B8 sidecar writer (ADR-0005).
func (r *Ring) Snapshot() RingSnapshot {
	r.mu.Lock()
	defer r.mu.Unlock()
	s := RingSnapshot{NextSeq: r.nextSeq}
	if len(r.chunks) > 0 {
		s.Chunks = make([]Chunk, 0, len(r.chunks))
		for _, c := range r.chunks {
			s.Chunks = append(s.Chunks, Chunk{Seq: c.Seq, Data: append([]byte(nil), c.Data...)})
		}
	}
	return s
}

// RestoreFromSnapshot loads a previously snapshotted chunk range into a fresh
// ring and resumes sequence allocation at nextSeq, guaranteeing no sequence
// reuse. It fails closed with ErrBadSnapshot when the ring has already been
// used, the chunks are not contiguous, a chunk seq is outside [1, nextSeq),
// the newest chunk does not end exactly at nextSeq-1, or the total exceeds the
// budget (a restore never silently drops raw bytes — size the budget instead).
func (r *Ring) RestoreFromSnapshot(chunks []Chunk, nextSeq uint64) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.nextSeq != 1 || len(r.chunks) != 0 {
		return fmt.Errorf("%w: ring already used", ErrBadSnapshot)
	}
	if nextSeq < 1 {
		return fmt.Errorf("%w: nextSeq must be >= 1", ErrBadSnapshot)
	}
	total := 0
	for i, c := range chunks {
		if c.Seq < 1 || c.Seq >= nextSeq {
			return fmt.Errorf("%w: chunk seq %d outside [1, %d)", ErrBadSnapshot, c.Seq, nextSeq)
		}
		if i > 0 && c.Seq != chunks[i-1].Seq+1 {
			return fmt.Errorf("%w: chunks not contiguous at seq %d", ErrBadSnapshot, c.Seq)
		}
		total += len(c.Data)
	}
	if len(chunks) > 0 && chunks[len(chunks)-1].Seq != nextSeq-1 {
		return fmt.Errorf("%w: newest chunk %d does not end at nextSeq-1 (%d)",
			ErrBadSnapshot, chunks[len(chunks)-1].Seq, nextSeq-1)
	}
	if total > r.budget {
		return fmt.Errorf("%w: %d bytes exceed budget %d", ErrBadSnapshot, total, r.budget)
	}
	r.chunks = make([]Chunk, 0, len(chunks))
	for _, c := range chunks {
		r.chunks = append(r.chunks, Chunk{Seq: c.Seq, Data: append([]byte(nil), c.Data...)})
	}
	r.retained = total
	r.nextSeq = nextSeq
	return nil
}
