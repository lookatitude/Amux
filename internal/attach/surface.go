package attach

import (
	"sync"
	"time"

	"github.com/amux-run/amux/internal/observability"
	"github.com/amux-run/amux/internal/ordering"
	"github.com/amux-run/amux/internal/terminal"
)

// pumpBatch bounds how many retained chunks one pump fetch copies out of the
// ring, so a catching-up observer pages through history instead of
// materializing the whole retained window at once.
const pumpBatch = 64

// stallGrace is the bounded confirmation interval between observing a stall
// window and executing the slow-consumer disconnect. It exists to separate "the
// client is wedged" from "the producer momentarily outran the scheduler": a
// runnable consumer goroutine is guaranteed CPU well inside this interval
// (Go's preemption quantum is ~10ms), so only a client that consumed nothing
// across BOTH a full extra buffer of production AND this interval is
// disconnected. It is a decision-confirmation bound, not a delivery timeout —
// no frame delivery ever waits on it.
const stallGrace = 100 * time.Millisecond

// Surface is the attach unit for one terminal surface: it owns the raw-output
// ring, fans live output out to attached observers, and arbitrates the single
// input lease. Output is shared across all attachments; input requires the
// lease (ADR-0004). One mutex serializes ring appends, attach cutover, and
// lease transitions — that single lock is the linearization point the cutover
// invariant depends on. Delivery is ring-backed: each observer's pump follows
// its own cursor through the ring, so a draining client can never lose a frame
// to scheduling; the ring's retention budget is the only buffer that matters.
type Surface struct {
	id         string
	ring       *terminal.Ring
	snapshotFn func() terminal.CellSnapshot
	inputSink  func([]byte) error
	onLease    func(LeaseNotice)
	buffer     int
	metrics    *metrics

	mu        sync.Mutex
	cond      *sync.Cond // broadcast on append, close, and observer removal
	lease     ordering.LeaseState
	observers map[uint64]*observer
	nextObs   uint64
	closed    bool
}

// observer is one attachment's delivery cursor inside the surface. All fields
// below att are guarded by the surface mutex.
type observer struct {
	id  uint64
	att *Attachment

	// buffer == cap(att.out): the client's delivery depth in frames.
	buffer uint64
	// next is the next ring sequence the pump will fetch.
	next uint64
	// delivered is the highest sequence placed into att.out.
	delivered uint64
	// stallBase/stallDelivered track the slow-consumer window: stallBase is
	// the ring sequence at which the window opened (0 = closed), and
	// stallDelivered is the delivery watermark frozen at that point.
	stallBase      uint64
	stallDelivered uint64
	// graceTimer confirms an open stall window after stallGrace; it is armed
	// at most once per open window and re-armed by later appends if the
	// confirmation declined to disconnect.
	graceTimer *time.Timer
	graceArmed bool
}

// evalStall updates this observer's slow-consumer window for the just-appended
// sequence `latest` and arms the grace confirmation when the window is open.
// Called under s.mu. It never disconnects directly — see confirmStall.
//
// Policy (ADR-0004 slow-consumer boundary, mechanism owned by T4): lag alone
// never disconnects — the ring lets a draining client catch up losslessly.
// Disconnect requires ALL of: (a) the client lags by more than its delivery
// buffer, (b) the delivery buffer is completely full, and (c) the client
// consumed nothing across the stallGrace confirmation interval. (a)+(b) are
// the data-volume trigger; (c) grants the consumer goroutine a bounded
// scheduling opportunity before the verdict, so a healthy drained observer is
// never a timing casualty of a producer that momentarily outran the
// scheduler, while a wedged client is disconnected within one grace interval
// of wedging — whether or not any further output arrives (the pump arms the
// window itself via noteBlocked when it cannot hand a frame over).
func (s *Surface) evalStall(o *observer, latest uint64) {
	out := o.att.out
	if latest-o.delivered <= o.buffer || len(out) < cap(out) {
		o.stallBase = 0
		return
	}
	if o.stallBase == 0 || o.delivered != o.stallDelivered {
		o.stallBase, o.stallDelivered = latest, o.delivered
	}
	if !o.graceArmed {
		o.graceArmed = true
		id := o.id
		o.graceTimer = time.AfterFunc(stallGrace, func() { s.confirmStall(id) })
	}
}

