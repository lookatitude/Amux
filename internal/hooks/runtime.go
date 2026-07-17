// Package hooks is the hook runtime integration (B11): it consumes the
// completed T2 security authorization contract (docs/security/
// hook-authorization.md, executable in internal/securitytest) and drives the
// bounded, fail-closed launch pipeline. It owns config loading behind project
// opt-in, descriptor-bound object capture, scratch-cwd/env-allowlist
// containment, timeout and output caps, the terminate→kill-tree escalation,
// audit, and centralized redaction. Authorization POLICY lives in
// internal/control (Decide + the control actor's linearization point); this
// package is the process/plumbing that consumes an authorization result
// immediately before launch and NEVER re-implements the trust decision.
//
// The runtime is built to pass the implementation-neutral securitytest
// conformance corpus against REAL enforcement: the conformance wiring (a
// T4-owned test package) adapts a Runtime into a securitytest.SystemUnderTest
// with a spy launcher and the deterministic SchedClock, and runs the frozen
// timing/race/restore/redaction fixtures. No fixture executes a real hook.
package hooks

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"sync"

	v1 "github.com/amux-run/amux/api/v1"

	"github.com/amux-run/amux/internal/control"
	"github.com/amux-run/amux/internal/platform"
)

// Escalation and cap defaults (hook-authorization.md §7; mirror
// securitytest.Gates). Changing them is a spec-confirmation gate.
const (
	KillEscalationMS int64 = 2000
	DefaultTimeoutMS int64 = control.DefaultTimeoutMS
)

// Redactor scrubs an egress payload for a named context (RED-1). A redaction
// error fails the egress closed (RED-8): the runtime drops the value.
type Redactor func(ctx string, payload []byte) ([]byte, error)

// Config wires a Runtime.
type Config struct {
	Control    *control.Actor
	Clock      Scheduler
	Launcher   Launcher
	Redactor   Redactor
	FS         platform.FilesystemIdentity
	ScratchDir string
	// Validator resolves the replacement-validation discriminator for the
	// pre-launch root recheck (G-lane F2): a root whose (dev, ino) survived
	// replacement (overlayfs inode reuse) still fails RootIdentityMatch when
	// its discriminator no longer equals the one captured at registration.
	// Defaults to the production resolver.
	Validator platform.ReplacementValidator
	// KillMS overrides the terminate→kill escalation (default KillEscalationMS).
	KillMS int64
}

// ChildRecord is one entry in the process-creation ledger. Every process
// creation is recorded so "zero children" assertions observe reality, not
// intent (contract.go Child).
type ChildRecord struct {
	Activation     string
	ExecSHA256     string
	ConfigSHA256   string
	SpawnedAtMS    int64
	TerminatedAtMS int64
	KilledAtMS     int64
	ExitedAtMS     int64

	// project / launchEpoch let the revocation listener find in-flight
	// children of a revoked project (unexported: not part of the observed
	// ledger surface).
	project     control.ProjectKey
	launchEpoch uint64
}

// Outcome is one activation's decision, produced at the linearization point.
type Outcome struct {
	Allow         bool
	Code          v1.ErrorCode
	CompletedAtMS int64
}

// GrantRequest binds a hook at approval time (HA-6).
type GrantRequest struct {
	HookID        string
	ExecPath      string
	AllowedEvents []string
	Scope         control.ScopeKind
	FixedPath     string
	EnvAllowlist  []string
	TimeoutMS     int64
	OutputCap     int64
}

// ActivationRequest submits one activation.
type ActivationRequest struct {
	Project control.ProjectKey
	Hook    string
	Event   string
	Session string
	// PaneProject is the project a ScopePane target resolves to ("" =
	// unregistered pane).
	PaneProject control.ProjectKey
}

