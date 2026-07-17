package ordering

import "sync"

// ProjectID and epoch model the trust state the daemon-global control actor owns
// (ADR-0001/ADR-0005). An epoch is monotonic per project; revocation bumps it.
type ProjectID string

// launchReq asks the control actor to authorize a hook launch for a project at
// the epoch the caller last observed. The control actor is the single
// linearization point: it compares reqEpoch against the current epoch under its
// own goroutine and replies. authorized is true only if the project is still
// trusted at reqEpoch.
type launchReq struct {
	project  ProjectID
	reqEpoch uint64
	reply    chan launchResp
}

type launchResp struct {
	authorized bool
	epoch      uint64
}

type revokeReq struct {
	project ProjectID
	reply   chan uint64 // new (bumped) epoch
}

// ControlActor owns project trust epochs and serializes every launch
// authorization and revocation through a single goroutine. It never calls back
// into a session actor while holding state; it only replies on a channel. That
// one-way discipline is what removes nested-wait deadlock (ADR-0001).
type ControlActor struct {
	launches chan launchReq
	revokes  chan revokeReq
	stop     chan struct{}

	// epochs is owned exclusively by run(); no external goroutine touches it.
	epochs map[ProjectID]uint64
	// trusted marks whether a project currently has trust (revoke clears it).
	trusted map[ProjectID]bool

	// childrenMu guards the audit tally shared with the test harness. It is not
	// part of the linearization; it only records outcomes.
	childrenMu sync.Mutex
	children   map[ProjectID]int
}

// NewControlActor creates a control actor with the given projects pre-trusted at
// epoch 1.
func NewControlActor(projects ...ProjectID) *ControlActor {
	c := &ControlActor{
		launches: make(chan launchReq),
		revokes:  make(chan revokeReq),
		stop:     make(chan struct{}),
		epochs:   map[ProjectID]uint64{},
		trusted:  map[ProjectID]bool{},
		children: map[ProjectID]int{},
	}
	for _, p := range projects {
		c.epochs[p] = 1
		c.trusted[p] = true
	}
	return c
}

// Run drives the control actor's single goroutine. Call in a goroutine; stop
// with Stop.
func (c *ControlActor) Run() {
	for {
		select {
		case req := <-c.launches:
			// Linearization point: authorize iff still trusted at the requested
			// epoch. A revoke processed earlier in this same select loop has
			// already cleared trust / bumped the epoch, so a stale-epoch launch
			// fails here — it can never "linearize after" the revoke.
			ok := c.trusted[req.project] && c.epochs[req.project] == req.reqEpoch
			if ok {
				c.childrenMu.Lock()
				c.children[req.project]++
				c.childrenMu.Unlock()
			}
			req.reply <- launchResp{authorized: ok, epoch: c.epochs[req.project]}
		case req := <-c.revokes:
			c.trusted[req.project] = false
			c.epochs[req.project]++
			req.reply <- c.epochs[req.project]
		case <-c.stop:
			return
		}
	}
}

// Stop halts the actor goroutine.
func (c *ControlActor) Stop() { close(c.stop) }

// Authorize asks the control actor to authorize a hook launch for project at
// the epoch the caller last observed. It returns whether the launch is
// authorized and the project's current epoch. The check runs on the actor's
// single goroutine, so it is the sole linearization point: a launch authorized
// here can never have been ordered after a revoke of the same epoch.
func (c *ControlActor) Authorize(project ProjectID, observedEpoch uint64) (bool, uint64) {
	reply := make(chan launchResp, 1)
	c.launches <- launchReq{project: project, reqEpoch: observedEpoch, reply: reply}
	r := <-reply
	return r.authorized, r.epoch
}

// Revoke revokes a project's trust and returns the new epoch. After Revoke
// returns, no Authorize with a pre-revoke epoch can succeed.
func (c *ControlActor) Revoke(project ProjectID) uint64 {
	reply := make(chan uint64, 1)
	c.revokes <- revokeReq{project: project, reply: reply}
	return <-reply
}

// Children returns the audited count of authorized launches for a project.
func (c *ControlActor) Children(project ProjectID) int {
	c.childrenMu.Lock()
	defer c.childrenMu.Unlock()
	return c.children[project]
}