// confirmStall re-checks a stall window one grace interval after it was armed
// and executes the slow-consumer disconnect only if the client consumed
// nothing the whole time and still lags by more than a full delivery buffer.
// Any consumption (delivery watermark moved, or space opened in the client's
// buffer) cancels the disconnect and closes the window; a zero-consumption
// window whose lag shrank below the trigger stays open and is re-armed by the
// next append or pump block.
func (s *Surface) confirmStall(id uint64) {
	s.mu.Lock()
	o, live := s.observers[id]
	if !live {
		s.mu.Unlock()
		return
	}
	o.graceArmed = false
	latest := s.ring.LatestSeq()
	out := o.att.out
	wedged := o.stallBase != 0 &&
		o.delivered == o.stallDelivered &&
		len(out) == cap(out) &&
		latest-o.delivered > o.buffer
	if !wedged {
		if o.delivered != o.stallDelivered || len(out) < cap(out) {
			o.stallBase = 0
		}
		s.mu.Unlock()
		return
	}
	delete(s.observers, id)
	note, released := s.releaseLeaseLocked(o.att.clientID)
	s.cond.Broadcast()
	s.mu.Unlock()

	o.att.close(ErrSlowConsumer)
	s.metrics.addObservers(-1)
	s.metrics.incSlow()
	if released {
		s.emitLease(note)
	}
}

// stopGrace disarms a pending grace confirmation. Called under s.mu when the
// observer is removed by another path; a concurrently-firing timer is harmless
// because confirmStall re-checks liveness under the lock.
func (o *observer) stopGrace() {
	if o.graceTimer != nil {
		o.graceTimer.Stop()
	}
	o.graceArmed = false
}

// NewSurface builds a Surface. Ring and ID are required. Several surfaces
// sharing one Registry share the attach gauges/counters (resolved by name).
func NewSurface(cfg SurfaceConfig, reg *observability.Registry) (*Surface, error) {
	if cfg.ID == "" {
		return nil, ErrNoID
	}
	if cfg.Ring == nil {
		return nil, ErrNoRing
	}
	snap := cfg.Snapshot
	if snap == nil {
		snap = func() terminal.CellSnapshot { return terminal.CellSnapshot{} }
	}
	buf := cfg.Buffer
	if buf <= 0 {
		buf = DefaultBuffer
	}
	s := &Surface{
		id:         cfg.ID,
		ring:       cfg.Ring,
		snapshotFn: snap,
		inputSink:  cfg.InputSink,
		onLease:    cfg.OnLease,
		buffer:     buf,
		metrics:    newMetrics(reg),
		observers:  make(map[uint64]*observer),
	}
	s.cond = sync.NewCond(&s.mu)
	return s, nil
}

// ID returns the surface identity.
func (s *Surface) ID() string { return s.id }

// OnOutput ingests one raw output chunk: it appends p to the ring (allocating
// the next output sequence) under the surface lock and wakes every observer
// pump. Appending under the same lock Attach uses to read N is what
// linearizes attach cutover against live output (ADR-0004): an Attach that
// reads N sees the chunk either in its replay (seq <= N) or in its live feed
// (seq > N), never both and never neither. Routing every append through
// OnOutput (rather than appending to the ring directly) is what keeps that
// guarantee. It also evaluates the slow-consumer windows (evalStall); a
// wedged attachment is disconnected with a receipt by the grace confirmation
// (confirmStall), leaving the surface and every other attachment untouched.
func (s *Surface) OnOutput(p []byte) (uint64, error) {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return 0, ErrSurfaceClosed
	}
	seq, err := s.ring.Append(p)
	if err != nil {
		s.mu.Unlock()
		return 0, err
	}
	for _, o := range s.observers {
		s.evalStall(o, seq)
	}
	s.cond.Broadcast()
	s.mu.Unlock()
	return seq, nil
}

// noteBlocked is the pump-side stall trigger: the pump calls it when a frame
// cannot be handed over because the client's buffer is full, so a wedge is
// detected (and the grace confirmation armed) even when no further output
// ever arrives after the client stopped consuming.
func (s *Surface) noteBlocked(o *observer) {
	s.mu.Lock()
	if _, live := s.observers[o.id]; live {
		s.evalStall(o, s.ring.LatestSeq())
	}
	s.mu.Unlock()
}