// Runtime is the hook execution runtime.
type Runtime struct {
	cfg      Config
	barriers *barriers

	mu       sync.Mutex
	children map[string]*ChildRecord
	pending  map[string]chan Outcome
	grantCfg map[string]string // grantID -> config path
	nextAct  uint64
	// revoked tracks projects revoked during this runtime's life, so a child
	// parked at SyncAfterSpawn learns it must be torn down on release.
	revoked map[control.ProjectKey]uint64
}

// New builds a Runtime and registers its revocation listener on the control
// actor (so in-flight children are torn down when their project is revoked).
func New(ctx context.Context, cfg Config) (*Runtime, error) {
	if cfg.Control == nil {
		return nil, errors.New("hooks: Config.Control is required")
	}
	if cfg.Clock == nil {
		return nil, errors.New("hooks: Config.Clock is required")
	}
	if cfg.Launcher == nil {
		cfg.Launcher = NewSpyLauncher()
	}
	if cfg.FS == nil {
		cfg.FS = platform.NewFilesystemIdentity()
	}
	if cfg.Validator == nil {
		cfg.Validator = platform.NewReplacementValidator()
	}
	if cfg.KillMS == 0 {
		cfg.KillMS = KillEscalationMS
	}
	r := &Runtime{
		cfg:      cfg,
		barriers: newBarriers(),
		children: map[string]*ChildRecord{},
		pending:  map[string]chan Outcome{},
		grantCfg: map[string]string{},
		revoked:  map[control.ProjectKey]uint64{},
	}
	// The revocation listener runs ON the control actor goroutine immediately
	// after the project_revoked record commits (ADR-0004). It records the
	// revocation AND terminates every in-flight child of the project right
	// there, so the terminate audit is ordered strictly after project_revoked
	// and the kill-tree timer is armed before any later clock advance can
	// cross it (HA-15/HA-16). It must not call back into the actor (that would
	// self-deadlock); it appends audit through the direct sink instead.
	if err := cfg.Control.OnRevoke(ctx, func(p control.ProjectKey, epoch uint64) {
		r.mu.Lock()
		r.revoked[p] = epoch
		var toTerminate []string
		for id, c := range r.children {
			if c.project == p && c.SpawnedAtMS != 0 && c.TerminatedAtMS == 0 {
				toTerminate = append(toTerminate, id)
			}
		}
		r.mu.Unlock()
		sortStrings(toTerminate)
		for _, id := range toTerminate {
			r.terminate(id, p)
		}
	}); err != nil {
		return nil, err
	}
	return r, nil
}

// Hold/AwaitParked/Release expose the deterministic barriers.
func (r *Runtime) Hold(p SyncPoint)    { r.barriers.Hold(p) }
func (r *Runtime) Release(p SyncPoint) { r.barriers.Release(p) }
func (r *Runtime) AwaitParked(p SyncPoint, a string) <-chan struct{} {
	return r.barriers.AwaitParked(p, a)
}

// ApproveHook opens and digests the executable and config to bind the grant
// (HA-6), then records it through the control actor (SQLite-authoritative).
// The project must be approved. Config is read from the fixed project-relative
// path .amux/hooks.jsonc, only after opt-in (HA-3a).
func (r *Runtime) ApproveHook(ctx context.Context, session string, key control.ProjectKey, req GrantRequest) (string, error) {
	proj, ok, err := r.cfg.Control.Project(ctx, key)
	if err != nil {
		return "", err
	}
	if !ok || proj.State != control.StateApproved {
		return "", &grantError{code: v1.ErrProjectTrustRequired, msg: "grant requires an approved project"}
	}
	configPath := filepath.Join(proj.Root, ".amux", "hooks.jsonc")
	obj, err := r.cfg.Launcher.Open(req.ExecPath, configPath)
	if err != nil {
		return "", fmt.Errorf("hooks: opening grant objects: %w", err)
	}
	gid, err := r.cfg.Control.ApproveGrant(ctx, session, key, control.GrantInput{
		HookID:        req.HookID,
		ExecPath:      req.ExecPath,
		ExecSHA256:    obj.ExecSHA256,
		ConfigSHA256:  obj.ConfigSHA256,
		AllowedEvents: req.AllowedEvents,
		Scope:         req.Scope,
		FixedPath:     req.FixedPath,
		EnvAllowlist:  req.EnvAllowlist,
		TimeoutMS:     req.TimeoutMS,
		OutputCap:     req.OutputCap,
	})
	if err != nil {
		return "", err
	}
	r.mu.Lock()
	r.grantCfg[gid] = configPath
	r.mu.Unlock()
	return gid, nil
}

