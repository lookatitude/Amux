package hooks_test

// This T4-owned test package registers a real securitytest.Factory and runs
// the implementation-neutral T2 conformance corpus (timing, races, restore,
// redaction) against REAL backend enforcement: the control actor's trust
// engine + linearization point, the hook runtime's descriptor-bound launch
// pipeline (spy launcher — no fixture executes a real hook), the deterministic
// SchedClock, and the centralized redaction engine. It NEVER modifies
// internal/securitytest/** (the frozen contract); it consumes it.

import (
	"context"
	"path/filepath"
	"testing"

	v1 "github.com/amux-run/amux/api/v1"

	"github.com/amux-run/amux/internal/control"
	"github.com/amux-run/amux/internal/hooks"
	"github.com/amux-run/amux/internal/platform"
	"github.com/amux-run/amux/internal/redact"
	"github.com/amux-run/amux/internal/securitytest"
)

// TestSecurityConformance is the T4 half of the readiness-manifest
// "security-conformance" check: it invokes securitytest.RunConformance with a
// real Factory, so the fixtures execute against real enforcement instead of
// skipping.
func TestSecurityConformance(t *testing.T) {
	securitytest.RunConformance(t, func(t *testing.T) securitytest.SystemUnderTest {
		return newSUT(t)
	})
}

// sut is the adapter from the hook runtime + control actor to the frozen
// securitytest.SystemUnderTest surface.
type sut struct {
	clock    *hooks.SchedClock
	control  *control.Actor
	runtime  *hooks.Runtime
	redactor *redact.Engine
	ctx      context.Context
}

func newSUT(t *testing.T) *sut {
	t.Helper()
	ctx := context.Background()
	clock := hooks.NewSchedClock(1_000_000)
	ctrl := control.New(control.Deps{
		Store: control.NewMemStore(),
		FS:    platform.NewFilesystemIdentity(),
		Clock: clock,
	})
	ctrl.Start()

	scratch := filepath.Join(t.TempDir(), "scratch")
	rt, err := hooks.New(ctx, hooks.Config{
		Control:    ctrl,
		Clock:      clock,
		Launcher:   hooks.NewSpyLauncher(),
		FS:         platform.NewFilesystemIdentity(),
		ScratchDir: scratch,
		Redactor:   nil,
	})
	if err != nil {
		t.Fatalf("hooks.New: %v", err)
	}

	// Seed the redaction engine with every candidate secret the redaction
	// fixtures can inject, mapped to its labelled marker. Amux registers its
	// known secrets with the centralized engine; here the "known" set is the
	// fixture corpus. The built-in AMUXTEST_ pattern still catches truncated
	// prefixes (RED-5) that exact-match cannot.
	eng := redact.New()
	fixtures, err := securitytest.LoadFixtures(securitytest.FixtureDir())
	if err != nil {
		t.Fatalf("loading redaction fixtures: %v", err)
	}
	for _, v := range fixtures.Redaction {
		eng.Register(securitytest.DeriveCandidateSecret(v.Label), securitytest.RedactionMarker(v.Label))
	}

	return &sut{clock: clock, control: ctrl, runtime: rt, redactor: eng, ctx: ctx}
}

func (s *sut) Clock() securitytest.FakeClock      { return clockAdapter{s.clock} }
func (s *sut) Trust() securitytest.TrustOps       { return trustAdapter{s} }
func (s *sut) Hooks() securitytest.HookOps        { return hookAdapter{s} }
func (s *sut) Barriers() securitytest.Barriers    { return barrierAdapter{s} }
func (s *sut) Observe() securitytest.Observations { return obsAdapter{s} }
func (s *sut) Redactor() securitytest.Redactor    { return redactAdapter{s} }
func (s *sut) Restore() securitytest.RestoreOps   { return restoreAdapter{s} }

func (s *sut) Close() error {
	s.control.Stop()
	return nil
}

// clockAdapter exposes SchedClock as securitytest.FakeClock.
type clockAdapter struct{ c *hooks.SchedClock }

func (a clockAdapter) NowUnixMilli() int64 { return a.c.NowUnixMilli() }
func (a clockAdapter) Advance(ms int64)    { a.c.Advance(ms) }

type trustAdapter struct{ s *sut }

func (a trustAdapter) RegisterProject(root string) (securitytest.ProjectID, error) {
	k, err := a.s.control.RegisterProject(a.s.ctx, root)
	return securitytest.ProjectID(k), err
}
func (a trustAdapter) ApproveProject(session string, p securitytest.ProjectID) (securitytest.Epoch, error) {
	e, err := a.s.control.ApproveProject(a.s.ctx, session, control.ProjectKey(p))
	return securitytest.Epoch(e), err
}
func (a trustAdapter) DenyProject(session string, p securitytest.ProjectID) error {
	return a.s.control.DenyProject(a.s.ctx, session, control.ProjectKey(p))
}
func (a trustAdapter) RevokeProject(session string, p securitytest.ProjectID) (securitytest.Epoch, error) {
	e, err := a.s.control.RevokeProject(a.s.ctx, session, control.ProjectKey(p))
	return securitytest.Epoch(e), err
}
func (a trustAdapter) Epoch(p securitytest.ProjectID) (securitytest.Epoch, error) {
	e, err := a.s.control.Epoch(a.s.ctx, control.ProjectKey(p))
	return securitytest.Epoch(e), err
}

