//go:build integration

package hooks_test

// TestTrustMatrixReplayIntegrated is the readiness-manifest
// `trust-matrix-replay` binding (G-lane F5): every row of the frozen golden
// testdata/security/trust-matrix.json replayed against the REAL integrated
// trust engine — the production control actor (its goroutine, epochs, grants,
// and audit), the SQLite trust store (daemon.NewTrustStore over a real
// database file), and the hook runtime's launch pipeline with real project
// directories and real hook/config files. It replaces the old vacuous binding
// (`-run 'TrustMatrixReplay'` matched zero tests) and complements — never
// replaces — the unit-level TestDecideReplaysTrustMatrix in internal/control.
//
// Two driver classes, recorded per row:
//
//   - pipeline: the row's condition is materialized PHYSICALLY (real dirs,
//     real byte rewrites, real revocations) and driven end-to-end through
//     Runtime.Activate → the final pre-spawn authorization → the spy
//     launcher.
//   - authorize: the row's condition cannot be materialized without
//     privileged mounts (remount), a second filesystem, or product surface
//     the runtime does not yet expose (workspace-primary resolution, fixed-
//     scope identity recheck, env-value requests, queue admission, forced
//     invariant corruption). These rows drive the REAL linearization point —
//     control.Actor.AuthorizeLaunch over the same SQLite-backed state — with
//     the single deviated I/O fact injected exactly as the production
//     resolver would report it. The decision, epoch/grant resolution, and
//     deny audit are all real engine behavior.
//
// The test fails on zero rows, on any row without a driver, and on any driver
// without a row — the golden and this replay can never drift silently.

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	v1 "github.com/amux-run/amux/api/v1"

	"github.com/amux-run/amux/internal/control"
	"github.com/amux-run/amux/internal/daemon"
	"github.com/amux-run/amux/internal/hooks"
	"github.com/amux-run/amux/internal/securitytest"
	"github.com/amux-run/amux/internal/store"
)

// matrixSUT is one integrated engine instance: real actor, real SQLite trust
// store, real runtime, real project root on disk.
type matrixSUT struct {
	t    *testing.T
	ctx  context.Context
	ctrl *control.Actor
	rt   *hooks.Runtime
	st   *store.Store
	root string
	key  control.ProjectKey
}

func newMatrixSUT(t *testing.T) *matrixSUT {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	clock := hooks.NewSchedClock(1_000_000)
	ctrl := control.New(control.Deps{Store: daemon.NewTrustStore(st), Clock: clock})
	ctrl.Start()
	t.Cleanup(ctrl.Stop)
	ctx := context.Background()
	rt, err := hooks.New(ctx, hooks.Config{
		Control:    ctrl,
		Clock:      clock,
		Launcher:   hooks.NewSpyLauncher(),
		ScratchDir: filepath.Join(t.TempDir(), "scratch"),
	})
	if err != nil {
		t.Fatal(err)
	}
	s := &matrixSUT{t: t, ctx: ctx, ctrl: ctrl, rt: rt, st: st}
	s.root = filepath.Join(t.TempDir(), "project")
	s.writeRoot(baseConfig)
	return s
}

const baseConfig = `{
  // integrated trust-matrix replay config (bound by digest at approval)
  "hooks": [{"id": "h1", "events": ["pane_exit"], "scope": "none",
             "env": [], "timeout_ms": 2000, "output_cap": 1048576}]
}
`

func (s *matrixSUT) writeRoot(config string) {
	s.t.Helper()
	if err := os.MkdirAll(filepath.Join(s.root, ".amux"), 0o755); err != nil {
		s.t.Fatal(err)
	}
	if err := os.WriteFile(s.execPath(), []byte("#!/bin/sh\necho hook\n"), 0o755); err != nil {
		s.t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(s.root, ".amux", "hooks.jsonc"), []byte(config), 0o644); err != nil {
		s.t.Fatal(err)
	}
}

func (s *matrixSUT) execPath() string { return filepath.Join(s.root, "hook.sh") }

func (s *matrixSUT) register() control.ProjectKey {
	s.t.Helper()
	key, err := s.ctrl.RegisterProject(s.ctx, s.root)
	if err != nil {
		s.t.Fatal(err)
	}
	s.key = key
	return key
}

