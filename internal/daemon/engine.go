// Package daemon assembles the frozen subsystems into the running amuxd
// runtime: the daemon-global control actor (trust/registry), one session actor
// per session (graph authority), per-surface PTY supervision feeding a bounded
// raw-output ring and a VT engine, the SQLite store, snapshot persistence, the
// hook runtime, notifications, and observability. It is the single durable
// authority (ADR-0001): the CLI and TUI reach it only through the protocol,
// and every mutation is one session-actor command — there is no second
// authority and no CLI-only mutation path.
//
// engine.go is the transport-independent core: the Engine type and the methods
// the protocol handlers call. Wiring to the socket lives in server.go so the
// core is unit-testable without a live connection.
package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"sort"
	"sync"

	v1 "github.com/amux-run/amux/api/v1"

	"github.com/amux-run/amux/internal/attach"
	panectx "github.com/amux-run/amux/internal/context"
	"github.com/amux-run/amux/internal/control"
	"github.com/amux-run/amux/internal/domain"
	"github.com/amux-run/amux/internal/persist"
	"github.com/amux-run/amux/internal/platform"
	"github.com/amux-run/amux/internal/pty"
	"github.com/amux-run/amux/internal/session"
	"github.com/amux-run/amux/internal/store"
	"github.com/amux-run/amux/internal/terminal"
)

// DefaultReplayBytes is the per-surface raw replay floor (16 MiB, ADR/spec).
const DefaultReplayBytes = 16 << 20

// PTYFactory builds the PTY seam. Production wires pty.New(); tests inject a
// fake so surface spawning is hermetic and no real process is created.
type PTYFactory func() platform.PTY

// Deps wires the engine's subsystems.
type Deps struct {
	Control *control.Actor
	Clock   platform.Clock
	PTY     PTYFactory
	// Store, when non-nil, bundles the notification export into each snapshot
	// generation and re-imports the committed export on an explicit restore
	// (ADR-0005: the export is the ONLY thing a restore may import).
	Store       *store.Store
	SnapshotDir string
	ReplayBytes int

	// GitContext, when non-nil, is the bounded git collector behind the
	// pane.context projection (B10). Production wires
	// (&context.GitCollector{}).Collect; nil leaves the git fields
	// undetermined — absent context fails closed, never fabricated.
	GitContext func(ctx context.Context, cwd string) (panectx.GitInfo, error)
	// Foreground, when non-nil, probes the foreground process of a PTY master
	// for pane.context (B10). Production wires context.ForegroundCollector
	// over platform.NewLinuxProcessInspector; nil (or a fail-closed non-Linux
	// inspector) leaves the foreground fields undetermined.
	Foreground func(masterFD uintptr) (pid int, cmd string, err error)
}

// Engine owns every session runtime. It is safe for concurrent use; per-session
// mutation is serialized by that session's actor, and the engine's own maps are
// guarded by mu.
type Engine struct {
	deps Deps

	mu       sync.Mutex
	sessions map[domain.SessionID]*sessionRuntime
	// saved records, per session and in daemon memory ONLY, which live spawn
	// identities the latest snapshot generation captured. It is the sole
	// evidence that can make a restore classify a surface live: an in-daemon
	// restore of the exact recorded checkpoint may reconcile a surface whose
	// identical spawn identity this daemon still owns; a fresh daemon has no
	// record, so it is structurally excluded from live (ADR-0005) without any
	// persisted-format change.
	saved map[domain.SessionID]savedIdentity
}

// savedIdentity is one SaveSnapshot's ownership attestation: the checkpoint it
// committed and the spawn identity of every surface that was live at capture.
type savedIdentity struct {
	checkpoint string            // manifest CheckpointID of the committed generation
	spawnIDs   map[string]string // surface id -> spawnID of the live process at capture
}

// sessionRuntime is one live session: its graph actor plus the per-surface
// PTY/ring/engine/attach runtime keyed by surface id.
type sessionRuntime struct {
	sup *pty.Supervisor
	mu  sync.Mutex
	// actor is guarded by mu because an in-daemon restore swaps in the
	// rehydrated actor; read it through graphActor().
	actor    *session.Actor
	surfaces map[domain.SurfaceID]*surfaceRuntime
}

// graphActor returns the session's current graph actor. An in-daemon restore
// replaces the pointer under mu, so every reader takes it through here.
func (rt *sessionRuntime) graphActor() *session.Actor {
	rt.mu.Lock()
	defer rt.mu.Unlock()
	return rt.actor
}

