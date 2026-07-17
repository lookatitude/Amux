package pty

import (
	"fmt"
	"log/slog"
	"os"
	"sort"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/amux-run/amux/internal/platform"
)

// Exit reasons reported through SupervisorConfig.OnExit. Exactly one reason is
// evented per process lifetime (ADR-0004 exit-reason-evented-once).
const (
	// ReasonExited: the child terminated on its own with a normal exit status.
	ReasonExited = "exited"
	// ReasonSignaled: the child was terminated by a signal the Supervisor did
	// not send as part of a Stop (e.g. an external kill or a client Signal).
	ReasonSignaled = "signaled"
	// ReasonStopped: termination was initiated by Stop (graceful SIGTERM, with
	// SIGKILL escalation after the grace period if needed).
	ReasonStopped = "stopped"
	// ReasonDaemonShutdown: termination was initiated by StopAll, the
	// daemon-shutdown path.
	ReasonDaemonShutdown = "daemon_shutdown"
)

// defaultGraceMS is the SIGTERM→SIGKILL escalation grace applied when
// SupervisorConfig.GraceMS is zero or negative.
const defaultGraceMS = 2000

// outputDrainWindow bounds how long the reaper waits for the output pump to
// hit EOF before closing the master and reporting the exit. In the common case
// EOF arrives immediately after the child dies, so all output is delivered
// before OnExit; a grandchild holding the slave open cannot delay the exit
// event past this window (detach/exit must never hang on a lingering fd).
const outputDrainWindow = 500 * time.Millisecond

// ConflictError is returned by Spawn when the id already names a live process
// (one live process per id).
type ConflictError struct{ ID string }

func (e *ConflictError) Error() string {
	return fmt.Sprintf("pty: supervisor: process %q already live (conflict)", e.ID)
}

// NotFoundError is returned by Input/Resize/Signal when no live process has
// the given id.
type NotFoundError struct{ ID string }

func (e *NotFoundError) Error() string {
	return fmt.Sprintf("pty: supervisor: no live process %q (not_found)", e.ID)
}

// SupervisorConfig wires a Supervisor. PTY is required; everything else has a
// safe default. Callbacks are invoked from Supervisor-owned goroutines and
// must not block indefinitely.
type SupervisorConfig struct {
	// PTY is the spawn seam (required).
	PTY platform.PTY
	// Clock is the injectable time source used for lifecycle timestamps in
	// logs; nil selects the production system clock. Escalation deadlines are
	// real timers (time.AfterFunc with GraceMS) — see Stop.
	Clock platform.Clock
	// Logger receives lifecycle diagnostics; nil discards.
	Logger *slog.Logger
	// GraceMS is the SIGTERM→SIGKILL escalation grace in milliseconds;
	// zero/negative selects the 2000 ms default.
	GraceMS int64
	// OnExit is invoked exactly once per process lifetime with the exit
	// classification and reason.
	OnExit func(id string, exit platform.PTYExit, reason string)
	// OnOutput receives master-side output chunks (the slice is owned by the
	// callee; the Supervisor never reuses it).
	OnOutput func(id string, p []byte)
	// Containment is the optional daemon-death containment seam. When non-nil
	// the Supervisor prepares a handle per spawn and prefers KillTree over
	// group SIGKILL on escalation. On Linux this is the cgroup-v2 mechanism
	// (runtime evidence deferred per ADR-0006); on non-Linux hosts it is nil
	// and the portable baseline is process-group signaling — a TEST baseline,
	// not a support claim.
	Containment platform.Containment
}

// pidReporter is the unexported seam through which the Supervisor learns a
// handle's child pid (for orphan accounting, group diagnostics, and
// containment enrollment). The production handle implements it; a fake PTY
// that does not simply opts out of pid-based features.
type pidReporter interface{ PID() int }

// pidEnroller matches the Linux containment handle's post-start enrollment
// hook (cgroup.procs write). Asserted dynamically so this file stays portable.
type pidEnroller interface{ AddPID(pid int) error }

// pidRecord tracks one spawned child for OrphanScan: the pid and whether its
// handle has been reaped. Records are retained after exit on purpose — the
// whole point is finding pids that outlive their handle.
type pidRecord struct {
	id     string
	pid    int
	exited bool
}

