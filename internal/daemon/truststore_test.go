package daemon

import (
	"context"
	"path/filepath"
	"sync"
	"testing"

	v1 "github.com/amux-run/amux/api/v1"

	"github.com/amux-run/amux/internal/control"
	"github.com/amux-run/amux/internal/platform"
	"github.com/amux-run/amux/internal/store"
)

// These tests prove the restart/persistence half of the G-lane F2 fix: trust
// persisted in SQLite re-enters a fresh control actor ONLY through the
// replacement-validation recheck in RegisterProject. A replaced root is
// detected from the PERSISTED discriminator across a daemon restart — not
// merely by an in-memory comparison against state captured at registration.

// rehydrateFS pins (dev, ino) so the durable key is identical across the
// simulated replacement — the overlayfs inode-reuse class.
type rehydrateFS struct{ dev, ino uint64 }

func (f rehydrateFS) Identify(path string) (string, platform.FSIdentity, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", platform.FSIdentity{}, err
	}
	return abs, platform.FSIdentity{Dev: f.dev, Ino: f.ino}, nil
}

// rehydrateValidator scripts the discriminator each actor incarnation sees.
type rehydrateValidator struct {
	mu sync.Mutex
	id platform.ValidationID
}

func (v *rehydrateValidator) ValidationID(string) (platform.ValidationID, error) {
	v.mu.Lock()
	defer v.mu.Unlock()
	return v.id, nil
}

func (v *rehydrateValidator) set(id platform.ValidationID) {
	v.mu.Lock()
	defer v.mu.Unlock()
	v.id = id
}

func newRestartActor(t *testing.T, ts control.TrustStore, val platform.ReplacementValidator) *control.Actor {
	t.Helper()
	a := control.New(control.Deps{
		Store:     ts,
		Clock:     platform.NewFakeClock(1_000),
		FS:        rehydrateFS{dev: 7, ino: 42},
		Validator: val,
	})
	a.Start()
	return a
}