// Attach linearizes the cutover under the surface lock: it captures the cell
// snapshot and N (== ring.LatestSeq) and registers the observer's cursor —
// atomically, so no concurrent OnOutput can interleave (ADR-0004; the
// invariant ordering.AttachCutover proves). The default cursor is N+1:
// snapshot + live only, with raw replay opt-in via FromSeq (the resume path
// from a LastDelivered receipt). If the ring already evicted the requested
// floor, replay starts at OldestRetainedSeq and the snapshot carries a
// ReplayGap boundary rather than silently starting mid-history.
func (s *Surface) Attach(clientID ClientID, opts AttachOptions) (*Attachment, error) {
	buf := opts.Buffer
	if buf <= 0 {
		buf = s.buffer
	}
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return nil, ErrSurfaceClosed
	}
	n := s.ring.LatestSeq()
	cell := s.snapshotFn()
	oldest := s.ring.OldestRetainedSeq()

	// Cursor selection: FromSeq 0 (default) and any floor past N mean
	// live-only from the cutover point; an explicit retained floor replays
	// from there; an evicted floor surfaces a typed ReplayGap and replays
	// from the oldest retained sequence (never a silent bridge).
	next := n + 1
	var gap *ReplayGap
	if from := opts.FromSeq; from != 0 && from <= n {
		next = from
		if from < oldest {
			gap = &ReplayGap{RequestedFrom: from, OldestRetained: oldest, LatestSeq: n}
			next = oldest
		}
	}
	var replayBytes int64
	if next <= n {
		replayBytes = int64(s.ring.RetainedBytesFrom(next))
	}

	id := s.nextObs
	s.nextObs++
	att := &Attachment{
		surface:  s,
		id:       id,
		clientID: clientID,
		snap: AttachSnapshot{
			Cell:      cell,
			Pane:      PaneMeta{SurfaceID: s.id, Rows: cell.Rows, Cols: cell.Cols, Title: cell.Title},
			UpToSeq:   n,
			ReplayGap: gap,
		},
		out:  make(chan Frame, buf),
		done: make(chan struct{}),
	}
	o := &observer{id: id, att: att, buffer: uint64(buf), next: next, delivered: next - 1}
	s.observers[id] = o
	s.mu.Unlock()

	s.metrics.addObservers(1)
	s.metrics.addReplayBytes(replayBytes)
	go att.pump(o)
	return att, nil
}

// awaitOutput blocks until the ring holds sequences >= o.next and returns the
// next bounded batch, advancing the cursor. ok=false ends the pump: the
// attachment detached, was disconnected, or the surface closed — by then the
// receipt error is final (the pump waits for att.done so close(out) never
// races the Err write). A cursor evicted mid-stream is true data loss: the
// observer is disconnected as a slow consumer with its receipt (ADR-0004:
// gaps are explicit, never silently bridged).
func (s *Surface) awaitOutput(o *observer) ([]terminal.Chunk, bool) {
	s.mu.Lock()
	for {
		if _, live := s.observers[o.id]; !live {
			s.mu.Unlock()
			<-o.att.done
			return nil, false
		}
		chunks, err := s.ring.ReplayLimit(o.next, pumpBatch)
		if err != nil {
			// Evicted cursor: the client fell behind the ring's retention.
			o.stopGrace()
			delete(s.observers, o.id)
			note, released := s.releaseLeaseLocked(o.att.clientID)
			s.mu.Unlock()
			o.att.close(ErrSlowConsumer)
			s.metrics.addObservers(-1)
			s.metrics.incSlow()
			if released {
				s.emitLease(note)
			}
			return nil, false
		}
		if len(chunks) > 0 {
			o.next = chunks[len(chunks)-1].Seq + 1
			s.mu.Unlock()
			return chunks, true
		}
		s.cond.Wait()
	}
}

// markDelivered records the pump's delivery watermark for the slow-consumer
// policy. A watermark for an already-removed observer is harmless.
func (s *Surface) markDelivered(o *observer, seq uint64) {
	s.mu.Lock()
	o.delivered = seq
	s.mu.Unlock()
}

// wakePumps unblocks every waiting pump so it can observe removal/closure.
// sync.Cond permits Broadcast without holding the lock.
func (s *Surface) wakePumps() { s.cond.Broadcast() }

// detachObserver removes one attachment and releases the client's lease if it
// held it, without touching the sink/PTY (ADR-0004: detach is not stop).
// Idempotent: a second call (or one racing a slow-consumer disconnect) finds no
// observer and no held lease and is a no-op for the gauges.
func (s *Surface) detachObserver(id uint64, c ClientID) {
	s.mu.Lock()
	o, existed := s.observers[id]
	if existed {
		o.stopGrace()
		delete(s.observers, id)
	}
	note, released := s.releaseLeaseLocked(c)
	s.cond.Broadcast()
	s.mu.Unlock()
	if existed {
		s.metrics.addObservers(-1)
	}
	if released {
		s.emitLease(note)
	}
}