// process is one supervised child.
type process struct {
	id          string
	handle      platform.PTYHandle
	pid         int
	containment platform.ContainmentHandle // nil when no containment seam
	record      *pidRecord                 // nil when the handle reports no pid

	// done is closed after OnExit has been invoked (the process is fully
	// retired).
	done chan struct{}
	// readerDone is closed when the output pump has hit EOF.
	readerDone chan struct{}

	mu        sync.Mutex
	stopping  bool
	reason    string // set by stop(); empty until then
	killTimer *time.Timer
}

// Supervisor manages the live PTY processes of one daemon: spawn, I/O
// forwarding, resize, signaling, graceful stop with forced escalation, and
// exactly-once exit reporting.
type Supervisor struct {
	cfg   SupervisorConfig
	grace time.Duration

	mu      sync.Mutex
	procs   map[string]*process
	records []*pidRecord

	// escalations counts grace-period SIGKILL escalations, so tests and the
	// harness can assert a forced kill actually happened (vs. graceful exit).
	escalations atomic.Int64
}

// NewSupervisor validates cfg and returns a Supervisor. It fails closed when
// the PTY seam is absent.
func NewSupervisor(cfg SupervisorConfig) (*Supervisor, error) {
	if cfg.PTY == nil {
		return nil, fmt.Errorf("pty: supervisor: nil PTY seam")
	}
	if cfg.Clock == nil {
		cfg.Clock = platform.NewSystemClock()
	}
	if cfg.Logger == nil {
		cfg.Logger = slog.New(slog.DiscardHandler)
	}
	if cfg.GraceMS <= 0 {
		cfg.GraceMS = defaultGraceMS
	}
	return &Supervisor{
		cfg:   cfg,
		grace: time.Duration(cfg.GraceMS) * time.Millisecond,
		procs: make(map[string]*process),
	}, nil
}

// Spawn launches spec under id. At most one live process may exist per id; a
// second Spawn while the first is live returns a *ConflictError (the id
// becomes reusable once OnExit for the previous process has fired). On
// success a reader goroutine pumps master output to OnOutput in bounded
// chunks until EOF, and a waiter goroutine reaps exactly once and events
// OnExit exactly once.
func (s *Supervisor) Spawn(id string, spec platform.PTYSpec) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.procs[id]; ok {
		return &ConflictError{ID: id}
	}

	var ch platform.ContainmentHandle
	if s.cfg.Containment != nil {
		prepared, err := s.cfg.Containment.Prepare(spec.Containment)
		if err != nil {
			return fmt.Errorf("pty: supervisor: prepare containment for %q: %w", id, err)
		}
		ch = prepared
	}

	handle, err := s.cfg.PTY.Start(spec)
	if err != nil {
		if ch != nil {
			_ = ch.Close()
		}
		return fmt.Errorf("pty: supervisor: spawn %q: %w", id, err)
	}

	p := &process{
		id:          id,
		handle:      handle,
		containment: ch,
		done:        make(chan struct{}),
		readerDone:  make(chan struct{}),
	}
	if pr, ok := handle.(pidReporter); ok {
		p.pid = pr.PID()
		p.record = &pidRecord{id: id, pid: p.pid}
		s.records = append(s.records, p.record)
	}
	// Enroll the fresh child into containment (Linux: cgroup.procs) before it
	// can double-fork away.
	if ch != nil && p.pid > 0 {
		if en, ok := ch.(pidEnroller); ok {
			if err := en.AddPID(p.pid); err != nil {
				s.cfg.Logger.Warn("containment enrollment failed",
					"id", id, "pid", p.pid, "err", err)
			}
		}
	}
	s.procs[id] = p

	s.cfg.Logger.Info("spawned", "id", id, "pid", p.pid,
		"at_unix_ms", s.cfg.Clock.NowUnixMilli())
	go s.readLoop(p)
	go s.waitLoop(p)
	return nil
}

// readLoop pumps master output into OnOutput in bounded 32 KiB chunks until
// the read fails (EOF, EIO after child death, or master closed by the reaper).
func (s *Supervisor) readLoop(p *process) {
	defer close(p.readerDone)
	buf := make([]byte, 32*1024)
	for {
		n, err := p.handle.Read(buf)
		if n > 0 && s.cfg.OnOutput != nil {
			chunk := make([]byte, n)
			copy(chunk, buf[:n])
			s.cfg.OnOutput(p.id, chunk)
		}
		if err != nil {
			return
		}
	}
}

