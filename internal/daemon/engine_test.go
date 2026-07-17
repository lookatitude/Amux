package daemon

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"os"
	"sync"
	"testing"
	"time"

	v1 "github.com/amux-run/amux/api/v1"

	"github.com/amux-run/amux/internal/control"
	"github.com/amux-run/amux/internal/domain"
	"github.com/amux-run/amux/internal/platform"
	"github.com/amux-run/amux/internal/rpcapi"
	"github.com/amux-run/amux/internal/terminal"
)

// fakePTY is a deterministic platform.PTY: Start yields a handle that emits a
// fixed output payload then EOF, so surface spawning is hermetic (no real
// process, no host dependency). It records every spawned handle so tests can
// wait on a handle's one-shot drained barrier instead of polling (G-lane F6).
// stallAt/stallFor inject one mid-drain read stall, to prove waiters survive
// scheduling pauses that defeat timing heuristics.
type fakePTY struct {
	output   []byte
	stallAt  int
	stallFor time.Duration

	mu      sync.Mutex
	handles []*fakeHandle
}

func (f *fakePTY) Start(spec platform.PTYSpec) (platform.PTYHandle, error) {
	h := &fakeHandle{out: f.output, exit: make(chan struct{}), stallAt: f.stallAt, stallFor: f.stallFor}
	f.mu.Lock()
	f.handles = append(f.handles, h)
	f.mu.Unlock()
	return h, nil
}

// lastHandle returns the most recently spawned handle. Spawning is synchronous
// (Engine.SpawnSurface returns only after PTY.Start), so calling this right
// after a spawn yields that spawn's handle.
func (f *fakePTY) lastHandle(t *testing.T) *fakeHandle {
	t.Helper()
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.handles) == 0 {
		t.Fatal("fake PTY has spawned no handle")
	}
	return f.handles[len(f.handles)-1]
}

type fakeHandle struct {
	mu     sync.Mutex
	out    []byte
	off    int
	exit   chan struct{}
	closed bool

	// drained is the one-shot EOF barrier: closed exactly once, when Read
	// first returns io.EOF — i.e. after every output byte has been handed to
	// the supervisor's read loop. Lazily created so direct &fakeHandle{...}
	// constructions elsewhere in the package stay valid.
	drained chan struct{}
	eof     bool

	// stallAt/stallFor inject one read stall once off reaches stallAt.
	stallAt  int
	stallFor time.Duration
	stalled  bool
}

// drainedLocked returns the drained channel, creating it if needed. Callers
// must hold h.mu.
func (h *fakeHandle) drainedLocked() chan struct{} {
	if h.drained == nil {
		h.drained = make(chan struct{})
	}
	return h.drained
}

// Drained exposes the one-shot EOF barrier. The supervisor's read loop appends
// each chunk to the replay ring synchronously (readLoop → OnOutput →
// ring.Append) before the EOF-returning Read closes this channel, so observing
// it guarantees ALL fake output has reached the ring.
func (h *fakeHandle) Drained() <-chan struct{} {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.drainedLocked()
}

func (h *fakeHandle) Read(p []byte) (int, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.stallAt > 0 && !h.stalled && h.off >= h.stallAt {
		h.stalled = true
		d := h.stallFor
		h.mu.Unlock()
		time.Sleep(d)
		h.mu.Lock()
	}
	if h.off >= len(h.out) {
		if !h.eof {
			h.eof = true
			close(h.drainedLocked())
		}
		return 0, io.EOF
	}
	n := copy(p, h.out[h.off:])
	h.off += n
	return n, nil
}
func (h *fakeHandle) Write(p []byte) (int, error) { return len(p), nil }
func (h *fakeHandle) Resize(platform.PTYSize) error {
	return nil
}
func (h *fakeHandle) Signal(os.Signal) error { return nil }

// Wait models the process/output lifecycle coherently: the fake process
// "exits" only after its output is fully drained (a real child blocks on PTY
// writes until the master side reads them), so the supervisor's reap path
// never runs ahead of undelivered output.
func (h *fakeHandle) Wait() (platform.PTYExit, error) {
	<-h.Drained()
	return platform.PTYExit{Code: 0}, nil
}
func (h *fakeHandle) MasterFD() uintptr { return 0 }
func (h *fakeHandle) Close() error {
	h.mu.Lock()
	h.closed = true
	h.mu.Unlock()
	return nil
}

