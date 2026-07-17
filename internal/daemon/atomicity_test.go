package daemon

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"github.com/amux-run/amux/internal/control"
	"github.com/amux-run/amux/internal/platform"
	"github.com/amux-run/amux/internal/store"
)

// These tests prove the G-lane F1 durability contract END TO END: the control
// actor over the REAL SQLite adapter, with deterministic failures injected at
// every stage of the store's trust-transition transaction. Whichever
// statement fails, disk and memory both remain at the old approved
// epoch/state with grants active and zero partial audit; the retry succeeds
// without ErrEpochNotMonotonic; revoke listeners fire only after the durable
// commit; and a daemon restart rehydrates the pre-failure state after a
// failure and the committed state after success.

// txStages mirrors the failpoint labels inside store.ApplyTrustTransition.
var txStages = []string{"project-update", "deactivate-grants", "audit-main", "audit-grant", "commit"}

func atomicityGrant() control.GrantInput {
	return control.GrantInput{
		HookID: "h1", ExecPath: "/tmp/hook.sh", ExecSHA256: "aa", ConfigSHA256: "bb",
		AllowedEvents: []string{"pane_exit"}, Scope: control.ScopeNone,
		TimeoutMS: 2000, OutputCap: 1 << 20,
	}
}

func allowFacts() control.RuntimeFacts {
	return control.RuntimeFacts{
		RootIdentityMatch: true, ExecDigestMatch: true, ConfigDigestMatch: true,
		ConfigMatch:       control.ConfigMatch{EventSet: true, Scope: true, Env: true, Timeout: true, OutputCap: true},
		ExecIsRegularFile: true, EventAllowed: true,
		Scope: control.ScopeFacts{Kind: control.ScopeNone, Resolved: true},
	}
}

func TestOperatorRevocationAtomicAcrossActorAndSQLite(t *testing.T) {
	ctx := context.Background()
	dbDir := filepath.Join(t.TempDir(), "db")
	st, err := store.Open(dbDir)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	val := &rehydrateValidator{id: platform.ValidationID{Scheme: "test", Value: "birth-1"}}
	a := newRestartActor(t, NewTrustStore(st), val)
	root := filepath.Join(t.TempDir(), "project")

	key, err := a.RegisterProject(ctx, root)
	if err != nil {
		t.Fatal(err)
	}
	e1, err := a.ApproveProject(ctx, "s1", key)
	if err != nil {
		t.Fatal(err)
	}
	var gids []string
	for i := 0; i < 2; i++ {
		in := atomicityGrant()
		in.HookID = []string{"h1", "h2"}[i]
		gid, err := a.ApproveGrant(ctx, "s1", key, in)
		if err != nil {
			t.Fatal(err)
		}
		gids = append(gids, gid)
	}
	var notices []uint64
	if err := a.OnRevoke(ctx, func(_ control.ProjectKey, e uint64) { notices = append(notices, e) }); err != nil {
		t.Fatal(err)
	}
	auditBefore, err := st.CountAudit()
	if err != nil {
		t.Fatal(err)
	}

	// Inject a deterministic failure at EVERY transaction stage in turn.
	injected := errors.New("injected stage failure")
	for _, stage := range txStages {
		stage := stage
		st.SetTrustTransitionFailpoint(func(got string) error {
			if got == stage {
				return injected
			}
			return nil
		})
		if _, err := a.RevokeProject(ctx, "s1", key); !errors.Is(err, injected) {
			t.Fatalf("stage %s: revoke err = %v, want injected failure", stage, err)
		}
		st.SetTrustTransitionFailpoint(nil)

		// Memory: still approved at the old epoch, grants active.
		rec, ok, _ := a.Project(ctx, key)
		if !ok || rec.State != control.StateApproved || rec.Epoch != e1 {
			t.Fatalf("stage %s: actor state after failure = %+v", stage, rec)
		}
		// Disk: identical old state — no bumped epoch, grants active, no audit.
		row, err := st.GetProject(string(key))
		if err != nil || row.State != store.ProjectStateApproved || row.Epoch != e1 {
			t.Fatalf("stage %s: durable state after failure = %+v, %v", stage, row, err)
		}
		active, err := st.ListGrants(string(key), false)
		if err != nil || len(active) != 2 {
			t.Fatalf("stage %s: active grants after failure = %d, %v (want 2)", stage, len(active), err)
		}
		if n, _ := st.CountAudit(); n != auditBefore {
			t.Fatalf("stage %s: partial audit after failure: %d rows, want %d", stage, n, auditBefore)
		}
		if len(notices) != 0 {
			t.Fatalf("stage %s: listener notified before commit: %v", stage, notices)
		}
		// No observer can authorize from partially committed state: memory and
		// disk agree on approved/e1, so authorization still linearizes there.
		res, err := a.AuthorizeLaunch(ctx, key, "h1", gids[0], allowFacts())
		if err != nil || !res.Decision.Allow || res.Epoch != e1 {
			t.Fatalf("stage %s: authorize after failure = %+v epoch=%d err=%v (want allow at %d)",
				stage, res.Decision, res.Epoch, err, e1)
		}
	}

	// Retry with no failpoint: commits at e1+1 — the failed attempts consumed
	// no epoch (no ErrEpochNotMonotonic) — and the listener fires exactly once.
	e2, err := a.RevokeProject(ctx, "s1", key)
	if err != nil {
		t.Fatalf("retry revoke: %v", err)
	}
	if e2 != e1+1 {
		t.Fatalf("retry epoch = %d, want %d", e2, e1+1)
	}
	if len(notices) != 1 || notices[0] != e2 {
		t.Fatalf("listener notices = %v, want exactly [%d] after commit", notices, e2)
	}
	if res, _ := a.AuthorizeLaunch(ctx, key, "h1", gids[0], allowFacts()); res.Decision.Allow {
		t.Fatal("authorization succeeded after committed revocation")
	}

	// Exact audit shape: one project_revoked then exactly one grant_inactive
	// per deactivated grant, all at the committed epoch, appended exactly once
	// despite five failed attempts. (The deny above appends after them.)
	audit, err := st.ListAudit(0, 0)
	if err != nil {
		t.Fatal(err)
	}
	tail := audit[auditBefore:]
	wantKinds := []string{"project_revoked", "grant_inactive", "grant_inactive"}
	if len(tail) < len(wantKinds) {
		t.Fatalf("audit tail = %+v, want prefix %v", tail, wantKinds)
	}
	for i, kind := range wantKinds {
		if tail[i].Kind != kind || tail[i].Epoch != e2 {
			t.Fatalf("audit tail[%d] = %+v, want kind %s at epoch %d", i, tail[i], kind, e2)
		}
	}
	a.Stop()

	// Restart rehydration after success: a fresh actor incarnation over the
	// same database observes the committed revocation, and trust continues
	// monotonically above it.
	a2 := newRestartActor(t, NewTrustStore(st), val)
	defer a2.Stop()
	key2, err := a2.RegisterProject(ctx, root)
	if err != nil || key2 != key {
		t.Fatalf("restart registration: key=%s err=%v", key2, err)
	}
	rec, ok, _ := a2.Project(ctx, key)
	if !ok || rec.State != control.StateRevoked || rec.Epoch != e2 {
		t.Fatalf("rehydrated state = %+v, want revoked/%d", rec, e2)
	}
	e3, err := a2.ApproveProject(ctx, "s1", key)
	if err != nil || e3 != e2+1 {
		t.Fatalf("post-restart approve: epoch=%d err=%v, want %d", e3, err, e2+1)
	}
}