// waitLoop reaps the child exactly once, drains pending output (bounded),
// releases resources, retires the id, and events OnExit exactly once with the
// resolved reason.
func (s *Supervisor) waitLoop(p *process) {
	exit, werr := p.handle.Wait()

	p.mu.Lock()
	reason := p.reason
	timer := p.killTimer
	p.mu.Unlock()
	if timer != nil {
		timer.Stop()
	}
	if reason == "" {
		if exit.Signal != "" {
			reason = ReasonSignaled
		} else {
			reason = ReasonExited
		}
	}
	if werr != nil {
		s.cfg.Logger.Error("wait failed", "id", p.id, "err", werr)
	}

	// Give the output pump a bounded window to hit EOF so OnExit ordinarily
	// follows the last output chunk; then close the master to unblock a pump
	// held open by a lingering slave-side fd.
	select {
	case <-p.readerDone:
	case <-time.After(outputDrainWindow):
	}
	_ = p.handle.Close()
	<-p.readerDone

	if p.containment != nil {
		if err := p.containment.Close(); err != nil {
			s.cfg.Logger.Warn("containment close failed", "id", p.id, "err", err)
		}
	}

	s.mu.Lock()
	if s.procs[p.id] == p {
		delete(s.procs, p.id)
	}
	if p.record != nil {
		p.record.exited = true
	}
	s.mu.Unlock()

	s.cfg.Logger.Info("exited", "id", p.id, "pid", p.pid, "reason", reason,
		"code", exit.Code, "signal", exit.Signal,
		"at_unix_ms", s.cfg.Clock.NowUnixMilli())
	if s.cfg.OnExit != nil {
		s.cfg.OnExit(p.id, exit, reason)
	}
	close(p.done)
}

// Input writes p to the process's PTY master. Returns *NotFoundError when id
// has no live process.
func (s *Supervisor) Input(id string, p []byte) error {
	proc, err := s.lookup(id)
	if err != nil {
		return err
	}
	if _, err := proc.handle.Write(p); err != nil {
		return fmt.Errorf("pty: supervisor: input to %q: %w", id, err)
	}
	return nil
}

// Resize applies new geometry to the process's PTY.
func (s *Supervisor) Resize(id string, size platform.PTYSize) error {
	proc, err := s.lookup(id)
	if err != nil {
		return err
	}
	return proc.handle.Resize(size)
}

// Signal delivers sig to the process's whole process group.
func (s *Supervisor) Signal(id string, sig os.Signal) error {
	proc, err := s.lookup(id)
	if err != nil {
		return err
	}
	return proc.handle.Signal(sig)
}

// Stop terminates id gracefully: SIGTERM to the process group now, SIGKILL
// escalation (via containment KillTree when available) after GraceMS. It does
// not block until the exit is reaped — the exit is evented through OnExit as
// usual, with reason "stopped". Stopping an unknown/already-exited id and
// double Stop are clean no-ops.
func (s *Supervisor) Stop(id string) error {
	s.mu.Lock()
	p, ok := s.procs[id]
	s.mu.Unlock()
	if !ok {
		return nil
	}
	s.stop(p, ReasonStopped)
	return nil
}

// stop initiates graceful-then-forceful termination with the given exit
// reason. Idempotent per process: only the first call arms the escalation.
func (s *Supervisor) stop(p *process, reason string) {
	p.mu.Lock()
	if p.stopping {
		p.mu.Unlock()
		return
	}
	p.stopping = true
	p.reason = reason
	p.mu.Unlock()

	// SIGTERM the group. Failure (e.g. already dead) is fine: the waiter is
	// reaping it concurrently.
	if err := p.handle.Signal(syscall.SIGTERM); err != nil {
		s.cfg.Logger.Debug("stop SIGTERM failed", "id", p.id, "err", err)
	}
	t := time.AfterFunc(s.grace, func() { s.escalate(p) })
	p.mu.Lock()
	p.killTimer = t
	p.mu.Unlock()
	// The waiter may have retired the process between arming and recording
	// the timer; make the late timer harmless.
	select {
	case <-p.done:
		t.Stop()
	default:
	}
}