func (s *matrixSUT) approve() uint64 {
	s.t.Helper()
	e, err := s.ctrl.ApproveProject(s.ctx, "s1", s.key)
	if err != nil {
		s.t.Fatal(err)
	}
	return e
}

func (s *matrixSUT) grant(req hooks.GrantRequest) string {
	s.t.Helper()
	if req.HookID == "" {
		req = hooks.GrantRequest{
			HookID: "h1", ExecPath: s.execPath(), AllowedEvents: []string{"pane_exit"},
			Scope: control.ScopeNone, TimeoutMS: 2000, OutputCap: 1 << 20,
		}
	}
	gid, err := s.rt.ApproveHook(s.ctx, "s1", s.key, req)
	if err != nil {
		s.t.Fatal(err)
	}
	return gid
}

// baseline registers, approves, and grants h1 against real files.
func (s *matrixSUT) baseline() string {
	s.t.Helper()
	s.register()
	s.approve()
	return s.grant(hooks.GrantRequest{})
}

func (s *matrixSUT) activate(key control.ProjectKey, event string, pane control.ProjectKey) hooks.Outcome {
	s.t.Helper()
	id, err := s.rt.Activate(s.ctx, hooks.ActivationRequest{
		Project: key, Hook: "h1", Event: event, Session: "s1", PaneProject: pane,
	})
	if err != nil {
		s.t.Fatal(err)
	}
	out, err := awaitOutcome(s.ctx, s.rt, id)
	if err != nil {
		s.t.Fatal(err)
	}
	return out
}

// authorize drives the real linearization point over the SQLite-backed actor
// state, deviating exactly one production-resolved I/O fact.
func (s *matrixSUT) authorize(gid string, mut func(*control.RuntimeFacts)) control.AuthorizeResult {
	s.t.Helper()
	f := control.RuntimeFacts{
		RootIdentityMatch: true, ExecDigestMatch: true, ConfigDigestMatch: true,
		ConfigMatch: control.ConfigMatch{EventSet: true, Scope: true, Env: true, Timeout: true, OutputCap: true},
		TimeoutMS:   2000, OutputCapBytes: 1 << 20,
		ExecIsRegularFile: true, EventAllowed: true,
		Scope: control.ScopeFacts{Kind: control.ScopeNone, Resolved: true},
	}
	mut(&f)
	res, err := s.ctrl.AuthorizeLaunch(s.ctx, s.key, "h1", gid, f)
	if err != nil {
		s.t.Fatal(err)
	}
	return res
}

func (s *matrixSUT) requireDenyAudited(code v1.ErrorCode) {
	s.t.Helper()
	recs, err := s.ctrl.Audit(s.ctx)
	if err != nil {
		s.t.Fatal(err)
	}
	for _, r := range recs {
		if r.Kind == control.AuditActivationDeny && r.Code == code {
			return
		}
	}
	s.t.Fatalf("deny (%s) not audited in the durable trail (AUD-3): %+v", code, recs)
}

// rewriteConfig applies a field-targeted redefinition. The integrated runtime
// enforces post-approval config drift through the digest bound at approval
// (HA-7/HA-11): any redefinition changes the bytes behind the bound
// descriptor, so the grant goes stale at the real pre-launch check.
func (s *matrixSUT) rewriteConfig(old, new string) {
	s.t.Helper()
	if !strings.Contains(baseConfig, old) {
		s.t.Fatalf("config rewrite target %q not present", old)
	}
	s.writeRoot(strings.Replace(baseConfig, old, new, 1))
}

func expectOutcome(t *testing.T, rowID string, want securitytest.Expected, allow bool, code v1.ErrorCode) {
	t.Helper()
	switch want.Decision {
	case securitytest.DecisionAllow:
		if !allow {
			t.Fatalf("%s: deny(%s), want allow", rowID, code)
		}
	case securitytest.DecisionDeny:
		if allow {
			t.Fatalf("%s: allow, want deny (fail closed)", rowID)
		}
		if code != want.Code {
			t.Fatalf("%s: deny code %s, want %s", rowID, code, want.Code)
		}
	default:
		t.Fatalf("%s: golden row with unknown decision %q", rowID, want.Decision)
	}
}