// --- input lease (ADR-0004 §input-lease state machine) --------------------

// AcquireInput grants the input lease if free (or re-affirms it for the current
// holder). It NEVER implicitly takes over another client's lease — that returns
// ErrInputLeaseHeld; the caller must call TakeoverInput deliberately.
func (s *Surface) AcquireInput(c ClientID) error {
	s.mu.Lock()
	prev, wasHeld := s.lease.Holder()
	ns, ev, ok := s.lease.Acquire(c)
	if !ok {
		s.mu.Unlock()
		return ErrInputLeaseHeld
	}
	s.lease = ns
	if !wasHeld {
		s.metrics.addLeases(1)
	}
	note := LeaseNotice{Surface: s.id, Kind: ev, Holder: c, Prev: prev}
	s.mu.Unlock()
	s.emitLease(note)
	return nil
}

// TakeoverInput deliberately transfers the lease to c and emits a takeover
// event; the previous holder (Prev) is expected to observe it and stop writing.
func (s *Surface) TakeoverInput(c ClientID) error {
	s.mu.Lock()
	prev, wasHeld := s.lease.Holder()
	ns, ev := s.lease.Takeover(c)
	s.lease = ns
	if !wasHeld {
		s.metrics.addLeases(1) // free -> held; a held->held takeover is net zero
	}
	note := LeaseNotice{Surface: s.id, Kind: ev, Holder: c, Prev: prev}
	s.mu.Unlock()
	s.emitLease(note)
	return nil
}

// ReleaseInput frees the lease if c holds it (a non-holder release is a no-op).
func (s *Surface) ReleaseInput(c ClientID) error {
	s.mu.Lock()
	note, released := s.releaseLeaseLocked(c)
	s.mu.Unlock()
	if released {
		s.emitLease(note)
	}
	return nil
}

// Write forwards p to the input sink ONLY for the lease holder. A non-holder is
// rejected with ErrNotLeaseHolder before any byte reaches the sink (ADR-0004).
// The sink is invoked outside the surface lock so a blocking PTY write never
// stalls output fan-out.
func (s *Surface) Write(c ClientID, p []byte) error {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return ErrSurfaceClosed
	}
	allowed, _ := s.lease.WriteAllowed(c)
	sink := s.inputSink
	s.mu.Unlock()
	if !allowed {
		return ErrNotLeaseHolder
	}
	if sink == nil {
		return nil
	}
	return sink(p)
}

// InputHolder returns the current lease holder and whether the lease is held.
func (s *Surface) InputHolder() (ClientID, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.lease.Holder()
}

// releaseLeaseLocked frees c's lease under the surface lock, decrementing the
// gauge, and returns the notice to emit after unlocking. Callers hold s.mu.
func (s *Surface) releaseLeaseLocked(c ClientID) (LeaseNotice, bool) {
	ns, ev := s.lease.Release(c)
	if ev == ordering.LeaseReleased {
		s.lease = ns
		s.metrics.addLeases(-1)
		return LeaseNotice{Surface: s.id, Kind: ev, Holder: "", Prev: c}, true
	}
	return LeaseNotice{}, false
}

func (s *Surface) emitLease(n LeaseNotice) {
	if s.onLease != nil && n.Kind != ordering.LeaseNone {
		s.onLease(n)
	}
}

// Close tears the surface down: it disconnects every attachment with
// ErrSurfaceClosed and frees the lease. It is the manager-level teardown, NOT a
// client detach — a normal client Detach never closes the surface (ADR-0004).
func (s *Surface) Close() {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return
	}
	s.closed = true
	obs := s.observers
	for _, o := range obs {
		o.stopGrace()
	}
	s.observers = make(map[uint64]*observer)
	_, wasHeld := s.lease.Holder()
	s.lease = ordering.LeaseState{}
	s.cond.Broadcast()
	s.mu.Unlock()

	for range obs {
		s.metrics.addObservers(-1)
	}
	if wasHeld {
		s.metrics.addLeases(-1)
	}
	for _, o := range obs {
		o.att.close(ErrSurfaceClosed)
	}
}
