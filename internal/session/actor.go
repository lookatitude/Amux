// Package session implements the per-session graph actor (ADR-0001): one
// goroutine owns one session's domain.State, its revision counter, and its
// event-sequence counter. All graph mutation flows through Submit; peripheral
// producers (PTY readers, clients, persistence) never touch the state
// directly.
//
// Event ordering (ADR-0004): a sequence number is allocated ONLY after a
// command commits — a rejected command allocates nothing, so the committed
// log is strictly monotonic and gap-free. Committed events land in a bounded
// in-memory ring; replay past the ring's tail is a typed ErrEventGap boundary
// (the subscriber takes a fresh snapshot and a new cursor, never a silent
// bridge). Subscribers get bounded buffers; a slow consumer is disconnected
// with a receipt carrying its last delivered sequence.
//
// The actor goroutine never performs blocking I/O: no PTY, SQLite, git, or
// hook work runs here (ADR-0001 §Cross-actor ordering).
package session

import (
	"context"
	"errors"
	"fmt"

	"github.com/amux-run/amux/internal/domain"
	"github.com/amux-run/amux/internal/platform"
)

// DefaultMailbox bounds the actor's message queue (backpressure, not
// unbounded buffering).
const DefaultMailbox = 256

// DefaultRingEvents bounds the committed-event replay ring.
const DefaultRingEvents = 4096

// DefaultSubscriberBuffer bounds one subscriber's delivery queue.
const DefaultSubscriberBuffer = 256

// ErrStopped is returned for calls made after Stop.
var ErrStopped = errors.New("session: actor stopped")

// ErrSlowConsumer closes a subscription whose bounded buffer overflowed. The
// receipt (Subscription.Err + LastDelivered) tells the client where to resume
// via bounded replay or snapshot-on-gap (ADR-0004 slow-consumer boundary).
var ErrSlowConsumer = errors.New("session: slow consumer disconnected")

// EventGapError is the typed event_gap boundary: the requested cursor
// precedes the ring's oldest retained sequence.
type EventGapError struct {
	Requested      uint64
	OldestRetained uint64
	Latest         uint64
}

func (e *EventGapError) Error() string {
	return fmt.Sprintf("event_gap: requested seq %d, retained window is [%d..%d]; take a fresh snapshot and a new cursor",
		e.Requested, e.OldestRetained, e.Latest)
}

// Event is one committed, sequence-numbered session event. Seq/TimeMS wrap
// the domain payload after commit (the domain itself stays clock- and
// sequence-free, ADR-0002).
type Event struct {
	Session   domain.SessionID
	Seq       uint64
	TimeMS    int64
	Kind      string
	Workspace domain.WorkspaceID
	Rev       uint64
	Payload   domain.Event
}

// Result is a committed transition: the new immutable state, its session
// revision, and the committed events with their allocated sequences.
type Result struct {
	State  *domain.State
	Rev    uint64
	Events []Event
}

// Config wires an actor.
type Config struct {
	ID    domain.SessionID
	IDs   domain.IDSource
	Clock platform.Clock
	// RingEvents bounds the replay ring (default DefaultRingEvents).
	RingEvents int
}

// Actor is one session's event-loop goroutine.
type Actor struct {
	id      domain.SessionID
	mailbox chan func()
	stop    chan struct{}
	done    chan struct{}

	// Owned by run():
	state   *domain.State
	ids     domain.IDSource
	clock   platform.Clock
	seq     uint64
	ring    []Event // circular; len grows to ringCap then wraps
	ringCap int
	start   int // index of oldest
	count   int
	nextSub int
	subs    map[int]*Subscription
}

// New creates a session actor with an empty graph.
func New(cfg Config) *Actor {
	if cfg.IDs == nil {
		cfg.IDs = domain.NewUUIDv7Source()
	}
	if cfg.Clock == nil {
		cfg.Clock = platform.NewSystemClock()
	}
	if cfg.RingEvents <= 0 {
		cfg.RingEvents = DefaultRingEvents
	}
	return &Actor{
		id:      cfg.ID,
		mailbox: make(chan func(), DefaultMailbox),
		stop:    make(chan struct{}),
		done:    make(chan struct{}),
		state:   domain.NewState(cfg.ID),
		ids:     cfg.IDs,
		clock:   cfg.Clock,
		ringCap: cfg.RingEvents,
		subs:    map[int]*Subscription{},
	}
}