// escalate forces termination after the grace period: containment KillTree
// when available (reaps double-forked grandchildren on Linux), otherwise
// SIGKILL to the process group.
func (s *Supervisor) escalate(p *process) {
	select {
	case <-p.done:
		return // already retired; nothing to force
	default:
	}
	s.escalations.Add(1)
	s.cfg.Logger.Warn("escalating to SIGKILL", "id", p.id, "pid", p.pid)
	if p.containment != nil {
		if err := p.containment.KillTree(); err == nil {
			return
		} else {
			s.cfg.Logger.Warn("containment KillTree failed; falling back to group SIGKILL",
				"id", p.id, "err", err)
		}
	}
	if err := p.handle.Signal(syscall.SIGKILL); err != nil {
		s.cfg.Logger.Debug("escalation SIGKILL failed", "id", p.id, "err", err)
	}
}

// StopAll is the daemon-shutdown path: it stops every live process (reason
// "daemon_shutdown"), waits — bounded by the grace period plus a fixed
// margin — for every reap, and reports any process that failed to retire. A
// clean return means every supervised child was reaped; the T6 harness then
// asserts zero orphans via OrphanScan.
func (s *Supervisor) StopAll() error {
	s.mu.Lock()
	live := make([]*process, 0, len(s.procs))
	for _, p := range s.procs {
		live = append(live, p)
	}
	s.mu.Unlock()

	for _, p := range live {
		s.stop(p, ReasonDaemonShutdown)
	}

	bound := s.grace + outputDrainWindow + 5*time.Second
	deadline := time.NewTimer(bound)
	defer deadline.Stop()
	var stuck []string
	for _, p := range live {
		select {
		case <-p.done:
		case <-deadline.C:
			stuck = append(stuck, p.id)
		}
	}
	if len(stuck) > 0 {
		sort.Strings(stuck)
		return fmt.Errorf("pty: supervisor: StopAll: %d process(es) not reaped within %v: %v",
			len(stuck), bound, stuck)
	}
	return nil
}

// Live returns the ids of all live processes, sorted.
func (s *Supervisor) Live() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]string, 0, len(s.procs))
	for id := range s.procs {
		out = append(out, id)
	}
	sort.Strings(out)
	return out
}

// Alive reports whether id names a live (spawned, not yet retired) process.
func (s *Supervisor) Alive(id string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, ok := s.procs[id]
	return ok
}

// MasterFD returns the live process's PTY master descriptor, solely for
// platform.ProcessInspector.ForegroundPID queries (B10 pane context). ok is
// false when id has no live process. Callers must not read, write, or close
// the descriptor.
func (s *Supervisor) MasterFD(id string) (fd uintptr, ok bool) {
	s.mu.Lock()
	p := s.procs[id]
	s.mu.Unlock()
	if p == nil || p.handle == nil {
		return 0, false
	}
	return p.handle.MasterFD(), true
}

// OrphanScan is the forced-cleanup check: it probes every recorded child pid
// whose handle has already exited and returns the pids probe still reports
// alive — i.e. orphans that outlived their supervision. probe is typically
// kill(pid, 0) (unix.ESRCH means gone). Used by the test suite and the T6
// zero-orphan harness after StopAll.
func (s *Supervisor) OrphanScan(probe func(pid int) bool) []int {
	s.mu.Lock()
	exited := make([]pidRecord, 0, len(s.records))
	for _, r := range s.records {
		if r.exited {
			exited = append(exited, *r)
		}
	}
	s.mu.Unlock()

	var orphans []int
	for _, r := range exited {
		if probe(r.pid) {
			orphans = append(orphans, r.pid)
		}
	}
	return orphans
}

// Escalations reports how many grace-period SIGKILL escalations have fired
// since construction — observability for tests and the T6 harness (a graceful
// stop keeps this at zero).
func (s *Supervisor) Escalations() int64 { return s.escalations.Load() }

// lookup returns the live process for id or a typed *NotFoundError.
func (s *Supervisor) lookup(id string) (*process, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	p, ok := s.procs[id]
	if !ok {
		return nil, &NotFoundError{ID: id}
	}
	return p, nil
}