func TestTrustRehydratesAcrossRestartAndRevalidates(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(filepath.Join(t.TempDir(), "db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	ts := NewTrustStore(st)
	root := filepath.Join(t.TempDir(), "project")
	val := &rehydrateValidator{id: platform.ValidationID{Scheme: "test", Value: "birth-1"}}

	// Incarnation 1: register, approve, grant.
	a1 := newRestartActor(t, ts, val)
	key, err := a1.RegisterProject(ctx, root)
	if err != nil {
		t.Fatal(err)
	}
	e1, err := a1.ApproveProject(ctx, "s1", key)
	if err != nil {
		t.Fatal(err)
	}
	gid, err := a1.ApproveGrant(ctx, "s1", key, control.GrantInput{
		HookID: "h1", ExecPath: "/tmp/hook.sh", ExecSHA256: "aa", ConfigSHA256: "bb",
		AllowedEvents: []string{"pane_exit"}, Scope: control.ScopeNone,
		TimeoutMS: 2000, OutputCap: 1 << 20,
	})
	if err != nil {
		t.Fatal(err)
	}
	a1.Stop()

	// Incarnation 2 (restart, root unchanged): persisted trust rehydrates
	// through the validation recheck and the epoch remains monotonic — a
	// fresh approval continues above the persisted epoch instead of failing
	// the store's non-rollback gate.
	a2 := newRestartActor(t, ts, val)
	key2, err := a2.RegisterProject(ctx, root)
	if err != nil {
		t.Fatal(err)
	}
	if key2 != key {
		t.Fatalf("rehydrated key drifted: %s vs %s", key2, key)
	}
	rec, ok, err := a2.Project(ctx, key)
	if err != nil || !ok {
		t.Fatalf("rehydrated record: ok=%v err=%v", ok, err)
	}
	if rec.State != control.StateApproved || rec.Epoch != e1 {
		t.Fatalf("trust did not survive restart with matching discriminator: %+v (want approved/%d)", rec, e1)
	}
	e2, err := a2.ApproveProject(ctx, "s1", key)
	if err != nil || e2 <= e1 {
		t.Fatalf("post-restart approve must continue the monotonic epoch: %d -> %d err=%v", e1, e2, err)
	}
	a2.Stop()

	// Incarnation 3 (restart, root REPLACED while the daemon was down: same
	// (dev, ino), new birth time): rehydration must invalidate, not reuse.
	val.set(platform.ValidationID{Scheme: "test", Value: "birth-2"})
	a3 := newRestartActor(t, ts, val)
	key3, err := a3.RegisterProject(ctx, root)
	if err != nil {
		t.Fatal(err)
	}
	if key3 != key {
		t.Fatalf("frozen key definition must hold under (dev, ino) reuse: %s vs %s", key3, key)
	}
	rec, ok, err = a3.Project(ctx, key)
	if err != nil || !ok {
		t.Fatalf("record after invalidating rehydration: ok=%v err=%v", ok, err)
	}
	if rec.State == control.StateApproved {
		t.Fatal("replaced root regained approved trust across a restart")
	}
	if rec.Epoch <= e2 {
		t.Fatalf("invalidation epoch not monotonic across restart: %d -> %d", e2, rec.Epoch)
	}
	// The persisted grant must be inactive in the durable store as well.
	rows, err := st.ListGrants(string(key), false)
	if err != nil {
		t.Fatal(err)
	}
	for _, g := range rows {
		if g.ID == gid && g.Active {
			t.Fatal("grant remained active in SQLite after identity invalidation")
		}
	}
	// The invalidation is audited durably as a system revocation.
	audit, err := a3.Audit(ctx)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, r := range audit {
		if r.Kind == control.AuditProjectRevoked && r.Code == v1.ErrProjectTrustRequired && r.Epoch == rec.Epoch {
			found = true
		}
	}
	if !found {
		t.Fatalf("restart invalidation not audited: %+v", audit)
	}
	a3.Stop()
}

// A row persisted before the validation mechanism existed (empty
// discriminator, migration default) is AMBIGUOUS: approved trust must be
// denied reuse — invalidated on the first post-upgrade registration.
func TestAmbiguousPersistedValidationDeniesTrustReuse(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(filepath.Join(t.TempDir(), "db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	ts := NewTrustStore(st)
	root := filepath.Join(t.TempDir(), "project")

	// Seed a pre-migration-shaped row: approved at epoch 1, no discriminator.
	abs, _ := filepath.Abs(root)
	pk, _, err := platform.ComputeProjectKey(rehydrateFS{dev: 7, ino: 42}, abs)
	if err != nil {
		t.Fatal(err)
	}
	if err := ts.SaveProject(control.ProjectRecord{
		Key: control.ProjectKey(pk), Root: abs,
		Identity: platform.FSIdentity{Dev: 7, Ino: 42},
		State:    control.StateApproved, Epoch: 1,
	}); err != nil {
		t.Fatal(err)
	}

	val := &rehydrateValidator{id: platform.ValidationID{Scheme: "test", Value: "birth-1"}}
	a := newRestartActor(t, ts, val)
	defer a.Stop()
	key, err := a.RegisterProject(ctx, root)
	if err != nil {
		t.Fatal(err)
	}
	rec, ok, err := a.Project(ctx, key)
	if err != nil || !ok {
		t.Fatalf("record: ok=%v err=%v", ok, err)
	}
	if rec.State == control.StateApproved {
		t.Fatal("ambiguous (absent) persisted discriminator allowed trust reuse")
	}
	if rec.Epoch != 2 {
		t.Fatalf("ambiguity invalidation must bump the epoch: got %d", rec.Epoch)
	}
	if rec.Validation.IsZero() {
		t.Fatal("record must now carry the current object's discriminator")
	}
}