// surfaceRuntime is the durable+ephemeral runtime for one terminal surface.
type surfaceRuntime struct {
	id      domain.SurfaceID
	ring    *terminal.Ring
	engine  *terminal.Engine
	surface *attach.Surface // observer fanout + input lease (ADR-0004)
	argv    []string
	cwd     string
	env     []string
	policy  persist.RestartPolicy
	// class/reason describe a restored surface's classification (empty for a
	// freshly spawned live surface).
	class  persist.SurfaceClass
	reason string
	live   bool
	// exitCode is the recorded numeric exit of the LAST process incarnation
	// (nil until an exit is evented). Exposed read-only by pane.context.
	exitCode *int
	// spawnID identifies the CURRENT process incarnation: every spawn/restart
	// mints a new one. SaveSnapshot records the live spawnIDs per checkpoint so
	// an in-daemon restore can prove "same still-owned PTY identity" before
	// classifying live (ADR-0005 precedence rule 2).
	spawnID string
}

// New builds an Engine. Control is required and must already be Started.
func New(deps Deps) (*Engine, error) {
	if deps.Control == nil {
		return nil, errors.New("daemon: Deps.Control is required")
	}
	if deps.Clock == nil {
		deps.Clock = platform.NewSystemClock()
	}
	if deps.PTY == nil {
		deps.PTY = pty.New
	}
	if deps.ReplayBytes <= 0 {
		deps.ReplayBytes = DefaultReplayBytes
	}
	return &Engine{
		deps:     deps,
		sessions: map[domain.SessionID]*sessionRuntime{},
		saved:    map[domain.SessionID]savedIdentity{},
	}, nil
}

// Close stops every session actor and supervisor, reaping all PTYs (clean
// shutdown: zero orphaned processes).
func (e *Engine) Close() {
	e.mu.Lock()
	rts := make([]*sessionRuntime, 0, len(e.sessions))
	for _, rt := range e.sessions {
		rts = append(rts, rt)
	}
	e.sessions = map[domain.SessionID]*sessionRuntime{}
	e.mu.Unlock()
	for _, rt := range rts {
		if rt.sup != nil {
			_ = rt.sup.StopAll()
		}
		rt.graphActor().Stop()
	}
}

func (e *Engine) runtime(id domain.SessionID) (*sessionRuntime, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	rt, ok := e.sessions[id]
	if !ok {
		return nil, &engineError{code: v1.ErrNotFound, msg: "session not found: " + string(id)}
	}
	return rt, nil
}

// --- sessions ---------------------------------------------------------------

// CreateSession registers a new session and starts its graph actor.
func (e *Engine) CreateSession(ctx context.Context, name string) (control.SessionInfo, error) {
	sid := domain.SessionID(control.NewID())
	act := session.New(session.Config{ID: sid, IDs: domain.NewUUIDv7Source(), Clock: e.deps.Clock})
	act.Start()
	rt := &sessionRuntime{actor: act, surfaces: map[domain.SurfaceID]*surfaceRuntime{}}
	sup, err := ptySupervisor(e, rt)
	if err != nil {
		act.Stop()
		return control.SessionInfo{}, err
	}
	rt.sup = sup
	info := control.SessionInfo{ID: string(sid), Name: name, CreatedMS: e.deps.Clock.NowUnixMilli()}
	if err := e.deps.Control.RegisterSession(ctx, info); err != nil {
		act.Stop()
		_ = sup.StopAll()
		return control.SessionInfo{}, err
	}
	e.mu.Lock()
	e.sessions[sid] = rt
	e.mu.Unlock()
	return info, nil
}

// ptySupervisor builds a supervisor whose callbacks route master output into
// the owning surface's ring + VT engine and record each exit classification
// exactly once. The callbacks look the surface up by id, so a chunk for a
// closed surface is harmlessly dropped.
func ptySupervisor(e *Engine, rt *sessionRuntime) (*pty.Supervisor, error) {
	return pty.NewSupervisor(pty.SupervisorConfig{
		PTY:      e.deps.PTY(),
		Clock:    e.deps.Clock,
		OnOutput: func(id string, p []byte) { rt.onOutput(domain.SurfaceID(id), p) },
		OnExit: func(id string, exit platform.PTYExit, reason string) {
			rt.onExit(domain.SurfaceID(id), exit, reason)
		},
	})
}

// onOutput appends a master-output chunk to the surface's replay ring (the raw
// output authority, ADR-0005) and feeds the VT engine that derives the cell
// grid. A chunk for an unknown/closed surface is dropped.
func (rt *sessionRuntime) onOutput(id domain.SurfaceID, p []byte) {
	rt.mu.Lock()
	sr := rt.surfaces[id]
	rt.mu.Unlock()
	if sr == nil {
		return
	}
	// Feed the VT engine (derived grid) then hand the chunk to the attach
	// surface, which appends to the raw-authority ring AND fans out to live
	// observers under one lock (ADR-0004 cutover discipline).
	sr.engine.Feed(p)
	if sr.surface != nil {
		_, _ = sr.surface.OnOutput(p)
	} else {
		_, _ = sr.ring.Append(p)
	}
}

