package control

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	v1 "github.com/amux-run/amux/api/v1"

	"github.com/amux-run/amux/internal/platform"
)

func newTestActor(t *testing.T) (*Actor, TrustStore) {
	t.Helper()
	store := NewMemStore()
	a := New(Deps{Store: store, Clock: platform.NewFakeClock(1_000)})
	a.Start()
	t.Cleanup(a.Stop)
	return a, store
}

func mkRoot(t *testing.T) string {
	t.Helper()
	root := filepath.Join(t.TempDir(), "project")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	return root
}

func allowRuntime() RuntimeFacts {
	return RuntimeFacts{
		RootIdentityMatch: true,
		ExecDigestMatch:   true,
		ConfigDigestMatch: true,
		ConfigMatch:       ConfigMatch{EventSet: true, Scope: true, Env: true, Timeout: true, OutputCap: true},
		ExecIsRegularFile: true,
		EventAllowed:      true,
		Scope:             ScopeFacts{Kind: ScopeNone, Resolved: true},
	}
}

func grantInput() GrantInput {
	return GrantInput{
		HookID:        "h1",
		ExecPath:      "/tmp/hook.sh",
		ExecSHA256:    "aa",
		ConfigSHA256:  "bb",
		AllowedEvents: []string{"pane_exit"},
		Scope:         ScopeNone,
		TimeoutMS:     2000,
		OutputCap:     1 << 20,
	}
}

func TestSessionRegistry(t *testing.T) {
	a, _ := newTestActor(t)
	ctx := context.Background()
	if err := a.RegisterSession(ctx, SessionInfo{ID: "s1", Name: "one"}); err != nil {
		t.Fatal(err)
	}
	if err := a.RegisterSession(ctx, SessionInfo{ID: "s1"}); err == nil {
		t.Fatal("duplicate session registration accepted")
	}
	if err := a.RegisterSession(ctx, SessionInfo{ID: "s2"}); err != nil {
		t.Fatal(err)
	}
	got, err := a.ListSessions(ctx)
	if err != nil || len(got) != 2 {
		t.Fatalf("ListSessions = %v, %v", got, err)
	}
	if err := a.UnregisterSession(ctx, "s1"); err != nil {
		t.Fatal(err)
	}
	got, _ = a.ListSessions(ctx)
	if len(got) != 1 || got[0].ID != "s2" {
		t.Fatalf("after unregister: %v", got)
	}
}

// Registration alone confers nothing; approve/revoke/reapprove bump the epoch
// monotonically and revocation deactivates grants while retaining history
// (HA-3b, HA-4, HA-18e).
func TestTrustLifecycleEpochsAndGrants(t *testing.T) {
	a, _ := newTestActor(t)
	ctx := context.Background()
	root := mkRoot(t)

	key, err := a.RegisterProject(ctx, root)
	if err != nil {
		t.Fatal(err)
	}
	// Idempotent re-registration returns the same key.
	key2, err := a.RegisterProject(ctx, root)
	if err != nil || key2 != key {
		t.Fatalf("re-registration: %s vs %s (%v)", key2, key, err)
	}
	if _, err := a.ApproveGrant(ctx, "s1", key, grantInput()); err == nil {
		t.Fatal("grant approved for an unapproved project")
	}

	e1, err := a.ApproveProject(ctx, "s1", key)
	if err != nil || e1 == 0 {
		t.Fatalf("approve: epoch=%d err=%v", e1, err)
	}
	gid, err := a.ApproveGrant(ctx, "s1", key, grantInput())
	if err != nil {
		t.Fatal(err)
	}
	g, ok, _ := a.Grant(ctx, gid)
	if !ok || !g.Active || g.BoundEpoch != e1 {
		t.Fatalf("grant record: %+v ok=%v", g, ok)
	}

	e2, err := a.RevokeProject(ctx, "s1", key)
	if err != nil || e2 <= e1 {
		t.Fatalf("revoke: epoch %d -> %d err=%v", e1, e2, err)
	}
	g, _, _ = a.Grant(ctx, gid)
	if g.Active {
		t.Fatal("grant still active after revocation")
	}

	e3, err := a.ApproveProject(ctx, "s1", key)
	if err != nil || e3 <= e2 {
		t.Fatalf("reapprove: epoch %d -> %d err=%v", e2, e3, err)
	}
	g, _, _ = a.Grant(ctx, gid)
	if g.Active {
		t.Fatal("reapproval must NOT reactivate a deactivated grant (HA-18e)")
	}

	recs, err := a.Audit(ctx)
	if err != nil {
		t.Fatal(err)
	}
	wantOrder := []AuditKind{AuditProjectApproved, AuditGrantApproved, AuditProjectRevoked, AuditGrantInactive, AuditProjectApproved}
	i := 0
	for _, r := range recs {
		if i < len(wantOrder) && r.Kind == wantOrder[i] {
			i++
		}
	}
	if i != len(wantOrder) {
		t.Fatalf("audit trail %v missing ordered kinds %v", recs, wantOrder)
	}
}