func TestReplacementInvalidationAtomicAcrossRestart(t *testing.T) {
	ctx := context.Background()
	dbDir := filepath.Join(t.TempDir(), "db")
	st, err := store.Open(dbDir)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	val := &rehydrateValidator{id: platform.ValidationID{Scheme: "test", Value: "birth-1"}}
	root := filepath.Join(t.TempDir(), "project")

	// Incarnation 1: approved project with an active grant.
	a1 := newRestartActor(t, NewTrustStore(st), val)
	key, err := a1.RegisterProject(ctx, root)
	if err != nil {
		t.Fatal(err)
	}
	e1, err := a1.ApproveProject(ctx, "s1", key)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a1.ApproveGrant(ctx, "s1", key, atomicityGrant()); err != nil {
		t.Fatal(err)
	}
	auditBefore, _ := st.CountAudit()

	// The root is replaced under (dev, ino) reuse, and every stage of the
	// invalidation transaction fails in turn: the error surfaces from
	// RegisterProject and NOTHING moves on disk or in memory.
	val.set(platform.ValidationID{Scheme: "test", Value: "birth-2"})
	injected := errors.New("injected stage failure")
	for _, stage := range txStages {
		stage := stage
		st.SetTrustTransitionFailpoint(func(got string) error {
			if got == stage {
				return injected
			}
			return nil
		})
		if _, err := a1.RegisterProject(ctx, root); !errors.Is(err, injected) {
			t.Fatalf("stage %s: invalidation err = %v, want injected failure", stage, err)
		}
		st.SetTrustTransitionFailpoint(nil)
		row, err := st.GetProject(string(key))
		if err != nil || row.State != store.ProjectStateApproved || row.Epoch != e1 {
			t.Fatalf("stage %s: durable state after failed invalidation = %+v, %v", stage, row, err)
		}
		if row.ValidationValue != "birth-1" {
			t.Fatalf("stage %s: failed invalidation moved the discriminator: %+v", stage, row)
		}
		if active, _ := st.ListGrants(string(key), false); len(active) != 1 {
			t.Fatalf("stage %s: grants after failed invalidation = %d active, want 1", stage, len(active))
		}
		if n, _ := st.CountAudit(); n != auditBefore {
			t.Fatalf("stage %s: partial audit after failed invalidation", stage)
		}
	}
	a1.Stop()

	// Restart rehydration after failure: a fresh incarnation sees the intact
	// approved state and retries the invalidation from the SAME epoch —
	// durably, with the full audit, and without ErrEpochNotMonotonic.
	a2 := newRestartActor(t, NewTrustStore(st), val)
	defer a2.Stop()
	key2, err := a2.RegisterProject(ctx, root)
	if err != nil || key2 != key {
		t.Fatalf("post-restart registration: key=%s err=%v", key2, err)
	}
	rec, ok, _ := a2.Project(ctx, key)
	if !ok || rec.State != control.StateRevoked || rec.Epoch != e1+1 {
		t.Fatalf("retried invalidation across restart = %+v, want revoked/%d", rec, e1+1)
	}
	row, _ := st.GetProject(string(key))
	if row.State != store.ProjectStateRevoked || row.Epoch != e1+1 || row.ValidationValue != "birth-2" {
		t.Fatalf("durable state after retried invalidation = %+v", row)
	}
	if active, _ := st.ListGrants(string(key), false); len(active) != 0 {
		t.Fatalf("grants still active after retried invalidation: %d", len(active))
	}
	audit, _ := st.ListAudit(0, 0)
	tail := audit[auditBefore:]
	if len(tail) != 2 || tail[0].Kind != "project_revoked" || tail[0].Code != "project_trust_required" ||
		tail[1].Kind != "grant_inactive" {
		t.Fatalf("audit tail after retried invalidation = %+v, want [project_revoked(project_trust_required) grant_inactive]", tail)
	}
}