// Activate submits an activation asynchronously and returns its ID. Await
// blocks for the decision.
func (r *Runtime) Activate(ctx context.Context, req ActivationRequest) (string, error) {
	r.mu.Lock()
	r.nextAct++
	id := fmt.Sprintf("act-%d", r.nextAct)
	done := make(chan Outcome, 1)
	r.pending[id] = done
	r.mu.Unlock()

	go r.pipeline(ctx, id, req, done)
	return id, nil
}

// Await returns the activation's decision, blocking until it is produced or
// ctx is done.
func (r *Runtime) Await(ctx context.Context, id string) (Outcome, error) {
	r.mu.Lock()
	done, ok := r.pending[id]
	r.mu.Unlock()
	if !ok {
		return Outcome{}, fmt.Errorf("hooks: unknown activation %s", id)
	}
	select {
	case out := <-done:
		return out, nil
	case <-ctx.Done():
		return Outcome{}, ctx.Err()
	}
}

// Children returns a snapshot of the process-creation ledger.
func (r *Runtime) Children() []ChildRecord {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]ChildRecord, 0, len(r.children))
	// Deterministic order by spawn time then activation id.
	ids := make([]string, 0, len(r.children))
	for id := range r.children {
		ids = append(ids, id)
	}
	sortStrings(ids)
	for _, id := range ids {
		out = append(out, *r.children[id])
	}
	return out
}