// Replacing the root directory never lets trust transfer to the imposter
// (HA-2c). On filesystems that allocate a fresh inode the durable key itself
// changes; on filesystems that deterministically reuse (dev, ino) — the
// overlayfs container class — the key legitimately collides (its frozen
// definition is unchanged) and the persisted replacement-validation
// discriminator must invalidate the trust instead. Both branches are
// fail-closed; which one fires depends on the filesystem under TMPDIR.
func TestReplacedRootChangesIdentity(t *testing.T) {
	a, _ := newTestActor(t)
	ctx := context.Background()
	root := mkRoot(t)
	key1, err := a.RegisterProject(ctx, root)
	if err != nil {
		t.Fatal(err)
	}
	e1, err := a.ApproveProject(ctx, "s1", key1)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(root); err != nil {
		t.Fatal(err)
	}
	// Keep the recreate out of any coarse birth-time bucket.
	time.Sleep(50 * time.Millisecond)
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	key2, err := a.RegisterProject(ctx, root)
	if err != nil {
		t.Fatal(err)
	}
	if key1 != key2 {
		// Fresh inode: the identity tuple changed. Trust stays bound to the
		// vanished object; the new object starts with none.
		rec, ok, err := a.Project(ctx, key2)
		if err != nil || !ok {
			t.Fatalf("new project record: ok=%v err=%v", ok, err)
		}
		if rec.State == StateApproved {
			t.Fatal("replacement object inherited approved trust")
		}
		return
	}
	// Inode reuse: the discriminator must have detected the replacement and
	// invalidated the trust with a monotonic epoch bump.
	rec, ok, err := a.Project(ctx, key1)
	if err != nil || !ok {
		t.Fatalf("project record after reuse-replacement: ok=%v err=%v", ok, err)
	}
	if rec.State == StateApproved {
		t.Fatal("replaced root reused (dev, ino) and RETAINED approved trust; replacement validation must invalidate it")
	}
	if rec.Epoch <= e1 {
		t.Fatalf("identity invalidation must bump the epoch monotonically: %d -> %d", e1, rec.Epoch)
	}
}

// fakeFS pins (dev, ino) so the durable key stays IDENTICAL across a root
// replacement — the deterministic reproduction of the overlayfs inode-reuse
// class on any host filesystem.
type fakeFS struct{ dev, ino uint64 }

func (f fakeFS) Identify(path string) (string, platform.FSIdentity, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", platform.FSIdentity{}, err
	}
	return abs, platform.FSIdentity{Dev: f.dev, Ino: f.ino}, nil
}

// fakeValidator scripts the replacement-validation discriminator.
type fakeValidator struct {
	mu  sync.Mutex
	id  platform.ValidationID
	err error
}

func (v *fakeValidator) ValidationID(string) (platform.ValidationID, error) {
	v.mu.Lock()
	defer v.mu.Unlock()
	return v.id, v.err
}

func (v *fakeValidator) set(id platform.ValidationID, err error) {
	v.mu.Lock()
	defer v.mu.Unlock()
	v.id, v.err = id, err
}

