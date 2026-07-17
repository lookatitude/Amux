package control

import (
	"context"
	"errors"
	"testing"

	v1 "github.com/amux-run/amux/api/v1"

	"github.com/amux-run/amux/internal/platform"
)

// failingStore wraps a TrustStore and fails ApplyTransition on demand — the
// deterministic failure injection for the memory path (G-lane F1). Everything
// else delegates.
type failingStore struct {
	TrustStore
	failApply error
}

func (f *failingStore) ApplyTransition(t TrustTransition) ([]string, error) {
	if f.failApply != nil {
		return nil, f.failApply
	}
	return f.TrustStore.ApplyTransition(t)
}

func auditKinds(t *testing.T, a *Actor) []AuditKind {
	t.Helper()
	recs, err := a.Audit(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	kinds := make([]AuditKind, 0, len(recs))
	for _, r := range recs {
		kinds = append(kinds, r.Kind)
	}
	return kinds
}

// The memory implementation of ApplyTransition provides the same
// all-or-nothing semantics as the SQLite transaction: a refused transition
// (non-monotonic epoch) mutates nothing, and a committed one moves project
// state, grant activity, and the exact audit records together.
func TestMemStoreApplyTransitionAtomicSemantics(t *testing.T) {
	m := NewMemStore()
	rec := ProjectRecord{Key: "p1", Root: "/tmp/p1", State: StateApproved, Epoch: 1,
		Validation: platform.ValidationID{Scheme: "test", Value: "birth-1"}}
	if err := m.SaveProject(rec); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"g2", "g1"} {
		if err := m.SaveGrant(GrantRecord{ID: id, Project: "p1", HookID: "h", BoundEpoch: 1, Active: true}); err != nil {
			t.Fatal(err)
		}
	}
	if err := m.SaveGrant(GrantRecord{ID: "gx", Project: "other", Active: true}); err != nil {
		t.Fatal(err)
	}

	// Non-monotonic epoch: refused, nothing moves.
	bad := rec
	bad.State = StateRevoked
	if _, err := m.ApplyTransition(TrustTransition{Project: bad, DeactivateGrants: true,
		Audit: &AuditRecord{Kind: AuditProjectRevoked, Project: "p1", Epoch: 1}}); !errors.Is(err, ErrEpochNotMonotonic) {
		t.Fatalf("equal epoch: err = %v, want ErrEpochNotMonotonic", err)
	}
	got, _, _ := m.LoadProject("p1")
	if got.State != StateApproved || got.Epoch != 1 {
		t.Fatalf("refused transition mutated project: %+v", got)
	}
	if recs, _ := m.ListAudit(); len(recs) != 0 {
		t.Fatalf("refused transition appended audit: %+v", recs)
	}

	// Unregistered project: refused.
	if _, err := m.ApplyTransition(TrustTransition{Project: ProjectRecord{Key: "ghost", State: StateApproved, Epoch: 1}}); err == nil {
		t.Fatal("transition on unregistered project succeeded")
	}

	// Committed revocation: state+epoch+discriminator, deactivation, exact audit.
	next := rec
	next.State = StateRevoked
	next.Epoch = 2
	next.Validation = platform.ValidationID{Scheme: "test", Value: "birth-2"}
	ids, err := m.ApplyTransition(TrustTransition{
		Project: next, DeactivateGrants: true,
		Audit:      &AuditRecord{Kind: AuditProjectRevoked, Project: "p1", Epoch: 2, Code: v1.ErrProjectTrustRequired},
		GrantAudit: &AuditRecord{Kind: AuditGrantInactive, Project: "p1", Epoch: 2},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(ids) != 2 || ids[0] != "g1" || ids[1] != "g2" {
		t.Fatalf("deactivated IDs = %v, want [g1 g2] (deterministic order)", ids)
	}
	got, _, _ = m.LoadProject("p1")
	if got.State != StateRevoked || got.Epoch != 2 || got.Validation.Value != "birth-2" {
		t.Fatalf("committed transition = %+v, want revoked/2/birth-2", got)
	}
	recs, _ := m.ListAudit()
	if len(recs) != 3 || recs[0].Kind != AuditProjectRevoked ||
		recs[1].Kind != AuditGrantInactive || recs[2].Kind != AuditGrantInactive {
		t.Fatalf("audit after commit = %+v, want [project_revoked grant_inactive grant_inactive]", recs)
	}
	if recs[0].Code != v1.ErrProjectTrustRequired {
		t.Fatalf("main record lost its code: %+v", recs[0])
	}
}

// A failed operator revocation leaves EVERY observer at the old approved
// state — memory and store agree, the grant still authorizes, no partial
// audit exists, no listener fired — and the retry succeeds without any
// epoch conflict, after which containment is complete.
func TestRevokeFailureLeavesConsistentStateAndRetries(t *testing.T) {
	fs := &failingStore{TrustStore: NewMemStore()}
	a := New(Deps{Store: fs, Clock: platform.NewFakeClock(1_000)})
	a.Start()
	t.Cleanup(a.Stop)
	ctx := context.Background()

	key, err := a.RegisterProject(ctx, mkRoot(t))
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
	var notices []uint64
	if err := a.OnRevoke(ctx, func(_ ProjectKey, e uint64) { notices = append(notices, e) }); err != nil {
		t.Fatal(err)
	}
	kindsBefore := auditKinds(t, a)

	injected := errors.New("injected commit failure")
	fs.failApply = injected
	if _, err := a.RevokeProject(ctx, "s1", key); !errors.Is(err, injected) {
		t.Fatalf("failed revoke: err = %v, want injected failure", err)
	}

	// No observer sees a partial transition: actor state, durable state, and
	// authorization all still agree on the old approved epoch.
	rec, ok, _ := a.Project(ctx, key)
	if !ok || rec.State != StateApproved || rec.Epoch != e1 {
		t.Fatalf("failed revoke mutated actor state: %+v", rec)
	}
	durable, found, err := fs.LoadProject(key)
	if err != nil || !found || durable.State != StateApproved || durable.Epoch != e1 {
		t.Fatalf("failed revoke mutated durable state: %+v found=%v err=%v", durable, found, err)
	}
	if g, _, _ := a.Grant(ctx, gid); !g.Active {
		t.Fatal("failed revoke deactivated the in-memory grant")
	}
	res, _ := a.AuthorizeLaunch(ctx, key, "h1", gid, allowRuntime())
	if !res.Decision.Allow || res.Epoch != e1 {
		t.Fatalf("authorization after failed revoke = %+v (epoch %d), want allow at old epoch %d", res.Decision, res.Epoch, e1)
	}
	if len(notices) != 0 {
		t.Fatalf("listener notified before durable commit: %v", notices)
	}
	if got := auditKinds(t, a); len(got) != len(kindsBefore) {
		t.Fatalf("failed revoke left partial audit: %v -> %v", kindsBefore, got)
	}

	// Retry: same actor, no epoch conflict, full containment.
	fs.failApply = nil
	e2, err := a.RevokeProject(ctx, "s1", key)
	if err != nil {
		t.Fatalf("retry after failed revoke: %v", err)
	}
	if e2 != e1+1 {
		t.Fatalf("retry epoch = %d, want %d (the failed attempt must not consume epochs)", e2, e1+1)
	}
	if len(notices) != 1 || notices[0] != e2 {
		t.Fatalf("listener notices = %v, want exactly one at epoch %d", notices, e2)
	}
	res, _ = a.AuthorizeLaunch(ctx, key, "h1", gid, allowRuntime())
	if res.Decision.Allow {
		t.Fatal("authorization succeeded after committed revocation")
	}
	got := auditKinds(t, a)
	tail := got[len(kindsBefore):]
	// The deny above appends its own record; the transition itself must have
	// appended exactly [project_revoked, grant_inactive].
	if len(tail) < 2 || tail[0] != AuditProjectRevoked || tail[1] != AuditGrantInactive {
		t.Fatalf("audit tail after retry = %v, want [project_revoked grant_inactive ...]", tail)
	}
}

// A failed replacement invalidation (the (dev, ino)-reuse path) also mutates
// nothing: the error surfaces from RegisterProject, trust remains at the old
// approved epoch on both sides, and the next registration retries the
// invalidation cleanly.
func TestReplacementInvalidationFailureRetriesCleanly(t *testing.T) {
	fs := &failingStore{TrustStore: NewMemStore()}
	fv := &fakeValidator{id: platform.ValidationID{Scheme: "test", Value: "birth-1"}}
	a := New(Deps{Store: fs, Clock: platform.NewFakeClock(1_000), FS: fakeFS{dev: 7, ino: 42}, Validator: fv})
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
	kindsBefore := auditKinds(t, a)

	// The root is replaced under (dev, ino) reuse AND the durable commit fails.
	fv.set(platform.ValidationID{Scheme: "test", Value: "birth-2"}, nil)
	injected := errors.New("injected commit failure")
	fs.failApply = injected
	if _, err := a.RegisterProject(ctx, root); !errors.Is(err, injected) {
		t.Fatalf("failed invalidation: err = %v, want injected failure", err)
	}
	rec, ok, _ := a.Project(ctx, key)
	if !ok || rec.State != StateApproved || rec.Epoch != e1 {
		t.Fatalf("failed invalidation mutated actor state: %+v", rec)
	}
	durable, _, _ := fs.LoadProject(key)
	if durable.State != StateApproved || durable.Epoch != e1 {
		t.Fatalf("failed invalidation mutated durable state: %+v", durable)
	}
	if g, _, _ := a.Grant(ctx, gid); !g.Active {
		t.Fatal("failed invalidation deactivated the grant")
	}
	if got := auditKinds(t, a); len(got) != len(kindsBefore) {
		t.Fatalf("failed invalidation left partial audit: %v -> %v", kindsBefore, got)
	}

	// Retry: the next registration performs the invalidation with the SAME
	// epoch bump — the failed attempt consumed nothing.
	fs.failApply = nil
	key2, err := a.RegisterProject(ctx, root)
	if err != nil || key2 != key {
		t.Fatalf("retry registration: key=%s err=%v", key2, err)
	}
	rec, _, _ = a.Project(ctx, key)
	if rec.State != StateRevoked || rec.Epoch != e1+1 {
		t.Fatalf("retried invalidation = %+v, want revoked/%d", rec, e1+1)
	}
	if g, _, _ := a.Grant(ctx, gid); g.Active {
		t.Fatal("grant survived the retried invalidation")
	}
	tail := auditKinds(t, a)[len(kindsBefore):]
	if len(tail) != 2 || tail[0] != AuditProjectRevoked || tail[1] != AuditGrantInactive {
		t.Fatalf("audit tail after retried invalidation = %v, want [project_revoked grant_inactive]", tail)
	}
}

// Approve and deny transitions get the same fail-closed atomicity: a failed
// commit changes nothing and the retry proceeds from the old epoch.
func TestApproveDenyFailuresLeaveStateUntouched(t *testing.T) {
	fs := &failingStore{TrustStore: NewMemStore()}
	a := New(Deps{Store: fs, Clock: platform.NewFakeClock(1_000)})
	a.Start()
	t.Cleanup(a.Stop)
	ctx := context.Background()

	key, err := a.RegisterProject(ctx, mkRoot(t))
	if err != nil {
		t.Fatal(err)
	}
	injected := errors.New("injected commit failure")
	fs.failApply = injected
	if _, err := a.ApproveProject(ctx, "s1", key); !errors.Is(err, injected) {
		t.Fatalf("failed approve: err = %v, want injected failure", err)
	}
	rec, _, _ := a.Project(ctx, key)
	if rec.State != StateNone || rec.Epoch != 0 {
		t.Fatalf("failed approve mutated state: %+v", rec)
	}
	if kinds := auditKinds(t, a); len(kinds) != 0 {
		t.Fatalf("failed approve left audit: %v", kinds)
	}

	fs.failApply = nil
	e1, err := a.ApproveProject(ctx, "s1", key)
	if err != nil || e1 != 1 {
		t.Fatalf("retry approve: epoch=%d err=%v", e1, err)
	}

	fs.failApply = injected
	if err := a.DenyProject(ctx, "s1", key); !errors.Is(err, injected) {
		t.Fatalf("failed deny: err = %v, want injected failure", err)
	}
	rec, _, _ = a.Project(ctx, key)
	if rec.State != StateApproved || rec.Epoch != e1 {
		t.Fatalf("failed deny mutated state: %+v", rec)
	}
	fs.failApply = nil
	if err := a.DenyProject(ctx, "s1", key); err != nil {
		t.Fatalf("retry deny: %v", err)
	}
	rec, _, _ = a.Project(ctx, key)
	if rec.State != StateDenied || rec.Epoch != e1+1 {
		t.Fatalf("retried deny = %+v, want denied/%d", rec, e1+1)
	}
}
