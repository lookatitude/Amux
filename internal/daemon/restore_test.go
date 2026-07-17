package daemon

import (
	"context"
	"errors"
	"io"
	"os"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	v1 "github.com/amux-run/amux/api/v1"

	"github.com/amux-run/amux/internal/control"
	"github.com/amux-run/amux/internal/domain"
	"github.com/amux-run/amux/internal/platform"
	"github.com/amux-run/amux/internal/rpcapi"
)

// holdPTY yields handles that emit one payload and then stay ALIVE until
// signaled/closed (Wait blocks), so a spawned surface deterministically keeps
// a live, daemon-owned process across save/restore — the precondition the
// in-daemon live-reconcile tests need.
type holdPTY struct{ output []byte }

func (f holdPTY) Start(spec platform.PTYSpec) (platform.PTYHandle, error) {
	return &holdHandle{out: f.output, exit: make(chan struct{})}, nil
}

type holdHandle struct {
	mu     sync.Mutex
	out    []byte
	off    int
	exit   chan struct{}
	closed bool
}

func (h *holdHandle) Read(p []byte) (int, error) {
	h.mu.Lock()
	if h.off < len(h.out) {
		n := copy(p, h.out[h.off:])
		h.off += n
		h.mu.Unlock()
		return n, nil
	}
	h.mu.Unlock()
	<-h.exit
	return 0, io.EOF
}
func (h *holdHandle) Write(p []byte) (int, error)   { return len(p), nil }
func (h *holdHandle) Resize(platform.PTYSize) error { return nil }
func (h *holdHandle) Signal(os.Signal) error        { h.shut(); return nil }
func (h *holdHandle) Wait() (platform.PTYExit, error) {
	<-h.exit
	return platform.PTYExit{Code: 0}, nil
}
func (h *holdHandle) MasterFD() uintptr { return 0 }
func (h *holdHandle) Close() error      { h.shut(); return nil }
func (h *holdHandle) shut() {
	h.mu.Lock()
	if !h.closed {
		h.closed = true
		close(h.exit)
	}
	h.mu.Unlock()
}

// recordPTY wraps holdPTY and records every spawn spec, so a test can assert
// exactly which processes a restore launched and with what argv/cwd/env/size.
type recordPTY struct {
	output []byte
	mu     *sync.Mutex
	specs  *[]platform.PTYSpec
}

func (f recordPTY) Start(spec platform.PTYSpec) (platform.PTYHandle, error) {
	f.mu.Lock()
	*f.specs = append(*f.specs, spec)
	f.mu.Unlock()
	return holdPTY{output: f.output}.Start(spec)
}

// limitedPTY allows a fixed number of successful starts and then fails every
// later one — the injection seam for the relaunch-failure fail-closed tests.
type limitedPTY struct {
	allowed *atomic.Int32
	output  []byte
}

func (f limitedPTY) Start(spec platform.PTYSpec) (platform.PTYHandle, error) {
	if f.allowed.Add(-1) < 0 {
		return nil, errors.New("injected launch failure: no PTY available")
	}
	return holdPTY{output: f.output}.Start(spec)
}

