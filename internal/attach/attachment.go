package attach

import "sync"

// Attachment is one client's ordered view of a surface: an atomic snapshot
// (Snapshot) followed by a single contiguous, duplicate-free stream of raw
// frames (Frames) — replay ending exactly at the snapshot's UpToSeq, then live
// output strictly after it (ADR-0004 attach cutover). Delivery is ring-backed:
// the pump follows a cursor through the surface's retention window, so a
// draining client never loses a frame to scheduling. A wedged client — one
// whose delivery buffer stays full with zero consumption, lagging more than a
// full buffer, across a bounded grace confirmation, or whose cursor falls out
// of the ring's retention — is disconnected with Err() == ErrSlowConsumer and a
// LastDelivered receipt; the surface and other attachments are unaffected.
// Detach removes the attachment (and releases the client's input lease)
// WITHOUT stopping the surface/PTY.
type Attachment struct {
	surface  *Surface
	id       uint64
	clientID ClientID

	snap AttachSnapshot

	// out is the public, ordered frame channel; it closes when the pump exits.
	// Its capacity is the attachment's delivery buffer — the client-side bound
	// the slow-consumer policy is measured against.
	out chan Frame
	// done signals the pump to stop (clean detach, slow-consumer disconnect,
	// or surface close). It closes only after err is recorded, so a drained
	// Frames() channel always observes the final Err.
	done chan struct{}

	mu     sync.Mutex
	last   uint64
	err    error
	closed bool
}

// Snapshot returns the atomic snapshot captured at cutover (valid immediately
// after Attach returns, before draining Frames).
func (a *Attachment) Snapshot() AttachSnapshot { return a.snap }

// Frames returns the ordered raw-frame channel. It closes when the attachment
// detaches or is disconnected; check Err after it closes.
func (a *Attachment) Frames() <-chan Frame { return a.out }

// Done closes when the attachment ends (clean detach, slow-consumer
// disconnect, or surface close). Err is final once Done is closed; frames
// already buffered in Frames remain readable afterwards.
func (a *Attachment) Done() <-chan struct{} { return a.done }

// Err reports why the attachment closed: nil while live or after a clean
// Detach, ErrSlowConsumer after a slow-consumer disconnect, ErrSurfaceClosed
// after the surface was torn down.
func (a *Attachment) Err() error {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.err
}

// LastDelivered is the receipt: the highest sequence actually handed to Frames.
// A disconnected consumer resumes from here via bounded replay (Attach with
// FromSeq = LastDelivered+1) or a fresh snapshot (ADR-0004 slow-consumer
// boundary).
func (a *Attachment) LastDelivered() uint64 {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.last
}

// Detach removes the attachment and releases the client's input lease if held,
// leaving the surface and its PTY sink untouched (ADR-0004: detach is not stop).
// Idempotent.
func (a *Attachment) Detach() {
	a.surface.detachObserver(a.id, a.clientID)
	a.close(nil)
}

// Close is an alias for Detach.
func (a *Attachment) Close() { a.Detach() }

// close finalizes the attachment exactly once, recording the receipt error
// before waking the pump (err is written strictly before done closes, so a
// pump exit — and therefore the out-channel closure — never races the Err a
// draining client reads). Safe to call from the surface (slow-consumer /
// close paths) and the client (Detach) concurrently.
func (a *Attachment) close(err error) {
	a.mu.Lock()
	if a.closed {
		a.mu.Unlock()
		return
	}
	a.closed = true
	a.err = err
	close(a.done)
	a.mu.Unlock()
	a.surface.wakePumps()
}

// pump streams ring chunks from the observer's cursor onto out, in order,
// until the attachment ends. Replay (cursor <= UpToSeq) and live (cursor >
// UpToSeq) are one contiguous walk of the ring, so the cutover invariant
// holds by construction — no scheduling can introduce a gap or duplicate.
func (a *Attachment) pump(o *observer) {
	defer close(a.out)
	for {
		chunks, ok := a.surface.awaitOutput(o)
		if !ok {
			return
		}
		for _, c := range chunks {
			if !a.deliver(o, Frame{Seq: c.Seq, Data: c.Data}) {
				return
			}
		}
	}
}

// deliver forwards one frame, recording it as the receipt on success. A full
// client buffer is reported to the surface (noteBlocked) before blocking, so
// the slow-consumer window opens even if no further output ever arrives. It
// aborts (returns false) when the attachment ends while the client's buffer
// is full.
func (a *Attachment) deliver(o *observer, f Frame) bool {
	select {
	case a.out <- f:
	default:
		a.surface.noteBlocked(o)
		select {
		case a.out <- f:
		case <-a.done:
			return false
		}
	}
	a.mu.Lock()
	a.last = f.Seq
	a.mu.Unlock()
	a.surface.markDelivered(o, f.Seq)
	return true
}