// ID returns the session identity.
func (a *Actor) ID() domain.SessionID { return a.id }

// Start launches the actor goroutine.
func (a *Actor) Start() { go a.run() }

// Stop halts the actor and closes every subscription. Idempotent.
func (a *Actor) Stop() {
	select {
	case <-a.stop:
	default:
		close(a.stop)
	}
	<-a.done
}

func (a *Actor) run() {
	defer func() {
		for _, s := range a.subs {
			s.close(ErrStopped)
		}
		close(a.done)
	}()
	for {
		select {
		case fn := <-a.mailbox:
			fn()
		case <-a.stop:
			return
		}
	}
}

func (a *Actor) do(ctx context.Context, fn func()) error {
	donec := make(chan struct{})
	wrapped := func() { fn(); close(donec) }
	select {
	case a.mailbox <- wrapped:
	case <-ctx.Done():
		return ctx.Err()
	case <-a.stop:
		return ErrStopped
	}
	select {
	case <-donec:
		return nil
	case <-a.stop:
		return ErrStopped
	}
}

// Submit applies one domain command. On success the committed events carry
// freshly allocated, contiguous sequence numbers; on a typed domain error
// nothing is allocated and the state is unchanged (no partial transition).
func (a *Actor) Submit(ctx context.Context, cmd domain.Command) (Result, error) {
	var res Result
	var terr error
	if err := a.do(ctx, func() {
		next, events, err := domain.Apply(a.state, cmd, a.ids)
		if err != nil {
			terr = err
			return
		}
		a.state = next
		res.State = next
		res.Rev = next.Rev
		now := a.clock.NowUnixMilli()
		for _, ev := range events {
			a.seq++ // allocated ONLY after commit (ADR-0004)
			se := Event{
				Session: a.id,
				Seq:     a.seq,
				TimeMS:  now,
				Kind:    KindOf(ev),
				Rev:     eventRev(ev),
				Payload: ev,
			}
			se.Workspace = eventWorkspace(ev)
			a.append(se)
			res.Events = append(res.Events, se)
		}
	}); err != nil {
		return Result{}, err
	}
	if terr != nil {
		return Result{}, terr
	}
	return res, nil
}

// State returns the current committed state. The returned value is never
// mutated again by the actor (Apply clones), so concurrent reads are safe.
func (a *Actor) State(ctx context.Context) (*domain.State, error) {
	var st *domain.State
	if err := a.do(ctx, func() { st = a.state }); err != nil {
		return nil, err
	}
	return st, nil
}

// LatestSeq returns the last allocated event sequence.
func (a *Actor) LatestSeq(ctx context.Context) (uint64, error) {
	var s uint64
	if err := a.do(ctx, func() { s = a.seq }); err != nil {
		return 0, err
	}
	return s, nil
}

// Snapshot atomically exports the graph and the event cursor it corresponds
// to — the pair persistence commits together (ADR-0005 ComponentGraph).
func (a *Actor) Snapshot(ctx context.Context) (*domain.Snapshot, uint64, error) {
	var snap *domain.Snapshot
	var cursor uint64
	if err := a.do(ctx, func() {
		snap = a.state.Export()
		cursor = a.seq
	}); err != nil {
		return nil, 0, err
	}
	return snap, cursor, nil
}

// Restore rehydrates the graph from a snapshot and resumes sequence
// allocation at cursor (the next committed event is cursor+1). It fails
// closed on invariant violations and refuses to run on a non-empty actor.
func (a *Actor) Restore(ctx context.Context, snap *domain.Snapshot, cursor uint64) error {
	var terr error
	if err := a.do(ctx, func() {
		if a.seq != 0 || a.count != 0 || len(a.state.WorkspaceOrder()) != 0 {
			terr = errors.New("session: restore requires a fresh actor")
			return
		}
		st, err := domain.Rehydrate(snap)
		if err != nil {
			terr = err
			return
		}
		a.state = st
		a.seq = cursor
	}); err != nil {
		return err
	}
	return terr
}

// --- bounded replay ring -----------------------------------------------