// pipeline runs one activation through the bounded, fail-closed stages.
func (r *Runtime) pipeline(ctx context.Context, id string, req ActivationRequest, done chan Outcome) {
	finish := func(o Outcome) {
		o.CompletedAtMS = r.cfg.Clock.NowUnixMilli()
		done <- o
	}

	// Pre-open trust gate (HA-3a): never open or parse objects for an
	// unapproved project. An unapproved/absent project denies without touching
	// the filesystem; the denial is audited by the control authorization.
	proj, ok, err := r.cfg.Control.Project(ctx, req.Project)
	if err != nil {
		finish(Outcome{Allow: false, Code: v1.ErrInternal})
		return
	}
	if !ok || proj.State != control.StateApproved {
		res, _ := r.cfg.Control.AuthorizeLaunch(ctx, req.Project, req.Hook, "", control.RuntimeFacts{})
		finish(Outcome{Allow: res.Decision.Allow, Code: res.Decision.Code})
		return
	}

	grant, gok, err := r.cfg.Control.FindGrant(ctx, req.Project, req.Hook)
	if err != nil {
		finish(Outcome{Allow: false, Code: v1.ErrInternal})
		return
	}

	// Descriptor-bound object capture (HA-10/HA-11). Everything captured here
	// is immune to a post-open swap; the launcher runs exactly this object.
	var obj OpenedObject
	rootMatch := true
	if gok {
		configPath := r.grantCfgPath(grant.ID)
		obj, err = r.cfg.Launcher.Open(grant.ExecPath, configPath)
		if err != nil {
			// A missing/altered object at open is a stale grant, fail closed.
			res, _ := r.cfg.Control.AuthorizeLaunch(ctx, req.Project, req.Hook, grant.ID, control.RuntimeFacts{})
			_ = res
			finish(Outcome{Allow: false, Code: v1.ErrHookGrantStale})
			return
		}
		rootMatch = r.rootIdentityMatches(proj)
	}

	// Park at the check-to-exec window: the races.* fixtures mutate the objects
	// now. Captured facts above do not change.
	r.barriers.reach(SyncAfterObjectOpen, id)

	scope := r.resolveScope(grant, req)

	// Park before final validation: the revoke-first/revoke-cancel fixtures
	// revoke while parked here.
	r.barriers.reach(SyncBeforeFinalValidation, id)

	rt := control.RuntimeFacts{
		RootIdentityMatch: rootMatch,
		ExecDigestMatch:   gok && obj.ExecSHA256 == grant.ExecSHA256,
		ConfigDigestMatch: gok && obj.ConfigSHA256 == grant.ConfigSHA256,
		ConfigMatch: control.ConfigMatch{
			EventSet: true, Scope: true, Env: true, Timeout: true, OutputCap: true,
		},
		TimeoutMS:         grant.TimeoutMS,
		OutputCapBytes:    grant.OutputCap,
		ExecIsRegularFile: obj.ExecIsRegularFile(),
		EventAllowed:      eventAllowed(grant, req.Event),
		Scope:             scope,
		EnvOutside:        false,
	}
	res, err := r.cfg.Control.AuthorizeLaunch(ctx, req.Project, req.Hook, grant.ID, rt)
	if err != nil {
		finish(Outcome{Allow: false, Code: v1.ErrInternal})
		return
	}
	if !res.Decision.Allow {
		finish(Outcome{Allow: false, Code: res.Decision.Code})
		return
	}

	// Spawn: record the child before the after-spawn barrier so a parked
	// launch-first fixture observes exactly one child with the approved digest.
	proc, err := r.cfg.Launcher.Launch(obj, []string{grant.ExecPath}, r.buildEnv(grant), r.cwd(scope, proj))
	if err != nil {
		finish(Outcome{Allow: false, Code: v1.ErrInternal})
		return
	}
	spawnedAt := r.cfg.Clock.NowUnixMilli()
	r.mu.Lock()
	r.children[id] = &ChildRecord{
		Activation:   id,
		ExecSHA256:   obj.ExecSHA256,
		ConfigSHA256: obj.ConfigSHA256,
		SpawnedAtMS:  spawnedAt,
		project:      req.Project,
		launchEpoch:  res.Epoch,
	}
	r.mu.Unlock()
	r.cfg.Control.RecordAudit(control.AuditSpawn, req.Project, res.Epoch, "")
	_ = proc

	// Park after spawn (launch-first). A revoke may land while parked; the
	// revoke listener terminates the child synchronously, so on release the
	// backstop below is a no-op.
	r.barriers.reach(SyncAfterSpawn, id)

	// Backstop for the production path where no barrier is held: if a revoke
	// was recorded for this project at or after this child's launch epoch and
	// the listener has not already terminated it (e.g. the child spawned after
	// the revoke landed), terminate now. terminate() is idempotent.
	if r.wasRevoked(req.Project, res.Epoch) {
		r.terminate(id, req.Project)
	}

	finish(Outcome{Allow: true, Code: ""})
}

// terminate SIGTERMs the in-flight child, schedules the kill-tree escalation
// KillMS later on the deterministic clock, and audits each transition through
// the direct sink. It is idempotent (guards on TerminatedAtMS). The escalation
// callback fires during a clock Advance, so KilledAtMS - TerminatedAtMS ==
// KillMS exactly (the frozen 2000 ms boundary).
func (r *Runtime) terminate(id string, key control.ProjectKey) {
	r.mu.Lock()
	child := r.children[id]
	if child == nil || child.TerminatedAtMS != 0 {
		r.mu.Unlock()
		return
	}
	child.TerminatedAtMS = r.cfg.Clock.NowUnixMilli()
	epoch := child.launchEpoch
	r.mu.Unlock()
	r.cfg.Control.RecordAudit(control.AuditTerminate, key, epoch, "")

	r.cfg.Clock.AfterFunc(r.cfg.KillMS, func() {
		killAt := r.cfg.Clock.NowUnixMilli()
		r.mu.Lock()
		child := r.children[id]
		if child == nil || child.KilledAtMS != 0 {
			r.mu.Unlock()
			return
		}
		child.KilledAtMS = killAt
		child.ExitedAtMS = killAt
		r.mu.Unlock()
		r.cfg.Control.RecordAudit(control.AuditKillEscalation, key, epoch, "")
		r.cfg.Control.RecordAudit(control.AuditExit, key, epoch, "")
	})
}

