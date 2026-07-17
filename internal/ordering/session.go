package ordering

// SessionActor models a per-session event-loop goroutine (ADR-0001). It owns a
// monotonic revision and event-sequence counter and allocates a sequence ONLY
// after a command commits (ADR-0004). All mutation flows through one goroutine,
// so sequences are contiguous and gap-free without locks on the hot path.
type SessionActor struct {
	commands chan commitReq
	stop     chan struct{}

	// Owned exclusively by run().
	rev uint64
	seq uint64
	log []uint64 // committed sequence numbers, in commit order
}

type commitReq struct {
	// commit reports whether this command validated; only committed commands
	// allocate a sequence (a rejected command allocates nothing — no gap).
	commit bool
	reply  chan commitResp
}

type commitResp struct {
	committed bool
	rev       uint64
	seq       uint64 // 0 if not committed
}

// NewSessionActor creates a session actor.
func NewSessionActor() *SessionActor {
	return &SessionActor{
		commands: make(chan commitReq),
		stop:     make(chan struct{}),
	}
}

// Run drives the actor goroutine.
func (s *SessionActor) Run() {
	for {
		select {
		case req := <-s.commands:
			if !req.commit {
				// Rejected: no sequence allocated, no gap introduced.
				req.reply <- commitResp{committed: false, rev: s.rev}
				continue
			}
			s.rev++
			s.seq++
			s.log = append(s.log, s.seq)
			req.reply <- commitResp{committed: true, rev: s.rev, seq: s.seq}
		case <-s.stop:
			return
		}
	}
}

// Stop halts the actor goroutine.
func (s *SessionActor) Stop() { close(s.stop) }

// Submit sends a command; commit reflects whether it validated. Returns the
// resulting revision and (for committed commands) the allocated sequence.
func (s *SessionActor) Submit(commit bool) commitResp {
	reply := make(chan commitResp, 1)
	s.commands <- commitReq{commit: commit, reply: reply}
	return <-reply
}

// Log returns the committed sequence numbers in commit order. Called after Stop.
func (s *SessionActor) Log() []uint64 { return append([]uint64(nil), s.log...) }