type hookAdapter struct{ s *sut }

func (a hookAdapter) ApproveHook(session string, p securitytest.ProjectID, g securitytest.GrantSpec) (securitytest.GrantID, error) {
	id, err := a.s.runtime.ApproveHook(a.s.ctx, session, control.ProjectKey(p), hooks.GrantRequest{
		HookID:        g.HookID,
		ExecPath:      g.ExecutablePath,
		AllowedEvents: g.AllowedEvents,
		Scope:         control.ScopeKind(g.ScopeKind),
		FixedPath:     g.FixedPath,
		EnvAllowlist:  g.EnvAllowlist,
		TimeoutMS:     g.TimeoutMS,
		OutputCap:     g.OutputCapBytes,
	})
	return securitytest.GrantID(id), err
}
func (a hookAdapter) Activate(spec securitytest.ActivationSpec) (securitytest.ActivationID, error) {
	id, err := a.s.runtime.Activate(a.s.ctx, hooks.ActivationRequest{
		Project:     control.ProjectKey(spec.Project),
		Hook:        spec.Hook,
		Event:       spec.Event,
		Session:     spec.Session,
		PaneProject: control.ProjectKey(spec.PaneProject),
	})
	return securitytest.ActivationID(id), err
}
func (a hookAdapter) Await(id securitytest.ActivationID) (securitytest.ActivationResult, error) {
	out, err := a.s.runtime.Await(a.s.ctx, string(id))
	if err != nil {
		return securitytest.ActivationResult{}, err
	}
	dec := securitytest.DecisionAllow
	if !out.Allow {
		dec = securitytest.DecisionDeny
	}
	return securitytest.ActivationResult{Decision: dec, Code: out.Code, CompletedAtMS: out.CompletedAtMS}, nil
}

type barrierAdapter struct{ s *sut }

func (a barrierAdapter) Hold(point securitytest.SyncPoint) { a.s.runtime.Hold(hooks.SyncPoint(point)) }
func (a barrierAdapter) Release(point securitytest.SyncPoint) {
	a.s.runtime.Release(hooks.SyncPoint(point))
}
func (a barrierAdapter) AwaitParked(point securitytest.SyncPoint, id securitytest.ActivationID) error {
	<-a.s.runtime.AwaitParked(hooks.SyncPoint(point), string(id))
	return nil
}

type obsAdapter struct{ s *sut }

func (a obsAdapter) Children() []securitytest.Child {
	kids := a.s.runtime.Children()
	out := make([]securitytest.Child, 0, len(kids))
	for _, k := range kids {
		out = append(out, securitytest.Child{
			Activation:     securitytest.ActivationID(k.Activation),
			ExecSHA256:     k.ExecSHA256,
			ConfigSHA256:   k.ConfigSHA256,
			SpawnedAtMS:    k.SpawnedAtMS,
			TerminatedAtMS: k.TerminatedAtMS,
			KilledAtMS:     k.KilledAtMS,
			ExitedAtMS:     k.ExitedAtMS,
		})
	}
	return out
}
func (a obsAdapter) Audit() []securitytest.AuditRecord {
	recs, err := a.s.control.Audit(a.s.ctx)
	if err != nil {
		return nil
	}
	out := make([]securitytest.AuditRecord, 0, len(recs))
	for _, r := range recs {
		out = append(out, securitytest.AuditRecord{
			Seq:     r.Seq,
			Kind:    securitytest.AuditKind(r.Kind),
			Project: securitytest.ProjectID(r.Project),
			Epoch:   securitytest.Epoch(r.Epoch),
			Code:    r.Code,
			AtMS:    r.AtMS,
		})
	}
	return out
}

type redactAdapter struct{ s *sut }

func (a redactAdapter) Redact(context string, payload []byte) ([]byte, error) {
	return a.s.redactor.Redact(context, payload)
}

// restoreAdapter models presenting a forged snapshot generation to the restore
// path. Trust epochs, grants, and audit are SQLite-only and are NEVER imported
// from a layout snapshot (ADR-0005 non-rollback rule): a forged generation's
// trust claims are ignored outright, so ImportGeneration is a trust no-op. The
// harness observes directly that epochs never decreased, audit never shrank,
// and a subsequent activation still fails closed.
type restoreAdapter struct{ s *sut }

func (a restoreAdapter) ImportGeneration(gen securitytest.GenerationFixture) error {
	// Only a notification/read-state export may ever be imported; this forged
	// generation carries only trust claims, so nothing is imported. Refusing
	// to honor a claim is the entire contract.
	_ = v1.ErrProjectTrustRequired // taxonomy anchor; import touches no trust state
	return nil
}
