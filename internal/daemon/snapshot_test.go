package daemon

import (
	"context"
	"encoding/base64"
	"strings"
	"testing"
	"time"

	"github.com/amux-run/amux/internal/control"
	"github.com/amux-run/amux/internal/platform"
	"github.com/amux-run/amux/internal/rpcapi"
)

// argvEchoPTY emits the spawned command's last argv element as its whole
// output, so every surface's raw stream is distinguishable in tests.
type argvEchoPTY struct{}

func (argvEchoPTY) Start(spec platform.PTYSpec) (platform.PTYHandle, error) {
	out := spec.Argv[len(spec.Argv)-1] + "\r\n"
	return &fakeHandle{out: []byte(out), exit: make(chan struct{})}, nil
}

func newArgvEchoEngine(t *testing.T) *Engine {
	t.Helper()
	ctrl := control.New(control.Deps{Store: control.NewMemStore(), Clock: platform.NewSystemClock()})
	ctrl.Start()
	t.Cleanup(ctrl.Stop)
	e, err := New(Deps{
		Control:     ctrl,
		Clock:       platform.NewSystemClock(),
		PTY:         func() platform.PTY { return argvEchoPTY{} },
		SnapshotDir: t.TempDir(),
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(e.Close)
	return e
}

// replayText reads a surface's full raw replay and decodes it to text.
func replayText(t *testing.T, e *Engine, session, surface string) string {
	t.Helper()
	res, err := e.ReplayRead(context.Background(), rpcapi.ReplayReadParams{Session: session, Surface: surface, FromSeq: 1})
	if err != nil {
		t.Fatalf("replay %s: %v", surface, err)
	}
	var sb strings.Builder
	for _, c := range res.Chunks {
		b, err := base64.StdEncoding.DecodeString(c.DataB64)
		if err != nil {
			t.Fatal(err)
		}
		sb.Write(b)
	}
	return sb.String()
}

// waitReplayContains polls until the surface's replay contains want.
func waitReplayContains(t *testing.T, e *Engine, session, surface, want string) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if strings.Contains(replayText(t, e, session, surface), want) {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("surface %s replay never contained %q", surface, want)
}

// A checkpoint holding TWO surfaces must restore each surface's raw replay
// from its OWN sidecar section: surface sequence spaces overlap (both start at
// 1), so any cross-surface bleed corrupts replay. This pins the per-surface
// sidecar partitioning end to end (save -> commit -> restore -> replay).
func TestSnapshotRestorePartitionsMultiSurfaceReplay(t *testing.T) {
	e := newArgvEchoEngine(t)
	ctx := context.Background()
	sess, err := e.CreateSession(ctx, "multi")
	if err != nil {
		t.Fatal(err)
	}
	ws, err := e.CreateWorkspace(ctx, rpcapi.WorkspaceCreateParams{Session: sess.ID})
	if err != nil {
		t.Fatal(err)
	}

	spawn := func(word string) string {
		sp, err := e.SpawnSurface(ctx, rpcapi.SurfaceSpawnParams{
			Session: sess.ID, Workspace: ws.Workspace, Pane: ws.FirstPane,
			Argv: []string{"/bin/echo", word}, Cwd: t.TempDir(),
		})
		if err != nil {
			t.Fatalf("spawn %s: %v", word, err)
		}
		return sp.Surface
	}
	surfA := spawn("alpha-payload")
	surfB := spawn("bravo-payload")
	waitReplayContains(t, e, sess.ID, surfA, "alpha-payload")
	waitReplayContains(t, e, sess.ID, surfB, "bravo-payload")

	if _, err := e.SaveSnapshot(ctx, rpcapi.SnapshotSaveParams{Session: sess.ID}); err != nil {
		t.Fatalf("save: %v", err)
	}
	restored, err := e.RestoreSnapshot(ctx, rpcapi.SnapshotRestoreParams{Session: sess.ID})
	if err != nil {
		t.Fatalf("restore: %v", err)
	}
	// Only spawned surfaces carry a runtime (the workspace's first surface is
	// graph-only until something spawns on it), so exactly the two spawned
	// surfaces restore with a classification.
	if len(restored.Surfaces) != 2 {
		t.Fatalf("restored %d surfaces: %+v", len(restored.Surfaces), restored.Surfaces)
	}
	for _, s := range restored.Surfaces {
		// The echo processes exited after their output, so no surface carries
		// ownable live identity — none may classify live.
		if s.Class == "live" {
			t.Fatalf("restore without owned identity classified %s live", s.Surface)
		}
	}

	gotA := replayText(t, e, sess.ID, surfA)
	gotB := replayText(t, e, sess.ID, surfB)
	if !strings.Contains(gotA, "alpha-payload") || strings.Contains(gotA, "bravo-payload") {
		t.Fatalf("surface A replay corrupted after restore: %q", gotA)
	}
	if !strings.Contains(gotB, "bravo-payload") || strings.Contains(gotB, "alpha-payload") {
		t.Fatalf("surface B replay corrupted after restore: %q", gotB)
	}
}