// newPTYEngine builds an engine over dir with the given PTY factory. Sharing
// dir across two engines models two daemon incarnations over the same
// snapshot root.
func newPTYEngine(t *testing.T, dir string, factory PTYFactory) *Engine {
	t.Helper()
	ctrl := control.New(control.Deps{Store: control.NewMemStore(), Clock: platform.NewSystemClock()})
	ctrl.Start()
	t.Cleanup(ctrl.Stop)
	e, err := New(Deps{
		Control:     ctrl,
		Clock:       platform.NewSystemClock(),
		PTY:         factory,
		SnapshotDir: dir,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(e.Close)
	return e
}

// newHoldEngine builds an engine over dir whose PTYs stay alive until stopped.
func newHoldEngine(t *testing.T, dir string) *Engine {
	return newPTYEngine(t, dir, func() platform.PTY { return holdPTY{output: []byte("hold-marker\r\n")} })
}

// holdSpawn creates a session with one long-lived spawned surface and waits
// until its output reached the replay ring.
func holdSpawn(t *testing.T, e *Engine) (sess string, ws rpcapi.WorkspaceCreateResult, surf string) {
	sess, ws, surf, _ = holdSpawnPolicy(t, e, "")
	return sess, ws, surf
}

// holdSpawnPolicy is holdSpawn with an explicit restart policy; it also
// returns the spawn cwd so relaunch tests can assert the replacement spec.
func holdSpawnPolicy(t *testing.T, e *Engine, policy string) (sess string, ws rpcapi.WorkspaceCreateResult, surf, cwd string) {
	t.Helper()
	ctx := context.Background()
	created, err := e.CreateSession(ctx, "hold")
	if err != nil {
		t.Fatal(err)
	}
	w, err := e.CreateWorkspace(ctx, rpcapi.WorkspaceCreateParams{Session: created.ID})
	if err != nil {
		t.Fatal(err)
	}
	cwd = t.TempDir()
	sp, err := e.SpawnSurface(ctx, rpcapi.SurfaceSpawnParams{
		Session: created.ID, Workspace: w.Workspace, Pane: w.FirstPane,
		Argv: []string{"/bin/echo", "hold"}, Cwd: cwd, RestartPolicy: policy,
	})
	if err != nil {
		t.Fatal(err)
	}
	waitReplayContains(t, e, created.ID, sp.Surface, "hold-marker")
	return created.ID, w, sp.Surface, cwd
}

// waitMarkerCount polls until surf's replay holds exactly want hold-markers
// (relaunch output arrives asynchronously through the supervisor pump).
func waitMarkerCount(t *testing.T, e *Engine, sess, surf string, want int) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	got := -1
	for time.Now().Before(deadline) {
		got = strings.Count(replayText(t, e, sess, surf), "hold-marker")
		if got == want {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("replay marker count = %d, want %d", got, want)
}

// restoredClass returns surf's class/reason from a restore result.
func restoredClass(t *testing.T, res rpcapi.SnapshotRestoreResult, surf string) (string, string) {
	t.Helper()
	for _, s := range res.Surfaces {
		if s.Surface == surf {
			return s.Class, s.Reason
		}
	}
	t.Fatalf("surface %s missing from restore result %+v", surf, res.Surfaces)
	return "", ""
}

// waitSurfaceStopped polls the (idempotent) confirmed stop until the surface's
// recorded classification is stopped — i.e. the exit was reaped.
func waitSurfaceStopped(t *testing.T, e *Engine, sess string, ws rpcapi.WorkspaceCreateResult, surf string) {
	t.Helper()
	ctx := context.Background()
	waitFor := func() bool {
		res, err := e.StopSurface(ctx, rpcapi.SurfaceStopParams{
			Session: sess, Workspace: ws.Workspace, Pane: ws.FirstPane, Surface: surf, Confirm: true,
		})
		return err == nil && res.Class == "stopped"
	}
	deadline := 200
	for i := 0; i < deadline; i++ {
		if waitFor() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("surface %s never reached stopped", surf)
}

// An in-daemon restore of the checkpoint this daemon saved, while the daemon
// still owns the SAME live PTY identity, must classify the surface live and
// leave the process untouched (spec success criterion 5; ADR-0005 precedence
// rule 2). This drives the full production path: save → restore on the same
// engine.
func TestInDaemonRestoreReconcilesOwnedSurfaceLive(t *testing.T) {
	e := newHoldEngine(t, t.TempDir())
	ctx := context.Background()
	sess, ws, surf := holdSpawn(t, e)

	if _, err := e.SaveSnapshot(ctx, rpcapi.SnapshotSaveParams{Session: sess}); err != nil {
		t.Fatalf("save: %v", err)
	}
	res, err := e.RestoreSnapshot(ctx, rpcapi.SnapshotRestoreParams{Session: sess})
	if err != nil {
		t.Fatalf("restore: %v", err)
	}
	class, reason := restoredClass(t, res, surf)
	if class != "live" {
		t.Fatalf("in-daemon restore of a still-owned surface classified %q (%s), want live", class, reason)
	}
	if reason != "reconciled to still-owned pty identity" {
		t.Fatalf("live reason = %q", reason)
	}

	// Untouched process: restart must refuse (the surface is already live)…
	if _, err := e.RestartSurface(ctx, rpcapi.SurfaceRestartParams{
		Session: sess, Workspace: ws.Workspace, Pane: ws.FirstPane, Surface: surf,
	}); Code(err) != v1.ErrConflict {
		t.Fatalf("restart after live reconcile = %v, want conflict (surface must stay live)", err)
	}
	// …and the replay holds exactly ONE spawn marker (no relaunch happened).
	if got := strings.Count(replayText(t, e, sess, surf), "hold-marker"); got != 1 {
		t.Fatalf("marker count after live reconcile = %d, want 1 (process was restarted?)", got)
	}

	// A second save/restore cycle re-reconciles the adopted identity live.
	if _, err := e.SaveSnapshot(ctx, rpcapi.SnapshotSaveParams{Session: sess}); err != nil {
		t.Fatalf("second save: %v", err)
	}
	res2, err := e.RestoreSnapshot(ctx, rpcapi.SnapshotRestoreParams{Session: sess})
	if err != nil {
		t.Fatalf("second restore: %v", err)
	}
	if class, _ := restoredClass(t, res2, surf); class != "live" {
		t.Fatalf("second in-daemon restore classified %q, want live", class)
	}
}

// A surface whose process the daemon no longer owns (stopped after the save)
// must NOT classify live on an in-daemon restore — it falls through to the
// exact fail-closed policy classification.
func TestInDaemonRestoreWithoutOwnershipNeverLive(t *testing.T) {
	e := newHoldEngine(t, t.TempDir())
	ctx := context.Background()
	sess, ws, surf := holdSpawn(t, e)

	if _, err := e.SaveSnapshot(ctx, rpcapi.SnapshotSaveParams{Session: sess}); err != nil {
		t.Fatalf("save: %v", err)
	}
	waitSurfaceStopped(t, e, sess, ws, surf)

	res, err := e.RestoreSnapshot(ctx, rpcapi.SnapshotRestoreParams{Session: sess})
	if err != nil {
		t.Fatalf("restore: %v", err)
	}
	class, reason := restoredClass(t, res, surf)
	if class != "stopped" {
		t.Fatalf("unowned surface classified %q, want stopped", class)
	}
	if reason != "manual restart policy (default): surface not relaunched" {
		t.Fatalf("stopped reason = %q", reason)
	}

	// Manual policy never launches: the replay holds only the checkpoint's
	// single marker and the supervisor owns no process for the surface.
	if got := strings.Count(replayText(t, e, sess, surf), "hold-marker"); got != 1 {
		t.Fatalf("manual-policy restore launched a process: %d markers, want 1", got)
	}
	rt, err := e.runtime(domain.SessionID(sess))
	if err != nil {
		t.Fatal(err)
	}
	if rt.sup.Alive(surf) {
		t.Fatal("manual-policy restore left a live process under the supervisor")
	}
}

// A LIVE process that is not the identity captured by the checkpoint (the
// surface was stopped and restarted between save and restore) must not be
// claimed live — same surface id is NOT same PTY identity. The impostor is
// stopped fail-closed so the reported classification is true.
func TestInDaemonRestoreIdentityMismatchNotLive(t *testing.T) {
	e := newHoldEngine(t, t.TempDir())
	ctx := context.Background()
	sess, ws, surf := holdSpawn(t, e)

	if _, err := e.SaveSnapshot(ctx, rpcapi.SnapshotSaveParams{Session: sess}); err != nil {
		t.Fatalf("save: %v", err)
	}
	waitSurfaceStopped(t, e, sess, ws, surf)
	if _, err := e.RestartSurface(ctx, rpcapi.SurfaceRestartParams{
		Session: sess, Workspace: ws.Workspace, Pane: ws.FirstPane, Surface: surf,
	}); err != nil {
		t.Fatalf("restart: %v", err)
	}

	res, err := e.RestoreSnapshot(ctx, rpcapi.SnapshotRestoreParams{Session: sess})
	if err != nil {
		t.Fatalf("restore: %v", err)
	}
	class, _ := restoredClass(t, res, surf)
	if class == "live" {
		t.Fatal("restore claimed live for a process spawned AFTER the checkpoint (identity mismatch)")
	}

	// Fail-closed follow-through: the mismatched live process was stopped, so
	// the surface becomes restartable again (no conflict) once reaped.
	// The reap is asynchronous, so the poll tolerates transient conflicts from
	// both the engine (live flag) and the supervisor (process not yet retired).
	restartable := false
	for i := 0; i < 200; i++ {
		if _, err := e.RestartSurface(ctx, rpcapi.SurfaceRestartParams{
			Session: sess, Workspace: ws.Workspace, Pane: ws.FirstPane, Surface: surf,
		}); err == nil {
			restartable = true
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if !restartable {
		t.Fatal("mismatched live process was not stopped by the fail-closed restore")
	}
}

// A FRESH daemon incarnation restoring the same checkpoint can NEVER classify
// live — even while the saving daemon's process is literally still running.
// Live requires ownership evidence only the saving incarnation holds.
func TestFreshDaemonRestoreNeverLiveEvenWhileProcessStillRuns(t *testing.T) {
	dir := t.TempDir()
	a := newHoldEngine(t, dir)
	ctx := context.Background()
	sess, _, surf := holdSpawn(t, a)
	if _, err := a.SaveSnapshot(ctx, rpcapi.SnapshotSaveParams{Session: sess}); err != nil {
		t.Fatalf("save: %v", err)
	}

	// Second incarnation over the same snapshot root; engine a's PTY is still
	// alive, but b owns nothing.
	b := newHoldEngine(t, dir)
	res, err := b.RestoreSnapshot(ctx, rpcapi.SnapshotRestoreParams{Session: sess})
	if err != nil {
		t.Fatalf("fresh restore: %v", err)
	}
	class, reason := restoredClass(t, res, surf)
	if class == "live" {
		t.Fatal("fresh daemon classified a surface live (resurrection claim)")
	}
	if class != "stopped" || reason != "manual restart policy (default): surface not relaunched" {
		t.Fatalf("fresh restore class/reason = %q/%q", class, reason)
	}
	// The replay history was rebuilt from the committed sidecar — and holds
	// ONLY the checkpoint's marker: a fresh manual restore never launches.
	if got := strings.Count(replayText(t, b, sess, surf), "hold-marker"); got != 1 {
		t.Fatalf("fresh manual restore replay marker count = %d, want exactly 1", got)
	}
}

// ADR-0005 defines `restarted` as a NEW process launched under an explicit
// automatic policy — a claim about completed behavior. An in-daemon restore of
// an automatic-policy surface whose process is gone must LAUNCH the
// replacement through the production supervisor and only then report
// restarted: exactly one replacement, with the restored argv/cwd/env and the
// restore geometry, emitting into the restored replay stream.
func TestInDaemonRestoreAutomaticPolicyRelaunches(t *testing.T) {
	var mu sync.Mutex
	var specs []platform.PTYSpec
	e := newPTYEngine(t, t.TempDir(), func() platform.PTY {
		return recordPTY{output: []byte("hold-marker\r\n"), mu: &mu, specs: &specs}
	})
	ctx := context.Background()
	sess, ws, surf, cwd := holdSpawnPolicy(t, e, "automatic")

	if _, err := e.SaveSnapshot(ctx, rpcapi.SnapshotSaveParams{Session: sess}); err != nil {
		t.Fatalf("save: %v", err)
	}
	waitSurfaceStopped(t, e, sess, ws, surf)

	res, err := e.RestoreSnapshot(ctx, rpcapi.SnapshotRestoreParams{Session: sess})
	if err != nil {
		t.Fatalf("restore: %v", err)
	}
	class, reason := restoredClass(t, res, surf)
	if class != "restarted" {
		t.Fatalf("automatic-policy restore classified %q (%s), want restarted", class, reason)
	}
	if reason != "relaunched under automatic restart policy" {
		t.Fatalf("restarted reason = %q", reason)
	}

	// Exactly one replacement launch, carrying the restored argv/cwd/env and
	// the restore geometry (24x80 — the size the restored VT grid re-derives at).
	mu.Lock()
	launches := append([]platform.PTYSpec(nil), specs...)
	mu.Unlock()
	if len(launches) != 2 {
		t.Fatalf("PTY starts = %d, want 2 (initial spawn + exactly one relaunch)", len(launches))
	}
	re := launches[1]
	if !reflect.DeepEqual(re.Argv, []string{"/bin/echo", "hold"}) || re.Dir != cwd || len(re.Env) != 0 {
		t.Fatalf("relaunch spec = %+v, want restored argv/cwd/env", re)
	}
	if re.Size.Rows != 24 || re.Size.Cols != 80 {
		t.Fatalf("relaunch geometry = %+v, want 24x80", re.Size)
	}

	// The replacement is genuinely live (restart refuses with conflict) and
	// its output joined the restored stream after the checkpoint history.
	if _, err := e.RestartSurface(ctx, rpcapi.SurfaceRestartParams{
		Session: sess, Workspace: ws.Workspace, Pane: ws.FirstPane, Surface: surf,
	}); Code(err) != v1.ErrConflict {
		t.Fatalf("restart after relaunch = %v, want conflict (replacement must be live)", err)
	}
	waitMarkerCount(t, e, sess, surf, 2)

	// The replacement is a first-class owned identity: a fresh save/restore
	// cycle adopts it live (the minted spawn identity is attestable).
	if _, err := e.SaveSnapshot(ctx, rpcapi.SnapshotSaveParams{Session: sess}); err != nil {
		t.Fatalf("second save: %v", err)
	}
	res2, err := e.RestoreSnapshot(ctx, rpcapi.SnapshotRestoreParams{Session: sess})
	if err != nil {
		t.Fatalf("second restore: %v", err)
	}
	if class, reason := restoredClass(t, res2, surf); class != "live" {
		t.Fatalf("restore after relaunch classified %q (%s), want live adoption", class, reason)
	}
}

// A failed replacement launch must NEVER surface as restarted (or live): the
// surface fails closed to stopped with the exact launch-failure reason, no
// false live owner is retained anywhere, and the surface stays explicitly
// restartable once launching works again.
func TestInDaemonRestoreAutomaticPolicyLaunchFailureFailsClosed(t *testing.T) {
	var allowed atomic.Int32
	allowed.Store(1) // the initial spawn only; the restore relaunch must fail
	e := newPTYEngine(t, t.TempDir(), func() platform.PTY {
		return limitedPTY{allowed: &allowed, output: []byte("hold-marker\r\n")}
	})
	ctx := context.Background()
	sess, ws, surf, _ := holdSpawnPolicy(t, e, "automatic")

	if _, err := e.SaveSnapshot(ctx, rpcapi.SnapshotSaveParams{Session: sess}); err != nil {
		t.Fatalf("save: %v", err)
	}
	waitSurfaceStopped(t, e, sess, ws, surf)

	// The restore itself succeeds (graph + replay rebuilt); only the surface
	// fails closed.
	res, err := e.RestoreSnapshot(ctx, rpcapi.SnapshotRestoreParams{Session: sess})
	if err != nil {
		t.Fatalf("restore: %v", err)
	}
	class, reason := restoredClass(t, res, surf)
	if class != "stopped" {
		t.Fatalf("failed relaunch classified %q (%s), want stopped", class, reason)
	}
	if !strings.Contains(reason, "automatic restart policy but relaunch failed") ||
		!strings.Contains(reason, "injected launch failure") {
		t.Fatalf("failed-relaunch reason = %q, want the exact launch failure", reason)
	}

	// No false live owner: engine runtime and supervisor agree, and the replay
	// holds only the checkpoint history (nothing was launched).
	rt, err := e.runtime(domain.SessionID(sess))
	if err != nil {
		t.Fatal(err)
	}
	rt.mu.Lock()
	live := rt.surfaces[domain.SurfaceID(surf)].live
	rt.mu.Unlock()
	if live {
		t.Fatal("failed relaunch left the surface marked live")
	}
	if rt.sup.Alive(surf) {
		t.Fatal("failed relaunch left a live process under the supervisor")
	}
	if got := strings.Count(replayText(t, e, sess, surf), "hold-marker"); got != 1 {
		t.Fatalf("replay marker count after failed relaunch = %d, want 1", got)
	}

	// Fail-closed is recoverable: once launches succeed again, the explicit
	// restart flow relaunches the surface.
	allowed.Store(1)
	if _, err := e.RestartSurface(ctx, rpcapi.SurfaceRestartParams{
		Session: sess, Workspace: ws.Workspace, Pane: ws.FirstPane, Surface: surf,
	}); err != nil {
		t.Fatalf("restart after recovered seam: %v", err)
	}
}

// A FRESH daemon restoring an automatic-policy surface performs a REAL
// replacement launch under its own supervisor before reporting restarted
// (fresh-daemon manual restore stays never-live:
// TestFreshDaemonRestoreNeverLiveEvenWhileProcessStillRuns).
func TestFreshDaemonRestoreAutomaticPolicyRelaunches(t *testing.T) {
	dir := t.TempDir()
	a := newHoldEngine(t, dir)
	ctx := context.Background()
	sess, _, surf, _ := holdSpawnPolicy(t, a, "automatic")
	if _, err := a.SaveSnapshot(ctx, rpcapi.SnapshotSaveParams{Session: sess}); err != nil {
		t.Fatalf("save: %v", err)
	}

	// Second incarnation over the same snapshot root: it owns nothing, so live
	// is structurally excluded — automatic policy demands an actual relaunch.
	b := newHoldEngine(t, dir)
	res, err := b.RestoreSnapshot(ctx, rpcapi.SnapshotRestoreParams{Session: sess})
	if err != nil {
		t.Fatalf("fresh restore: %v", err)
	}
	class, reason := restoredClass(t, res, surf)
	if class != "restarted" {
		t.Fatalf("fresh automatic restore classified %q (%s), want restarted", class, reason)
	}
	if reason != "relaunched under automatic restart policy" {
		t.Fatalf("restarted reason = %q", reason)
	}

	// The replacement runs under b's supervisor and its fresh marker joined
	// the history rebuilt from the committed sidecar.
	rt, err := b.runtime(domain.SessionID(sess))
	if err != nil {
		t.Fatal(err)
	}
	if !rt.sup.Alive(surf) {
		t.Fatal("fresh automatic restore reported restarted without a live replacement")
	}
	waitMarkerCount(t, b, sess, surf, 2)
}