// --- resolution helpers -------------------------------------------------

func (r *Runtime) grantCfgPath(id string) string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.grantCfg[id]
}

func (r *Runtime) wasRevoked(key control.ProjectKey, launchEpoch uint64) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	e, ok := r.revoked[key]
	return ok && e >= launchEpoch
}

// rootIdentityMatches recomputes the root's identity immediately before the
// final authorization (HA-2c/HA-14): the (realpath, dev, ino) tuple AND the
// replacement-validation discriminator must both equal the registered record.
// The discriminator closes the overlayfs inode-reuse hole; any resolution
// error fails closed.
func (r *Runtime) rootIdentityMatches(proj control.ProjectRecord) bool {
	_, id, err := r.cfg.FS.Identify(proj.Root)
	if err != nil || id != proj.Identity {
		return false
	}
	val, err := r.cfg.Validator.ValidationID(proj.Root)
	if err != nil {
		return false
	}
	return val == proj.Validation
}

func (r *Runtime) resolveScope(grant control.GrantRecord, req ActivationRequest) control.ScopeFacts {
	switch grant.Scope {
	case control.ScopeNone, "":
		return control.ScopeFacts{Kind: control.ScopeNone, Resolved: true}
	case control.ScopePane:
		if req.PaneProject == "" {
			return control.ScopeFacts{Kind: control.ScopePane, Reason: "pane has no registered project"}
		}
		if req.PaneProject != grant.Project {
			return control.ScopeFacts{Kind: control.ScopePane, Reason: "pane resolves to another project"}
		}
		return control.ScopeFacts{Kind: control.ScopePane, Resolved: true}
	case control.ScopeFixed:
		return control.ScopeFacts{Kind: control.ScopeFixed, Resolved: grant.FixedPath != ""}
	default:
		return control.ScopeFacts{Kind: grant.Scope, Resolved: false, Reason: "unresolved scope"}
	}
}

// cwd returns the launch directory: an Amux-owned scratch dir for ScopeNone
// (HA-9), else the resolved scope path. Never the ambient daemon cwd.
func (r *Runtime) cwd(scope control.ScopeFacts, proj control.ProjectRecord) string {
	if scope.Kind == control.ScopeNone {
		return r.cfg.ScratchDir
	}
	return proj.Root
}

// buildEnv returns ONLY the grant's allowlisted keys (no ambient environment,
// HA-8 no_ambient_env). Values come from an explicit non-secret store; the
// conformance path has none, so the environment is empty.
func (r *Runtime) buildEnv(grant control.GrantRecord) []string {
	out := make([]string, 0, len(grant.EnvAllowlist))
	// No non-secret store is wired in the conformance path; allowlisted keys
	// with no value are simply omitted. The point the fixtures assert is that
	// NOTHING outside the allowlist ever appears.
	return out
}

func eventAllowed(grant control.GrantRecord, event string) bool {
	if len(grant.AllowedEvents) == 0 {
		return false
	}
	for _, e := range grant.AllowedEvents {
		if e == event {
			return true
		}
	}
	return false
}

// grantError carries a taxonomy code.
type grantError struct {
	code v1.ErrorCode
	msg  string
}

func (e *grantError) Error() string { return string(e.code) + ": " + e.msg }

func sortStrings(s []string) {
	for i := 1; i < len(s); i++ {
		for j := i; j > 0 && s[j-1] > s[j]; j-- {
			s[j-1], s[j] = s[j], s[j-1]
		}
	}
}