func newEngine(t *testing.T) *Engine {
	t.Helper()
	return newEnginePTY(t, &fakePTY{output: []byte("hello world\r\n")})
}

// newEnginePTY builds an Engine over the given fake PTY, which the test keeps
// so it can wait on spawned handles' drained barriers.
func newEnginePTY(t *testing.T, f *fakePTY) *Engine {
	t.Helper()
	ctrl := control.New(control.Deps{Store: control.NewMemStore(), Clock: platform.NewSystemClock()})
	ctrl.Start()
	t.Cleanup(ctrl.Stop)
	e, err := New(Deps{
		Control:     ctrl,
		Clock:       platform.NewSystemClock(),
		PTY:         func() platform.PTY { return f },
		SnapshotDir: t.TempDir(),
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(e.Close)
	return e
}

func TestEngineGraphFlows(t *testing.T) {
	e := newEngine(t)
	ctx := context.Background()

	sess, err := e.CreateSession(ctx, "work")
	if err != nil {
		t.Fatal(err)
	}
	sessions, err := e.ListSessions(ctx)
	if err != nil || len(sessions) != 1 || sessions[0].ID != sess.ID {
		t.Fatalf("ListSessions = %v, %v", sessions, err)
	}

	ws, err := e.CreateWorkspace(ctx, rpcapi.WorkspaceCreateParams{Session: sess.ID, Name: "main"})
	if err != nil {
		t.Fatal(err)
	}
	if ws.Rev == 0 || ws.FirstPane == "" {
		t.Fatalf("workspace create result incomplete: %+v", ws)
	}

	sp, err := e.SplitPane(ctx, rpcapi.PaneSplitParams{
		Session: sess.ID, Workspace: ws.Workspace, Target: ws.FirstPane, Orientation: rpcapi.OrientHorizontal,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.FocusPane(ctx, rpcapi.PaneFocusParams{Session: sess.ID, Workspace: ws.Workspace, Pane: ws.FirstPane}); err != nil {
		t.Fatal(err)
	}
	if _, err := e.ResizePane(ctx, rpcapi.PaneResizeParams{Session: sess.ID, Workspace: ws.Workspace, Pane: sp.NewPane, Ratio: 0.4}); err != nil {
		t.Fatal(err)
	}
	wl, err := e.ListWorkspaces(ctx, rpcapi.WorkspaceListParams{Session: sess.ID})
	if err != nil || len(wl.Workspaces) != 1 || wl.Workspaces[0].PaneCount != 2 {
		t.Fatalf("ListWorkspaces = %+v, %v", wl, err)
	}
}

func TestEngineSurfaceSpawnReplayInspect(t *testing.T) {
	e := newEngine(t)
	ctx := context.Background()
	sess, _ := e.CreateSession(ctx, "s")
	ws, _ := e.CreateWorkspace(ctx, rpcapi.WorkspaceCreateParams{Session: sess.ID})

	sp, err := e.SpawnSurface(ctx, rpcapi.SurfaceSpawnParams{
		Session: sess.ID, Workspace: ws.Workspace, Pane: ws.FirstPane,
		Title: "sh", Argv: []string{"/bin/sh"}, Cwd: t.TempDir(), Cols: 80, Rows: 24,
	})
	if err != nil {
		t.Fatal(err)
	}

	// The fake PTY's output must land in the surface's replay ring.
	deadline := time.Now().Add(2 * time.Second)
	var replay rpcapi.ReplayReadResult
	for time.Now().Before(deadline) {
		replay, err = e.ReplayRead(ctx, rpcapi.ReplayReadParams{Session: sess.ID, Surface: sp.Surface, FromSeq: 1})
		if err == nil && len(replay.Chunks) > 0 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if len(replay.Chunks) == 0 {
		t.Fatal("no raw output reached the replay ring")
	}

	insp, err := e.InspectPane(ctx, rpcapi.PaneInspectParams{Session: sess.ID, Workspace: ws.Workspace, Pane: ws.FirstPane})
	if err != nil {
		t.Fatal(err)
	}
	if len(insp.Surfaces) < 2 { // first surface + spawned
		t.Fatalf("inspect surfaces = %+v", insp.Surfaces)
	}
}

func TestEngineSurfaceStopRequiresConfirmation(t *testing.T) {
	e := newEngine(t)
	ctx := context.Background()
	sess, _ := e.CreateSession(ctx, "s")
	ws, _ := e.CreateWorkspace(ctx, rpcapi.WorkspaceCreateParams{Session: sess.ID})
	sp, _ := e.SpawnSurface(ctx, rpcapi.SurfaceSpawnParams{Session: sess.ID, Workspace: ws.Workspace, Pane: ws.FirstPane, Argv: []string{"/bin/sh"}, Cwd: t.TempDir()})

	_, err := e.StopSurface(ctx, rpcapi.SurfaceStopParams{Session: sess.ID, Workspace: ws.Workspace, Pane: ws.FirstPane, Surface: sp.Surface, Confirm: false})
	if Code(err) != v1.ErrInvalidArgument {
		t.Fatalf("stop without confirmation should fail closed, got %v", err)
	}
	if _, err := e.StopSurface(ctx, rpcapi.SurfaceStopParams{Session: sess.ID, Workspace: ws.Workspace, Pane: ws.FirstPane, Surface: sp.Surface, Confirm: true}); err != nil {
		t.Fatalf("confirmed stop: %v", err)
	}
}

// A restore without ownership evidence must classify every surface
// stopped|restarted, NEVER live (spec success criterion 5): the fake PTY here
// exits immediately after its output, so even this in-daemon restore owns no
// live identity to reconcile. The fresh-daemon variant is pinned in
// TestFreshDaemonRestoreNeverLiveEvenWhileProcessStillRuns.
func TestEngineSnapshotSaveRestoreNeverLive(t *testing.T) {
	e := newEngine(t)
	ctx := context.Background()
	sess, _ := e.CreateSession(ctx, "s")
	ws, _ := e.CreateWorkspace(ctx, rpcapi.WorkspaceCreateParams{Session: sess.ID})
	sp, _ := e.SpawnSurface(ctx, rpcapi.SurfaceSpawnParams{
		Session: sess.ID, Workspace: ws.Workspace, Pane: ws.FirstPane,
		Argv: []string{"/bin/sh"}, Cwd: t.TempDir(), RestartPolicy: "manual",
	})
	_ = sp
	time.Sleep(50 * time.Millisecond) // let output reach the ring

	saved, err := e.SaveSnapshot(ctx, rpcapi.SnapshotSaveParams{Session: sess.ID})
	if err != nil {
		t.Fatalf("save: %v", err)
	}
	if saved.CheckpointID == "" {
		t.Fatal("no checkpoint id")
	}

	restored, err := e.RestoreSnapshot(ctx, rpcapi.SnapshotRestoreParams{Session: sess.ID})
	if err != nil {
		t.Fatalf("restore: %v", err)
	}
	if len(restored.Surfaces) == 0 {
		t.Fatal("restore produced no surfaces")
	}
	for _, s := range restored.Surfaces {
		if s.Class == "live" {
			t.Fatalf("fresh-daemon restore classified surface %s as live (resurrection)", s.Surface)
		}
		if s.Reason == "" {
			t.Fatalf("surface %s restored without a reason", s.Surface)
		}
	}
}

func TestEngineEnvAllowlistRejectsValues(t *testing.T) {
	e := newEngine(t)
	ctx := context.Background()
	sess, _ := e.CreateSession(ctx, "s")
	ws, _ := e.CreateWorkspace(ctx, rpcapi.WorkspaceCreateParams{Session: sess.ID})
	_, err := e.SpawnSurface(ctx, rpcapi.SurfaceSpawnParams{
		Session: sess.ID, Workspace: ws.Workspace, Pane: ws.FirstPane,
		Argv: []string{"/bin/sh"}, Cwd: t.TempDir(), Env: []string{"SECRET=value"},
	})
	if Code(err) != v1.ErrInvalidArgument {
		t.Fatalf("env key=value must be rejected, got %v", err)
	}
}

// newEngineOutput is newEngine with a custom fake-PTY payload, for tests that
// need a specific amount of output in the replay ring. It returns the fake PTY
// too so callers can wait on the spawned handle's drained barrier.
func newEngineOutput(t *testing.T, output []byte) (*Engine, *fakePTY) {
	t.Helper()
	f := &fakePTY{output: output}
	return newEnginePTY(t, f), f
}

// replayPayload builds a deterministic printable payload so byte-exact
// reassembly across pages is provable (and VT-engine feeding stays cheap).
func replayPayload(n int) []byte {
	p := make([]byte, n)
	for i := range p {
		if i%64 == 63 {
			p[i] = '\n'
		} else {
			p[i] = 'a' + byte(i%26)
		}
	}
	return p
}

// spawnAndQuiesce spawns a surface over e's fake PTY and blocks on the fake
// handle's one-shot drained barrier. The supervisor's read loop appends every
// chunk to the replay ring synchronously (readLoop → OnOutput → ring.Append)
// before the EOF-returning Read closes the barrier, so on return ALL fake
// output is in the ring. This is an exact lifecycle barrier, not a timing
// heuristic: a stalled read loop delays the barrier instead of being mistaken
// for quiescence (G-lane F6). The timeout only bounds a genuinely hung drain.
func spawnAndQuiesce(t *testing.T, e *Engine, f *fakePTY) (sid, surface string) {
	t.Helper()
	ctx := context.Background()
	sess, err := e.CreateSession(ctx, "replay-bound")
	if err != nil {
		t.Fatal(err)
	}
	ws, err := e.CreateWorkspace(ctx, rpcapi.WorkspaceCreateParams{Session: sess.ID})
	if err != nil {
		t.Fatal(err)
	}
	sp, err := e.SpawnSurface(ctx, rpcapi.SurfaceSpawnParams{
		Session: sess.ID, Workspace: ws.Workspace, Pane: ws.FirstPane,
		Argv: []string{"/bin/sh"}, Cwd: t.TempDir(),
	})
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-f.lastHandle(t).Drained():
	case <-time.After(2 * time.Minute):
		t.Fatal("fake PTY output never drained")
	}
	return sess.ID, sp.Surface
}

// decodedBytes sums the raw decoded payload of a replay page.
func decodedBytes(t *testing.T, res rpcapi.ReplayReadResult) int64 {
	t.Helper()
	var n int64
	for _, c := range res.Chunks {
		b, err := base64.StdEncoding.DecodeString(c.DataB64)
		if err != nil {
			t.Fatalf("chunk %d: bad base64: %v", c.Seq, err)
		}
		n += int64(len(b))
	}
	return n
}

// TestEngineReplayReadEnforcesMaxBytes pins the flow-14 bound contract:
// max_bytes 0 means the server default page bound, a positive bound caps the
// raw decoded payload, an oversized ask clamps to the server cap, the encoded
// unary response always stays under v1.MaxHeaderBytes, and a partial page's
// next_seq points at the first sequence NOT returned so continuation is
// contiguous and duplicate-free.
func TestEngineReplayReadEnforcesMaxBytes(t *testing.T) {
	payload := replayPayload(3 << 20) // 3 MiB retained, well past any one page
	e, f := newEngineOutput(t, payload)
	ctx := context.Background()
	sid, surface := spawnAndQuiesce(t, e, f)

	// Default bound (max_bytes 0): bounded page, encodable under the header cap.
	res, err := e.ReplayRead(ctx, rpcapi.ReplayReadParams{Session: sid, Surface: surface, FromSeq: 1})
	if err != nil {
		t.Fatalf("default replay read: %v", err)
	}
	if len(res.Chunks) == 0 {
		t.Fatal("default read returned no chunks")
	}
	if got := decodedBytes(t, res); got > replayReadMaxBytes {
		t.Fatalf("default page decoded %d bytes > server cap %d", got, replayReadMaxBytes)
	}
	encoded, err := json.Marshal(res)
	if err != nil {
		t.Fatal(err)
	}
	// The unary result is carried inside the response frame HEADER: it must
	// stay under v1.MaxHeaderBytes with room for the response envelope.
	if len(encoded) >= v1.MaxHeaderBytes-4096 {
		t.Fatalf("encoded default page = %d bytes, not safely under MaxHeaderBytes %d", len(encoded), v1.MaxHeaderBytes)
	}
	if res.NextSeq != res.Chunks[len(res.Chunks)-1].Seq+1 {
		t.Fatalf("partial page NextSeq = %d, want last returned seq+1 = %d", res.NextSeq, res.Chunks[len(res.Chunks)-1].Seq+1)
	}
	if res.NextSeq > res.LatestSeq {
		t.Fatalf("3 MiB retained must not fit one page: NextSeq %d > LatestSeq %d", res.NextSeq, res.LatestSeq)
	}

	// Explicit caller bound: decoded bytes never exceed it.
	res, err = e.ReplayRead(ctx, rpcapi.ReplayReadParams{Session: sid, Surface: surface, FromSeq: 1, MaxBytes: 64 << 10})
	if err != nil {
		t.Fatalf("bounded replay read: %v", err)
	}
	if got := decodedBytes(t, res); got > 64<<10 || len(res.Chunks) == 0 {
		t.Fatalf("64 KiB bound returned %d bytes in %d chunks", got, len(res.Chunks))
	}

	// Oversized ask: the server cap still bounds the page.
	res, err = e.ReplayRead(ctx, rpcapi.ReplayReadParams{Session: sid, Surface: surface, FromSeq: 1, MaxBytes: 1 << 40})
	if err != nil {
		t.Fatalf("oversized-bound replay read: %v", err)
	}
	if got := decodedBytes(t, res); got > replayReadMaxBytes {
		t.Fatalf("oversized ask decoded %d bytes > server cap %d", got, replayReadMaxBytes)
	}

	// Continuation from next_seq walks the whole window contiguously with no
	// duplicate and reassembles the exact retained bytes.
	var reassembled []byte
	var wantSeq uint64 = 1
	cursor := uint64(1)
	for {
		page, err := e.ReplayRead(ctx, rpcapi.ReplayReadParams{Session: sid, Surface: surface, FromSeq: cursor, MaxBytes: 200 << 10})
		if err != nil {
			t.Fatalf("continuation from %d: %v", cursor, err)
		}
		if len(page.Chunks) == 0 {
			break
		}
		for _, c := range page.Chunks {
			if c.Seq != wantSeq {
				t.Fatalf("continuation broke sequence truth: got seq %d, want %d", c.Seq, wantSeq)
			}
			wantSeq++
			b, err := base64.StdEncoding.DecodeString(c.DataB64)
			if err != nil {
				t.Fatal(err)
			}
			reassembled = append(reassembled, b...)
		}
		cursor = page.NextSeq
	}
	if !bytes.Equal(reassembled, payload) {
		t.Fatalf("continuation reassembled %d bytes, want the exact %d-byte payload", len(reassembled), len(payload))
	}

	// Negative and too-tiny bounds fail typed, never split a chunk.
	_, err = e.ReplayRead(ctx, rpcapi.ReplayReadParams{Session: sid, Surface: surface, FromSeq: 1, MaxBytes: -1})
	if Code(err) != v1.ErrInvalidArgument {
		t.Fatalf("negative max_bytes: got %v (code %q), want typed invalid_argument", err, Code(err))
	}
	_, err = e.ReplayRead(ctx, rpcapi.ReplayReadParams{Session: sid, Surface: surface, FromSeq: 1, MaxBytes: 1})
	if Code(err) != v1.ErrInvalidArgument {
		t.Fatalf("max_bytes below the next whole chunk: got %v (code %q), want typed invalid_argument", err, Code(err))
	}
}

// TestEngineReplayReadGapStructuredDetails pins the automation contract for
// replay gaps: the typed replay_gap error carries structured details (oldest
// retained + latest sequence) so no consumer ever parses the human message,
// and continuation from the reported boundary succeeds.
func TestEngineReplayReadGapStructuredDetails(t *testing.T) {
	// 17 MiB through a 16 MiB ring floor: the oldest chunks are evicted.
	e, f := newEngineOutput(t, replayPayload(17<<20))
	ctx := context.Background()
	sid, surface := spawnAndQuiesce(t, e, f)

	_, err := e.ReplayRead(ctx, rpcapi.ReplayReadParams{Session: sid, Surface: surface, FromSeq: 1, MaxBytes: 1 << 20})
	if Code(err) != v1.ErrReplayGap {
		t.Fatalf("evicted cursor: got %v (code %q), want replay_gap", err, Code(err))
	}
	body := ErrorBody(err)
	if len(body.Details) == 0 {
		t.Fatalf("replay_gap carries no structured details: %+v", body)
	}
	var gap rpcapi.ReplayGapDetails
	if err := json.Unmarshal(body.Details, &gap); err != nil {
		t.Fatalf("replay_gap details do not decode: %v (%s)", err, body.Details)
	}
	if gap.OldestRetained <= 1 || gap.LatestSeq < gap.OldestRetained || gap.FromSeq != 1 {
		t.Fatalf("gap details = %+v", gap)
	}

	// The structured boundary is actionable: reading from it succeeds.
	res, err := e.ReplayRead(ctx, rpcapi.ReplayReadParams{Session: sid, Surface: surface, FromSeq: gap.OldestRetained, MaxBytes: 1 << 20})
	if err != nil {
		t.Fatalf("read from reported oldest retained %d: %v", gap.OldestRetained, err)
	}
	if len(res.Chunks) == 0 || res.Chunks[0].Seq != gap.OldestRetained {
		t.Fatalf("continuation from gap boundary returned %d chunks (first seq %d), want first = %d",
			len(res.Chunks), res.Chunks[0].Seq, gap.OldestRetained)
	}
}

// spawnMetaSurface spawns a metadata-only surface (no argv → no PTY, empty
// ring) and returns its ids plus direct access to its replay ring, so a test
// can drive appends deterministically against concurrent ReplayRead calls.
func spawnMetaSurface(t *testing.T, e *Engine) (sid, surface string, ring *terminal.Ring) {
	t.Helper()
	ctx := context.Background()
	sess, err := e.CreateSession(ctx, "replay-atomic")
	if err != nil {
		t.Fatal(err)
	}
	ws, err := e.CreateWorkspace(ctx, rpcapi.WorkspaceCreateParams{Session: sess.ID})
	if err != nil {
		t.Fatal(err)
	}
	sp, err := e.SpawnSurface(ctx, rpcapi.SurfaceSpawnParams{
		Session: sess.ID, Workspace: ws.Workspace, Pane: ws.FirstPane, Title: "meta",
	})
	if err != nil {
		t.Fatal(err)
	}
	rt, err := e.runtime(domain.SessionID(sess.ID))
	if err != nil {
		t.Fatal(err)
	}
	rt.mu.Lock()
	sr := rt.surfaces[domain.SurfaceID(sp.Surface)]
	rt.mu.Unlock()
	if sr == nil || sr.ring == nil {
		t.Fatal("spawned surface has no runtime ring")
	}
	return sess.ID, sp.Surface, sr.ring
}

// TestEngineReplayReadEmptyPageAppendBarrier is the deterministic F5 pin: a
// chunk appended immediately after an empty-page snapshot is returned on the
// next page and never skipped, and cursor semantics for current and
// ahead-of-latest cursors derive from the snapshot itself.
func TestEngineReplayReadEmptyPageAppendBarrier(t *testing.T) {
	e := newEngine(t)
	ctx := context.Background()
	sid, surface, ring := spawnMetaSurface(t, e)

	if _, err := ring.Append([]byte("first")); err != nil {
		t.Fatal(err)
	}
	res, err := e.ReplayRead(ctx, rpcapi.ReplayReadParams{Session: sid, Surface: surface, FromSeq: 1})
	if err != nil || len(res.Chunks) != 1 || res.Chunks[0].Seq != 1 || res.NextSeq != 2 || res.LatestSeq != 1 {
		t.Fatalf("first page = %+v, %v; want seq 1, next 2, latest 1", res, err)
	}

	// Empty-page snapshot: the caller is current at the snapshot's latest.
	empty, err := e.ReplayRead(ctx, rpcapi.ReplayReadParams{Session: sid, Surface: surface, FromSeq: res.NextSeq})
	if err != nil {
		t.Fatal(err)
	}
	if len(empty.Chunks) != 0 || empty.LatestSeq != 1 || empty.NextSeq != 2 {
		t.Fatalf("empty page = %+v; want no chunks, latest 1, next 2", empty)
	}

	// The append immediately after the empty-page snapshot MUST surface on the
	// next page taken from the advertised continuation cursor.
	if _, err := ring.Append([]byte("second")); err != nil {
		t.Fatal(err)
	}
	next, err := e.ReplayRead(ctx, rpcapi.ReplayReadParams{Session: sid, Surface: surface, FromSeq: empty.NextSeq})
	if err != nil {
		t.Fatal(err)
	}
	if len(next.Chunks) != 1 || next.Chunks[0].Seq != 2 {
		t.Fatalf("continuation after post-snapshot append = %+v; the appended seq 2 was skipped", next)
	}
	b, err := base64.StdEncoding.DecodeString(next.Chunks[0].DataB64)
	if err != nil || string(b) != "second" {
		t.Fatalf("continuation payload = %q, %v; want \"second\"", b, err)
	}

	// Ahead-of-latest cursor keeps the pull-back semantics: empty page,
	// next_seq = latest+1 (never the caller's overshoot cursor).
	ahead, err := e.ReplayRead(ctx, rpcapi.ReplayReadParams{Session: sid, Surface: surface, FromSeq: 100})
	if err != nil {
		t.Fatal(err)
	}
	if len(ahead.Chunks) != 0 || ahead.LatestSeq != 2 || ahead.NextSeq != 3 {
		t.Fatalf("ahead-of-latest page = %+v; want no chunks, latest 2, next 3", ahead)
	}
}

// TestEngineReplayReadEmptyPageSnapshotRace pins the F5 atomicity invariant
// under concurrent appends: an empty page means the caller was current AT THE
// SNAPSHOT, so its latest_seq must be below the cursor and next_seq exactly
// latest_seq+1. If page selection and latest_seq came from different ring
// snapshots (the pre-fix Engine), an append landing between them yields an
// empty page whose latest_seq >= cursor — advertising a next_seq one past an
// unseen chunk, i.e. a silent skip.
func TestEngineReplayReadEmptyPageSnapshotRace(t *testing.T) {
	e := newEngine(t)
	ctx := context.Background()
	sid, surface, ring := spawnMetaSurface(t, e)

	const appends = 30000
	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := 0; i < appends; i++ {
			if _, err := ring.Append([]byte{byte(i)}); err != nil {
				t.Error(err)
				return
			}
		}
	}()

	cursor := uint64(1)
	for {
		res, err := e.ReplayRead(ctx, rpcapi.ReplayReadParams{Session: sid, Surface: surface, FromSeq: cursor})
		if err != nil {
			t.Fatalf("ReplayRead(%d): %v", cursor, err)
		}
		if len(res.Chunks) == 0 {
			if res.LatestSeq >= cursor {
				t.Fatalf("empty page at cursor %d advertised latest_seq %d and next_seq %d: sequences [%d..%d] would be silently skipped",
					cursor, res.LatestSeq, res.NextSeq, cursor, res.LatestSeq)
			}
			if res.NextSeq != res.LatestSeq+1 {
				t.Fatalf("empty page next_seq = %d, want latest_seq+1 = %d", res.NextSeq, res.LatestSeq+1)
			}
			select {
			case <-done:
				if cursor == appends+1 {
					return // every appended sequence was observed exactly once
				}
			default:
			}
			continue
		}
		for _, c := range res.Chunks {
			if c.Seq != cursor {
				t.Fatalf("pagination broke sequence truth: got seq %d, want %d", c.Seq, cursor)
			}
			cursor++
		}
		if res.NextSeq != cursor {
			t.Fatalf("page next_seq = %d, want first-not-returned %d", res.NextSeq, cursor)
		}
	}
}

// TestEngineReplayReadConcurrentEvictionPagination stresses monotonic
// contiguous pagination while appends push the retained window through the
// 16 MiB floor: eviction races page selection, gaps must surface as typed
// replay_gap details (a strictly forward jump), empty pages must satisfy the
// snapshot invariant, and pagination must reach the final sequence with no
// silent skip.
func TestEngineReplayReadConcurrentEvictionPagination(t *testing.T) {
	e := newEngine(t)
	ctx := context.Background()
	sid, surface, ring := spawnMetaSurface(t, e)

	const (
		chunkBytes = 256 << 10
		appends    = 96 // 24 MiB through the 16 MiB floor → guaranteed eviction
	)
	payload := bytes.Repeat([]byte{'r'}, chunkBytes)
	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := 0; i < appends; i++ {
			if _, err := ring.Append(payload); err != nil {
				t.Error(err)
				return
			}
		}
	}()

	cursor := uint64(1)
	for {
		res, err := e.ReplayRead(ctx, rpcapi.ReplayReadParams{Session: sid, Surface: surface, FromSeq: cursor, MaxBytes: 1 << 20})
		if err != nil {
			if Code(err) != v1.ErrReplayGap {
				t.Fatalf("ReplayRead(%d): %v", cursor, err)
			}
			var gap rpcapi.ReplayGapDetails
			if uerr := json.Unmarshal(ErrorBody(err).Details, &gap); uerr != nil {
				t.Fatalf("replay_gap details do not decode: %v", uerr)
			}
			if gap.OldestRetained <= cursor {
				t.Fatalf("replay_gap at cursor %d reported non-forward oldest_retained %d", cursor, gap.OldestRetained)
			}
			cursor = gap.OldestRetained // explicit, typed jump — never silent
			continue
		}
		if len(res.Chunks) == 0 {
			if res.LatestSeq >= cursor {
				t.Fatalf("empty page at cursor %d advertised latest_seq %d: silent skip", cursor, res.LatestSeq)
			}
			select {
			case <-done:
				if cursor == appends+1 {
					return // pagination reached the exact final sequence
				}
			default:
			}
			continue
		}
		for _, c := range res.Chunks {
			if c.Seq != cursor {
				t.Fatalf("pagination broke contiguity: got seq %d, want %d", c.Seq, cursor)
			}
			cursor++
		}
	}
}

// TestSpawnAndQuiesceWaitsOutADrainStall is the F6 red→green pin: a fake PTY
// that stalls mid-drain for longer than any polling stability window must not
// be declared quiesced early — after spawnAndQuiesce returns, the replay ring
// holds every output byte. The old helper (three unchanged 20 ms polls of
// LatestSeq) returns during the stall and fails this test deterministically.
func TestSpawnAndQuiesceWaitsOutADrainStall(t *testing.T) {
	payload := replayPayload(1 << 20)
	f := &fakePTY{output: payload, stallAt: 512 << 10, stallFor: 400 * time.Millisecond}
	e := newEnginePTY(t, f)
	ctx := context.Background()
	sid, surface := spawnAndQuiesce(t, e, f)

	var reassembled []byte
	cursor := uint64(1)
	for {
		page, err := e.ReplayRead(ctx, rpcapi.ReplayReadParams{Session: sid, Surface: surface, FromSeq: cursor, MaxBytes: 512 << 10})
		if err != nil {
			t.Fatalf("ReplayRead(%d): %v", cursor, err)
		}
		if len(page.Chunks) == 0 {
			break
		}
		for _, c := range page.Chunks {
			b, err := base64.StdEncoding.DecodeString(c.DataB64)
			if err != nil {
				t.Fatal(err)
			}
			reassembled = append(reassembled, b...)
		}
		cursor = page.NextSeq
	}
	if !bytes.Equal(reassembled, payload) {
		t.Fatalf("after quiesce the ring holds %d bytes, want the full %d-byte payload: the helper declared quiescence mid-drain",
			len(reassembled), len(payload))
	}
}