func expectEnforcement(t *testing.T, rowID string, want securitytest.Expected, res control.AuthorizeResult) {
	t.Helper()
	if want.Enforcement == "" {
		return
	}
	for _, e := range res.Decision.Enforcements {
		if e == want.Enforcement {
			return
		}
	}
	t.Fatalf("%s: allow lacks required enforcement %q (got %v)", rowID, want.Enforcement, res.Decision.Enforcements)
}

func TestTrustMatrixReplayIntegrated(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "..", "testdata", "security", "trust-matrix.json"))
	if err != nil {
		t.Fatalf("reading trust-matrix golden: %v", err)
	}
	var m securitytest.TrustMatrix
	if err := v1.DecodeStrict(raw, &m); err != nil {
		t.Fatalf("decoding trust-matrix golden: %v", err)
	}
	if len(m.Rows) == 0 {
		t.Fatal("golden matrix has no rows")
	}

	// pipelineRows drive Runtime.Activate end-to-end with physically
	// materialized conditions; the returned Outcome is asserted.
	pipelineRows := map[string]func(t *testing.T, s *matrixSUT) hooks.Outcome{
		"project.trusted-baseline": func(t *testing.T, s *matrixSUT) hooks.Outcome {
			s.baseline()
			return s.activate(s.key, "pane_exit", "")
		},
		"project.unregistered": func(t *testing.T, s *matrixSUT) hooks.Outcome {
			s.baseline() // a trusted project exists; the ACTIVATION targets a never-registered key
			return s.activate(control.ProjectKey("never-registered-key"), "pane_exit", "")
		},
		"project.registered-not-approved": func(t *testing.T, s *matrixSUT) hooks.Outcome {
			s.register()
			return s.activate(s.key, "pane_exit", "")
		},
		"project.denied": func(t *testing.T, s *matrixSUT) hooks.Outcome {
			s.register()
			if err := s.ctrl.DenyProject(s.ctx, "s1", s.key); err != nil {
				t.Fatal(err)
			}
			return s.activate(s.key, "pane_exit", "")
		},
		"project.revoked": func(t *testing.T, s *matrixSUT) hooks.Outcome {
			s.baseline()
			if _, err := s.ctrl.RevokeProject(s.ctx, "s1", s.key); err != nil {
				t.Fatal(err)
			}
			return s.activate(s.key, "pane_exit", "")
		},
		"project.replaced-root": func(t *testing.T, s *matrixSUT) hooks.Outcome {
			s.baseline()
			// Physical replacement with byte-identical content: on fresh-inode
			// filesystems the tuple mismatch denies; on inode-reusing
			// filesystems (overlayfs) the persisted birth-time discriminator
			// denies. Both are the real pre-launch identity recheck.
			if err := os.RemoveAll(s.root); err != nil {
				t.Fatal(err)
			}
			time.Sleep(50 * time.Millisecond)
			s.writeRoot(baseConfig)
			return s.activate(s.key, "pane_exit", "")
		},
		"grant.absent": func(t *testing.T, s *matrixSUT) hooks.Outcome {
			s.register()
			s.approve()
			return s.activate(s.key, "pane_exit", "")
		},
		"grant.inactive-after-revocation": func(t *testing.T, s *matrixSUT) hooks.Outcome {
			s.baseline()
			if _, err := s.ctrl.RevokeProject(s.ctx, "s1", s.key); err != nil {
				t.Fatal(err)
			}
			s.approve() // fresh reapproval; the prior grant stays inactive (HA-18e)
			return s.activate(s.key, "pane_exit", "")
		},
		"grant.epoch-stale": func(t *testing.T, s *matrixSUT) hooks.Outcome {
			s.baseline()
			s.approve() // epoch bump strands the grant at the earlier epoch
			return s.activate(s.key, "pane_exit", "")
		},
		"grant.exec-digest-changed": func(t *testing.T, s *matrixSUT) hooks.Outcome {
			s.baseline()
			if err := os.WriteFile(s.execPath(), []byte("#!/bin/sh\necho SUBSTITUTED\n"), 0o755); err != nil {
				t.Fatal(err)
			}
			return s.activate(s.key, "pane_exit", "")
		},
		"grant.config-digest-changed": func(t *testing.T, s *matrixSUT) hooks.Outcome {
			s.baseline()
			if err := os.WriteFile(filepath.Join(s.root, ".amux", "hooks.jsonc"), []byte("{}\n"), 0o644); err != nil {
				t.Fatal(err)
			}
			return s.activate(s.key, "pane_exit", "")
		},
		"grant.event-set-changed": func(t *testing.T, s *matrixSUT) hooks.Outcome {
			s.baseline()
			s.rewriteConfig(`"events": ["pane_exit"]`, `"events": ["pane_exit", "pane_focus"]`)
			return s.activate(s.key, "pane_exit", "")
		},
		"grant.cwd-scope-changed": func(t *testing.T, s *matrixSUT) hooks.Outcome {
			s.baseline()
			s.rewriteConfig(`"scope": "none"`, `"scope": "pane"`)
			return s.activate(s.key, "pane_exit", "")
		},
		"grant.env-allowlist-changed": func(t *testing.T, s *matrixSUT) hooks.Outcome {
			s.baseline()
			s.rewriteConfig(`"env": []`, `"env": ["PATH"]`)
			return s.activate(s.key, "pane_exit", "")
		},
		"grant.timeout-changed": func(t *testing.T, s *matrixSUT) hooks.Outcome {
			s.baseline()
			s.rewriteConfig(`"timeout_ms": 2000`, `"timeout_ms": 9000`)
			return s.activate(s.key, "pane_exit", "")
		},
		"grant.output-cap-changed": func(t *testing.T, s *matrixSUT) hooks.Outcome {
			s.baseline()
			s.rewriteConfig(`"output_cap": 1048576`, `"output_cap": 4096`)
			return s.activate(s.key, "pane_exit", "")
		},
		"scope.none-scratch": func(t *testing.T, s *matrixSUT) hooks.Outcome {
			s.baseline()
			out := s.activate(s.key, "pane_exit", "")
			if out.Allow {
				kids := s.rt.Children()
				if len(kids) != 1 || kids[0].SpawnedAtMS == 0 {
					t.Fatalf("scratch-scope allow must spawn exactly one child: %+v", kids)
				}
			}
			return out
		},
		"scope.fixed-valid": func(t *testing.T, s *matrixSUT) hooks.Outcome {
			s.register()
			s.approve()
			fixed := filepath.Join(s.root, "workdir")
			if err := os.MkdirAll(fixed, 0o755); err != nil {
				t.Fatal(err)
			}
			s.grant(hooks.GrantRequest{
				HookID: "h1", ExecPath: s.execPath(), AllowedEvents: []string{"pane_exit"},
				Scope: control.ScopeFixed, FixedPath: fixed, TimeoutMS: 2000, OutputCap: 1 << 20,
			})
			return s.activate(s.key, "pane_exit", "")
		},
		"scope.pane-same-project": func(t *testing.T, s *matrixSUT) hooks.Outcome {
			s.register()
			s.approve()
			s.grant(hooks.GrantRequest{
				HookID: "h1", ExecPath: s.execPath(), AllowedEvents: []string{"pane_exit"},
				Scope: control.ScopePane, TimeoutMS: 2000, OutputCap: 1 << 20,
			})
			return s.activate(s.key, "pane_exit", s.key)
		},
		"scope.pane-cross-project": func(t *testing.T, s *matrixSUT) hooks.Outcome {
			s.register()
			s.approve()
			s.grant(hooks.GrantRequest{
				HookID: "h1", ExecPath: s.execPath(), AllowedEvents: []string{"pane_exit"},
				Scope: control.ScopePane, TimeoutMS: 2000, OutputCap: 1 << 20,
			})
			other := filepath.Join(t.TempDir(), "other-project")
			if err := os.MkdirAll(other, 0o755); err != nil {
				t.Fatal(err)
			}
			otherKey, err := s.ctrl.RegisterProject(s.ctx, other)
			if err != nil {
				t.Fatal(err)
			}
			return s.activate(s.key, "pane_exit", otherKey)
		},
		"scope.pane-unregistered": func(t *testing.T, s *matrixSUT) hooks.Outcome {
			s.register()
			s.approve()
			s.grant(hooks.GrantRequest{
				HookID: "h1", ExecPath: s.execPath(), AllowedEvents: []string{"pane_exit"},
				Scope: control.ScopePane, TimeoutMS: 2000, OutputCap: 1 << 20,
			})
			return s.activate(s.key, "pane_exit", "")
		},
		"scope.event-not-granted": func(t *testing.T, s *matrixSUT) hooks.Outcome {
			s.baseline()
			return s.activate(s.key, "pane_focus", "")
		},
	}

	// grantRows drive the real grant-approval bound checks (HA-8: rejected,
	// never clamped) — the deny surfaces at ApproveGrant/ApproveHook.
	grantRows := map[string]func(t *testing.T, s *matrixSUT) v1.ErrorCode{
		"bounds.timeout-exceeds-max": func(t *testing.T, s *matrixSUT) v1.ErrorCode {
			s.register()
			s.approve()
			_, err := s.rt.ApproveHook(s.ctx, "s1", s.key, hooks.GrantRequest{
				HookID: "h1", ExecPath: s.execPath(), AllowedEvents: []string{"pane_exit"},
				Scope: control.ScopeNone, TimeoutMS: control.MaxTimeoutMS + 1, OutputCap: 1 << 20,
			})
			if err == nil {
				t.Fatal("oversized timeout was accepted (must reject, never clamp)")
			}
			return control.CodeOf(err)
		},
		"bounds.output-cap-exceeds-max": func(t *testing.T, s *matrixSUT) v1.ErrorCode {
			s.register()
			s.approve()
			_, err := s.rt.ApproveHook(s.ctx, "s1", s.key, hooks.GrantRequest{
				HookID: "h1", ExecPath: s.execPath(), AllowedEvents: []string{"pane_exit"},
				Scope: control.ScopeNone, TimeoutMS: 2000, OutputCap: control.MaxOutputCapBytes + 1,
			})
			if err == nil {
				t.Fatal("oversized output cap was accepted (must reject, never clamp)")
			}
			return control.CodeOf(err)
		},
	}

	// authorizeRows drive the real AuthorizeLaunch linearization point over
	// SQLite-backed actor state with one injected I/O fact. The reason each
	// row cannot be physically materialized is recorded inline.
	type authorizeRow struct {
		reason string
		mut    func(*control.RuntimeFacts)
	}
	authorizeRows := map[string]authorizeRow{
		"project.remounted-root": {
			reason: "remounting requires privileged mounts unavailable to the harness",
			mut:    func(f *control.RuntimeFacts) { f.RootIdentityMatch = false },
		},
		"scope.fixed-identity-changed": {
			reason: "fixed-scope pre-launch identity recheck is resolved by the (T4) scope resolver surface",
			mut: func(f *control.RuntimeFacts) {
				f.Scope = control.ScopeFacts{Kind: control.ScopeFixed, Reason: "granted directory identity changed"}
			},
		},
		"scope.wsprimary-valid": {
			reason: "workspace-primary resolution requires the workspace surface",
			mut: func(f *control.RuntimeFacts) {
				f.Scope = control.ScopeFacts{Kind: control.ScopeWorkspacePrimary, Resolved: true}
			},
		},
		"scope.wsprimary-absent": {
			reason: "workspace-primary resolution requires the workspace surface",
			mut: func(f *control.RuntimeFacts) {
				f.Scope = control.ScopeFacts{Kind: control.ScopeWorkspacePrimary, Reason: "no primary root configured"}
			},
		},
		"scope.wsprimary-ambiguous": {
			reason: "workspace-primary resolution requires the workspace surface",
			mut: func(f *control.RuntimeFacts) {
				f.Scope = control.ScopeFacts{Kind: control.ScopeWorkspacePrimary, Reason: "more than one candidate primary root"}
			},
		},
		"scope.wsprimary-replaced": {
			reason: "workspace-primary resolution requires the workspace surface",
			mut: func(f *control.RuntimeFacts) {
				f.Scope = control.ScopeFacts{Kind: control.ScopeWorkspacePrimary, Reason: "primary root identity changed"}
			},
		},
		"scope.wsprimary-foreign-project": {
			reason: "workspace-primary resolution requires the workspace surface",
			mut: func(f *control.RuntimeFacts) {
				f.Scope = control.ScopeFacts{Kind: control.ScopeWorkspacePrimary, Reason: "primary root belongs to another project"}
			},
		},
		"bounds.timeout-default": {
			reason: "enforcement labels are only observable on the AuthorizeResult",
			mut:    func(f *control.RuntimeFacts) { f.TimeoutMS = 0 },
		},
		"bounds.timeout-max": {
			reason: "enforcement labels are only observable on the AuthorizeResult",
			mut:    func(f *control.RuntimeFacts) { f.TimeoutMS = control.MaxTimeoutMS },
		},
		"bounds.output-within-cap": {
			reason: "enforcement labels are only observable on the AuthorizeResult",
			mut:    func(f *control.RuntimeFacts) { f.OutputCapBytes = 1024 },
		},
		"bounds.output-cap-enforced": {
			reason: "cap truncation is a runtime obligation attached to the allow decision",
			mut:    func(f *control.RuntimeFacts) {},
		},
		"bounds.exec-not-regular-file": {
			reason: "a non-regular file cannot carry the grant's bound digest simultaneously",
			mut:    func(f *control.RuntimeFacts) { f.ExecIsRegularFile = false },
		},
		"env.empty-allowlist": {
			reason: "enforcement labels are only observable on the AuthorizeResult",
			mut:    func(f *control.RuntimeFacts) {},
		},
		"env.allowlisted-keys-only": {
			reason: "env values come from the (T4) non-secret store surface",
			mut:    func(f *control.RuntimeFacts) {},
		},
		"env.non-allowlisted-requested": {
			reason: "env-key requests come from the (T4) activation input surface",
			mut:    func(f *control.RuntimeFacts) { f.EnvOutside = true },
		},
		"load.queue-exhausted": {
			reason: "queue admission is resolved by the (T4) bounded-queue surface",
			mut:    func(f *control.RuntimeFacts) { f.QueueExhausted = true },
		},
		"internal.invariant-violation": {
			reason: "an internal invariant cannot be corrupted from outside the engine",
			mut:    func(f *control.RuntimeFacts) { f.Invariant = true },
		},
	}

	covered := map[string]bool{}
	for _, row := range m.Rows {
		row := row
		t.Run(row.ID, func(t *testing.T) {
			s := newMatrixSUT(t)
			switch {
			case pipelineRows[row.ID] != nil:
				out := pipelineRows[row.ID](t, s)
				expectOutcome(t, row.ID, row.Expect, out.Allow, out.Code)
			case grantRows[row.ID] != nil:
				code := grantRows[row.ID](t, s)
				expectOutcome(t, row.ID, row.Expect, false, code)
			default:
				ar, ok := authorizeRows[row.ID]
				if !ok {
					t.Fatalf("no integrated driver for matrix row %q — extend the replay", row.ID)
				}
				gid := s.baseline()
				res := s.authorize(gid, ar.mut)
				expectOutcome(t, row.ID, row.Expect, res.Decision.Allow, res.Decision.Code)
				expectEnforcement(t, row.ID, row.Expect, res)
				if !res.Decision.Allow {
					s.requireDenyAudited(row.Expect.Code)
				}
			}
			covered[row.ID] = true
		})
	}

	// Driver tables may not reference rows that no longer exist in the golden.
	for id := range pipelineRows {
		if !covered[id] {
			t.Errorf("pipeline driver %q has no matching golden row", id)
		}
	}
	for id := range grantRows {
		if !covered[id] {
			t.Errorf("grant driver %q has no matching golden row", id)
		}
	}
	for id := range authorizeRows {
		if !covered[id] {
			t.Errorf("authorize driver %q has no matching golden row", id)
		}
	}
}