func (a *Actor) append(e Event) {
	if a.count < a.ringCap {
		a.ring = append(a.ring, e)
		a.count++
	} else {
		a.ring[a.start] = e
		a.start = (a.start + 1) % a.ringCap
	}
	for id, s := range a.subs {
		if s.filter != nil && !s.filter(e) {
			continue
		}
		select {
		case s.ch <- e:
			s.last = e.Seq
		default:
			// Bounded buffer overflowed: disconnect with a receipt rather
			// than blocking the actor or buffering without bound.
			delete(a.subs, id)
			s.close(ErrSlowConsumer)
		}
	}
}

func (a *Actor) ringAt(i int) Event {
	if a.count < a.ringCap {
		return a.ring[i]
	}
	return a.ring[(a.start+i)%a.ringCap]
}

// replayLocked returns retained events with Seq >= from. Runs on the actor
// goroutine. A cursor older than the retained window is a typed gap.
func (a *Actor) replayLocked(from uint64) ([]Event, error) {
	if a.count == 0 {
		// Nothing retained. Events that existed (or a restored cursor)
		// before seq+1 are unreachable — that is a gap, not an empty replay.
		if from <= a.seq {
			return nil, &EventGapError{Requested: from, OldestRetained: a.seq + 1, Latest: a.seq}
		}
		return nil, nil
	}
	oldest := a.ringAt(0).Seq
	if from < oldest {
		return nil, &EventGapError{Requested: from, OldestRetained: oldest, Latest: a.seq}
	}
	var out []Event
	for i := 0; i < a.count; i++ {
		e := a.ringAt(i)
		if e.Seq >= from {
			out = append(out, e)
		}
	}
	return out, nil
}

// Replay returns committed events with Seq >= from, or a typed EventGapError
// when the window no longer reaches back that far.
func (a *Actor) Replay(ctx context.Context, from uint64) ([]Event, error) {
	var out []Event
	var terr error
	if err := a.do(ctx, func() { out, terr = a.replayLocked(from) }); err != nil {
		return nil, err
	}
	return out, terr
}

// --- subscriptions --------------------------------------------------------

// Filter selects which events a subscription receives. nil = all.
type Filter func(Event) bool

// FilterWorkspace keeps only events of one workspace.
func FilterWorkspace(w domain.WorkspaceID) Filter {
	return func(e Event) bool { return e.Workspace == w }
}

// FilterKinds keeps only the named event kinds.
func FilterKinds(kinds ...string) Filter {
	set := map[string]bool{}
	for _, k := range kinds {
		set[k] = true
	}
	return func(e Event) bool { return set[e.Kind] }
}

// SubscribeOptions configures a subscription.
type SubscribeOptions struct {
	// FromSeq starts delivery at this sequence via ring replay, atomically
	// spliced with live events (no gap, no duplicate at the boundary). 0 (or
	// latest+1) means live-only.
	FromSeq uint64
	// Buffer bounds the delivery queue (default DefaultSubscriberBuffer).
	Buffer int
	// Filter selects events (nil = all). Replayed events are filtered too.
	Filter Filter
}

// Subscription is one bounded event feed.
type Subscription struct {
	ch     chan Event
	donec  chan struct{}
	filter Filter
	last   uint64
	err    error
	cancel func()
}

// C is the delivery channel; it closes on disconnect.
func (s *Subscription) C() <-chan Event { return s.ch }

// Err reports why the subscription closed (nil while live). Valid after C is
// closed.
func (s *Subscription) Err() error {
	select {
	case <-s.donec:
		return s.err
	default:
		return nil
	}
}

// LastDelivered is the receipt a disconnected consumer resumes from.
func (s *Subscription) LastDelivered() uint64 { return s.last }

// Cancel detaches the subscription.
func (s *Subscription) Cancel() { s.cancel() }

func (s *Subscription) close(err error) {
	select {
	case <-s.donec:
		return
	default:
	}
	s.err = err
	close(s.donec)
	close(s.ch)
}