// The deterministic overlayfs reproduction: the durable key is identical after
// replacement (same realpath, dev, ino) but the discriminator differs, so the
// approved trust must be invalidated — epoch bumped, grants deactivated,
// revocation audited with the project_trust_required code, and any later
// authorization denied (G-lane F2).
func TestReusedInodeReplacementInvalidatesTrust(t *testing.T) {
	store := NewMemStore()
	fv := &fakeValidator{id: platform.ValidationID{Scheme: "test", Value: "birth-1"}}
	a := New(Deps{Store: store, Clock: platform.NewFakeClock(1_000), FS: fakeFS{dev: 7, ino: 42}, Validator: fv})
	a.Start()
	t.Cleanup(a.Stop)
	ctx := context.Background()
	root := mkRoot(t)

	key, err := a.RegisterProject(ctx, root)
	if err != nil {
		t.Fatal(err)
	}
	e1, err := a.ApproveProject(ctx, "s1", key)
	if err != nil {
		t.Fatal(err)
	}
	gid, err := a.ApproveGrant(ctx, "s1", key, grantInput())
	if err != nil {
		t.Fatal(err)
	}
	res, _ := a.AuthorizeLaunch(ctx, key, "h1", gid, allowRuntime())
	if !res.Decision.Allow {
		t.Fatalf("baseline authorize denied: %+v", res.Decision)
	}

	// Replace the root: (dev, ino) reused, birth time changed.
	fv.set(platform.ValidationID{Scheme: "test", Value: "birth-2"}, nil)
	key2, err := a.RegisterProject(ctx, root)
	if err != nil {
		t.Fatal(err)
	}
	if key2 != key {
		t.Fatalf("frozen key definition must be preserved under (dev, ino) reuse: %s vs %s", key2, key)
	}
	rec, ok, _ := a.Project(ctx, key)
	if !ok || rec.State == StateApproved {
		t.Fatalf("replaced root retained trust: %+v ok=%v", rec, ok)
	}
	if rec.Epoch != e1+1 {
		t.Fatalf("invalidation epoch must bump monotonically: %d -> %d", e1, rec.Epoch)
	}
	if g, _, _ := a.Grant(ctx, gid); g.Active {
		t.Fatal("grant survived identity invalidation")
	}
	res, _ = a.AuthorizeLaunch(ctx, key, "h1", gid, allowRuntime())
	if res.Decision.Allow || res.Decision.Code != v1.ErrProjectTrustRequired {
		t.Fatalf("post-replacement authorize = %+v, want deny project_trust_required", res.Decision)
	}

	// The invalidation is audited as a system revocation distinguishable from
	// an operator revoke by its project_trust_required code (AUD-3/AUD-6).
	recs, err := a.Audit(ctx)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, r := range recs {
		if r.Kind == AuditProjectRevoked && r.Code == v1.ErrProjectTrustRequired && r.Epoch == rec.Epoch {
			found = true
		}
	}
	if !found {
		t.Fatalf("identity invalidation not audited: %+v", recs)
	}

	// Re-registration after invalidation is stable: same key, still untrusted.
	key3, err := a.RegisterProject(ctx, root)
	if err != nil || key3 != key {
		t.Fatalf("re-registration after invalidation: %s err=%v", key3, err)
	}
	rec, _, _ = a.Project(ctx, key)
	if rec.State == StateApproved || rec.Epoch != e1+1 {
		t.Fatalf("idempotent re-registration mutated state: %+v", rec)
	}
}

// An unavailable discriminator is an ambiguous identity: registration (the
// entry point of every trust flow) fails closed with a typed, audited denial
// instead of guessing (G-lane F2 fail-closed capability semantics).
func TestValidationUnsupportedFailsClosedTyped(t *testing.T) {
	store := NewMemStore()
	fv := &fakeValidator{err: platform.ErrValidationUnsupported}
	a := New(Deps{Store: store, Clock: platform.NewFakeClock(1_000), FS: fakeFS{dev: 7, ino: 42}, Validator: fv})
	a.Start()
	t.Cleanup(a.Stop)
	ctx := context.Background()

	_, err := a.RegisterProject(ctx, mkRoot(t))
	if err == nil {
		t.Fatal("registration succeeded without a replacement-validation discriminator")
	}
	if CodeOf(err) != v1.ErrProjectTrustRequired {
		t.Fatalf("unsupported discriminator must be typed project_trust_required, got %v", err)
	}
	if n := countAudit(t, a, AuditActivationDeny); n != 1 {
		t.Fatalf("fail-closed registration denial not audited: %d records", n)
	}
}