// onExit records the terminal's exit classification exactly once. Detach never
// reaches here — only real process exit or a stop does (ADR-0004 exit is
// evented once).
func (rt *sessionRuntime) onExit(id domain.SurfaceID, exit platform.PTYExit, reason string) {
	rt.mu.Lock()
	sr := rt.surfaces[id]
	if sr != nil {
		sr.live = false
		sr.class = persist.ClassStopped
		if exit.Signal != "" {
			sr.reason = "signaled: " + exit.Signal
		} else {
			sr.reason = reason
		}
		code := exit.Code
		sr.exitCode = &code
	}
	rt.mu.Unlock()
}

// ListSessions returns the registry entries.
func (e *Engine) ListSessions(ctx context.Context) ([]control.SessionInfo, error) {
	return e.deps.Control.ListSessions(ctx)
}

// DestroySession stops a session's actor + PTYs and unregisters it.
func (e *Engine) DestroySession(ctx context.Context, id domain.SessionID) error {
	e.mu.Lock()
	rt, ok := e.sessions[id]
	if ok {
		delete(e.sessions, id)
	}
	delete(e.saved, id)
	e.mu.Unlock()
	if !ok {
		return &engineError{code: v1.ErrNotFound, msg: "session not found"}
	}
	_ = rt.sup.StopAll()
	rt.graphActor().Stop()
	return e.deps.Control.UnregisterSession(ctx, string(id))
}

// --- graph commands ---------------------------------------------------------

// submit applies a domain command through the session actor and returns the
// committed result, mapping domain errors to the protocol taxonomy.
func (e *Engine) submit(ctx context.Context, id domain.SessionID, cmd domain.Command) (session.Result, error) {
	rt, err := e.runtime(id)
	if err != nil {
		return session.Result{}, err
	}
	res, err := rt.graphActor().Submit(ctx, cmd)
	if err != nil {
		return session.Result{}, mapDomainError(err)
	}
	return res, nil
}

// state returns a read-only snapshot of the session graph.
func (e *Engine) state(ctx context.Context, id domain.SessionID) (*domain.State, error) {
	rt, err := e.runtime(id)
	if err != nil {
		return nil, err
	}
	return rt.graphActor().State(ctx)
}

// mapDomainError translates a domain *Error into the protocol taxonomy.
func mapDomainError(err error) error {
	switch domain.CodeOf(err) {
	case domain.CodeInvalidArgument:
		return &engineError{code: v1.ErrInvalidArgument, msg: err.Error()}
	case domain.CodeNotFound:
		return &engineError{code: v1.ErrNotFound, msg: err.Error()}
	case domain.CodeConflict:
		return &engineError{code: v1.ErrConflict, msg: err.Error()}
	case domain.CodeInternal:
		return &engineError{code: v1.ErrInternal, msg: err.Error()}
	default:
		return err
	}
}

// engineError carries a protocol taxonomy code plus optional structured
// details for the wire ErrorBody (automation branches on code + details;
// the message is human diagnostics only).
type engineError struct {
	code    v1.ErrorCode
	msg     string
	details json.RawMessage
}

func (e *engineError) Error() string { return string(e.code) + ": " + e.msg }

// errorDetails marshals v into the details payload of an engineError,
// dropping the details (never the error) on a marshal failure.
func errorDetails(v any) json.RawMessage {
	b, err := json.Marshal(v)
	if err != nil {
		return nil
	}
	return b
}

// Code returns the taxonomy code of err, or "" for a plain error.
func Code(err error) v1.ErrorCode {
	var ee *engineError
	if errors.As(err, &ee) {
		return ee.code
	}
	return v1.ErrInternal
}

// ErrorBody converts err into a wire ErrorBody with a code from the taxonomy.
func ErrorBody(err error) *v1.ErrorBody {
	var ee *engineError
	if errors.As(err, &ee) {
		return &v1.ErrorBody{Code: ee.code, Message: ee.msg, Retryable: ee.code == v1.ErrResourceExhausted, Details: ee.details}
	}
	return &v1.ErrorBody{Code: v1.ErrInternal, Message: err.Error()}
}

// sortedSurfaceIDs is a deterministic helper for inspect ordering.
func sortedSurfaceIDs(m map[domain.SurfaceID]*surfaceRuntime) []domain.SurfaceID {
	ids := make([]domain.SurfaceID, 0, len(m))
	for id := range m {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	return ids
}