// Subscribe registers a bounded, optionally filtered subscription. The
// replay-from-cursor and the switch to live delivery happen atomically on the
// actor goroutine, so the spliced stream is contiguous and duplicate-free
// (ADR-0004 attach/cutover discipline applied to the event feed). A cursor
// before the retained window returns EventGapError.
func (a *Actor) Subscribe(ctx context.Context, opts SubscribeOptions) (*Subscription, error) {
	if opts.Buffer <= 0 {
		opts.Buffer = DefaultSubscriberBuffer
	}
	var sub *Subscription
	var terr error
	if err := a.do(ctx, func() {
		var backlog []Event
		if opts.FromSeq > 0 {
			backlog, terr = a.replayLocked(opts.FromSeq)
			if terr != nil {
				return
			}
			if opts.Filter != nil {
				kept := backlog[:0]
				for _, e := range backlog {
					if opts.Filter(e) {
						kept = append(kept, e)
					}
				}
				backlog = kept
			}
		}
		if len(backlog) > opts.Buffer {
			// The bounded buffer cannot even hold the requested replay: that
			// is a slow consumer by construction; report the gap boundary.
			terr = &EventGapError{Requested: opts.FromSeq, OldestRetained: backlog[len(backlog)-opts.Buffer].Seq, Latest: a.seq}
			return
		}
		id := a.nextSub
		a.nextSub++
		sub = &Subscription{
			ch:     make(chan Event, opts.Buffer),
			donec:  make(chan struct{}),
			filter: opts.Filter,
		}
		sub.cancel = func() {
			_ = a.do(context.Background(), func() {
				if _, ok := a.subs[id]; ok {
					delete(a.subs, id)
					sub.close(nil)
				}
			})
		}
		for _, e := range backlog {
			sub.ch <- e
			sub.last = e.Seq
		}
		a.subs[id] = sub
	}); err != nil {
		return nil, err
	}
	if terr != nil {
		return nil, terr
	}
	return sub, nil
}

// --- event kind projection --------------------------------------------------

// KindOf names a domain event for filters and the wire (stable vocabulary;
// the protocol codec depends on it).
func KindOf(ev domain.Event) string {
	switch ev.(type) {
	case domain.WorkspaceCreated:
		return "workspace_created"
	case domain.WorkspaceRenamed:
		return "workspace_renamed"
	case domain.WorkspaceClosed:
		return "workspace_closed"
	case domain.PaneSplit:
		return "pane_split"
	case domain.PaneClosed:
		return "pane_closed"
	case domain.PaneFocused:
		return "pane_focused"
	case domain.PaneResized:
		return "pane_resized"
	case domain.WorkspaceEqualized:
		return "workspace_equalized"
	case domain.SurfaceSpawned:
		return "surface_spawned"
	case domain.ActiveSurfaceChanged:
		return "active_surface_changed"
	case domain.SurfaceClosed:
		return "surface_closed"
	default:
		return "unknown"
	}
}

func eventWorkspace(ev domain.Event) domain.WorkspaceID {
	switch e := ev.(type) {
	case domain.WorkspaceCreated:
		return e.Workspace
	case domain.WorkspaceRenamed:
		return e.Workspace
	case domain.WorkspaceClosed:
		return e.Workspace
	case domain.PaneSplit:
		return e.Workspace
	case domain.PaneClosed:
		return e.Workspace
	case domain.PaneFocused:
		return e.Workspace
	case domain.PaneResized:
		return e.Workspace
	case domain.WorkspaceEqualized:
		return e.Workspace
	case domain.SurfaceSpawned:
		return e.Workspace
	case domain.ActiveSurfaceChanged:
		return e.Workspace
	case domain.SurfaceClosed:
		return e.Workspace
	default:
		return ""
	}
}

func eventRev(ev domain.Event) uint64 {
	switch e := ev.(type) {
	case domain.WorkspaceCreated:
		return e.Rev
	case domain.WorkspaceRenamed:
		return e.Rev
	case domain.WorkspaceClosed:
		return e.Rev
	case domain.PaneSplit:
		return e.Rev
	case domain.PaneClosed:
		return e.Rev
	case domain.PaneFocused:
		return e.Rev
	case domain.PaneResized:
		return e.Rev
	case domain.WorkspaceEqualized:
		return e.Rev
	case domain.SurfaceSpawned:
		return e.Rev
	case domain.ActiveSurfaceChanged:
		return e.Rev
	case domain.SurfaceClosed:
		return e.Rev
	default:
		return 0
	}
}