// Ordinary project work — creating, rewriting, and removing children — must
// never invalidate trust (the discriminator is content-churn-immune).
func TestOrdinaryContentChangesPreserveTrust(t *testing.T) {
	a, _ := newTestActor(t)
	ctx := context.Background()
	root := mkRoot(t)
	key, err := a.RegisterProject(ctx, root)
	if err != nil {
		t.Fatal(err)
	}
	e1, err := a.ApproveProject(ctx, "s1", key)
	if err != nil {
		t.Fatal(err)
	}

	child := filepath.Join(root, "main.go")
	if err := os.WriteFile(child, []byte("package main"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "internal", "pkg"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(child, []byte("package main // edited"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(filepath.Join(root, "internal")); err != nil {
		t.Fatal(err)
	}

	key2, err := a.RegisterProject(ctx, root)
	if err != nil || key2 != key {
		t.Fatalf("re-registration after content churn: %s err=%v", key2, err)
	}
	rec, ok, _ := a.Project(ctx, key)
	if !ok || rec.State != StateApproved || rec.Epoch != e1 {
		t.Fatalf("content churn disturbed trust: %+v ok=%v (want approved at epoch %d)", rec, ok, e1)
	}
}

func TestAuthorizeLaunchLifecycle(t *testing.T) {
	a, _ := newTestActor(t)
	ctx := context.Background()
	root := mkRoot(t)
	key, _ := a.RegisterProject(ctx, root)

	// Registered but unapproved: deny project_trust_required, audited.
	res, err := a.AuthorizeLaunch(ctx, key, "h1", "no-grant", allowRuntime())
	if err != nil {
		t.Fatal(err)
	}
	if res.Decision.Allow || res.Decision.Code != v1.ErrProjectTrustRequired {
		t.Fatalf("unapproved project authorize = %+v", res.Decision)
	}

	if _, err := a.ApproveProject(ctx, "s1", key); err != nil {
		t.Fatal(err)
	}
	// Approved, no grant: hook_grant_required.
	res, _ = a.AuthorizeLaunch(ctx, key, "h1", "missing", allowRuntime())
	if res.Decision.Code != v1.ErrHookGrantRequired {
		t.Fatalf("missing grant authorize = %+v", res.Decision)
	}

	gid, err := a.ApproveGrant(ctx, "s1", key, grantInput())
	if err != nil {
		t.Fatal(err)
	}
	res, _ = a.AuthorizeLaunch(ctx, key, "h1", gid, allowRuntime())
	if !res.Decision.Allow {
		t.Fatalf("baseline authorize denied: %+v", res.Decision)
	}

	// Re-approving the project bumps the epoch; the old grant is now stale.
	if _, err := a.ApproveProject(ctx, "s1", key); err != nil {
		t.Fatal(err)
	}
	res, _ = a.AuthorizeLaunch(ctx, key, "h1", gid, allowRuntime())
	if res.Decision.Code != v1.ErrHookGrantStale {
		t.Fatalf("stale-epoch authorize = %+v", res.Decision)
	}

	deniedBefore := countAudit(t, a, AuditActivationDeny)
	if deniedBefore < 3 {
		t.Fatalf("denials not audited: %d", deniedBefore)
	}
}

func countAudit(t *testing.T, a *Actor, kind AuditKind) int {
	t.Helper()
	recs, err := a.Audit(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	n := 0
	for _, r := range recs {
		if r.Kind == kind {
			n++
		}
	}
	return n
}

// TestNoAuthorizeLinearizesAfterRevoke is the real-actor version of the
// ordering model's two-sessions stress (ADR-0004): 2×200 authorizations race
// one revocation; after RevokeProject returns, no authorization may succeed,
// and the total allowed count equals the spawn-audit tally. Run under -race.
func TestNoAuthorizeLinearizesAfterRevoke(t *testing.T) {
	a, _ := newTestActor(t)
	ctx := context.Background()
	root := mkRoot(t)
	key, _ := a.RegisterProject(ctx, root)
	if _, err := a.ApproveProject(ctx, "s1", key); err != nil {
		t.Fatal(err)
	}
	gid, err := a.ApproveGrant(ctx, "s1", key, grantInput())
	if err != nil {
		t.Fatal(err)
	}

	preEpoch, err := a.Epoch(ctx, key)
	if err != nil {
		t.Fatal(err)
	}

	// The linearization proof is epoch-based: every allow decision carries the
	// epoch it linearized at. An allow requires GrantBoundEpoch == CurrentEpoch,
	// and the revoke bumps the epoch AND deactivates the grant, so an allow at
	// the post-revoke epoch is structurally a launch ordered after revocation.
	var revokeEpoch atomic.Uint64
	var allowedAtPostRevokeEpoch atomic.Int64
	var wg sync.WaitGroup
	for s := 0; s < 2; s++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 200; i++ {
				res, err := a.AuthorizeLaunch(ctx, key, "h1", gid, allowRuntime())
				if err != nil {
					t.Errorf("AuthorizeLaunch: %v", err)
					return
				}
				if res.Decision.Allow && res.Epoch != preEpoch {
					allowedAtPostRevokeEpoch.Add(1)
				}
			}
		}()
	}
	wg.Add(1)
	go func() {
		defer wg.Done()
		e, err := a.RevokeProject(ctx, "s1", key)
		if err != nil {
			t.Errorf("RevokeProject: %v", err)
			return
		}
		revokeEpoch.Store(e)
	}()
	wg.Wait()

	if got := allowedAtPostRevokeEpoch.Load(); got != 0 {
		t.Fatalf("%d authorizations linearized after the revocation epoch bump", got)
	}
	if revokeEpoch.Load() <= preEpoch {
		t.Fatalf("revocation did not bump the epoch: %d -> %d", preEpoch, revokeEpoch.Load())
	}
	// After the dust settles, a fresh authorization must fail closed.
	res, _ := a.AuthorizeLaunch(ctx, key, "h1", gid, allowRuntime())
	if res.Decision.Allow {
		t.Fatal("authorization succeeded after revocation")
	}
}

// Revoke listeners run post-commit on the actor goroutine, exactly once per
// revocation, observing the bumped epoch.
func TestRevokeListener(t *testing.T) {
	a, _ := newTestActor(t)
	ctx := context.Background()
	root := mkRoot(t)
	key, _ := a.RegisterProject(ctx, root)
	if _, err := a.ApproveProject(ctx, "s1", key); err != nil {
		t.Fatal(err)
	}

	type notice struct {
		key   ProjectKey
		epoch uint64
	}
	got := make(chan notice, 1)
	if err := a.OnRevoke(ctx, func(p ProjectKey, e uint64) {
		got <- notice{p, e}
	}); err != nil {
		t.Fatal(err)
	}
	e2, err := a.RevokeProject(ctx, "s1", key)
	if err != nil {
		t.Fatal(err)
	}
	n := <-got
	if n.key != key || n.epoch != e2 {
		t.Fatalf("listener saw %+v, want (%s, %d)", n, key, e2)
	}
}

func TestGrantBoundsRejectedNotClamped(t *testing.T) {
	a, _ := newTestActor(t)
	ctx := context.Background()
	root := mkRoot(t)
	key, _ := a.RegisterProject(ctx, root)
	if _, err := a.ApproveProject(ctx, "s1", key); err != nil {
		t.Fatal(err)
	}
	in := grantInput()
	in.TimeoutMS = MaxTimeoutMS + 1
	if _, err := a.ApproveGrant(ctx, "s1", key, in); CodeOf(err) != v1.ErrInvalidArgument {
		t.Fatalf("oversized timeout: err=%v", err)
	}
	in = grantInput()
	in.OutputCap = MaxOutputCapBytes + 1
	if _, err := a.ApproveGrant(ctx, "s1", key, in); CodeOf(err) != v1.ErrInvalidArgument {
		t.Fatalf("oversized cap: err=%v", err)
	}
}

func TestStoppedActorFailsClosed(t *testing.T) {
	store := NewMemStore()
	a := New(Deps{Store: store})
	a.Start()
	a.Stop()
	if _, err := a.Epoch(context.Background(), "k"); err == nil {
		t.Fatal("stopped actor served a request")
	}
}
