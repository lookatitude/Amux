package daemon

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	v1 "github.com/amux-run/amux/api/v1"

	"github.com/amux-run/amux/internal/client"
	"github.com/amux-run/amux/internal/control"
	"github.com/amux-run/amux/internal/notify"
	"github.com/amux-run/amux/internal/observability"
	"github.com/amux-run/amux/internal/platform"
	"github.com/amux-run/amux/internal/redact"
	"github.com/amux-run/amux/internal/rpcapi"
	"github.com/amux-run/amux/internal/store"
	"github.com/amux-run/amux/internal/transport/local"
)

// fakePeers satisfies the mandatory SO_PEERCRED seam off Linux (STR-2's
// production implementation is Linux-only; tests inject the owner UID).
type fakePeers struct{ uid uint32 }

func (p fakePeers) PeerUID(uintptr) (uint32, error) { return p.uid, nil }

// stayOpenPTY emits one payload then blocks until closed, so a surface stays
// live for attach/lease tests instead of exiting after its output.
type stayOpenPTY struct{ output []byte }

func (f stayOpenPTY) Start(spec platform.PTYSpec) (platform.PTYHandle, error) {
	return &stayOpenHandle{fakeHandle: fakeHandle{out: f.output, exit: make(chan struct{})}}, nil
}

type stayOpenHandle struct{ fakeHandle }

func (h *stayOpenHandle) Read(p []byte) (int, error) {
	h.mu.Lock()
	if h.off < len(h.out) {
		n := copy(p, h.out[h.off:])
		h.off += n
		h.mu.Unlock()
		return n, nil
	}
	closed := h.closed
	h.mu.Unlock()
	if closed {
		return 0, io.EOF
	}
	<-h.exit
	return 0, io.EOF
}

func (h *stayOpenHandle) Close() error {
	h.mu.Lock()
	if !h.closed {
		h.closed = true
		close(h.exit)
	}
	h.mu.Unlock()
	return nil
}

// Wait blocks until the handle is closed/signaled, so the fake process is
// genuinely ALIVE (owned by the supervisor) for the whole test — the
// precondition the in-daemon live-reconcile assertion needs.
func (h *stayOpenHandle) Wait() (platform.PTYExit, error) {
	<-h.exit
	return platform.PTYExit{Code: 0}, nil
}

func (h *stayOpenHandle) Signal(os.Signal) error { return h.Close() }

// testSpec builds a short socket path (darwin sun_path limit) with the /var
// symlink resolved so the transport's path-chain validation passes.
func testSpec(t *testing.T) platform.TransportSpec {
	t.Helper()
	base, err := os.MkdirTemp("", "amux")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(base) })
	canon, err := filepath.EvalSymlinks(base)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(canon, "run"), 0o700); err != nil {
		t.Fatal(err)
	}
	return platform.TransportSpec{
		SocketPath: filepath.Join(canon, "run", "amuxd.sock"),
		OwnerUID:   uint32(os.Getuid()),
	}
}

// daemonHarness is one full in-process daemon (engine + control + SQLite store
// + notifications + protocol server on a real socket) plus a connected shared
// client — the exact production assembly with only the PTY and peer seams
// injected.
type daemonHarness struct {
	cli      *client.Client
	engine   *Engine
	control  *control.Actor
	notify   *notify.Service
	store    *store.Store
	spec     platform.TransportSpec
	shutdown chan struct{}
}

// startDaemon boots the full in-process daemon. Optional mutators adjust the
// engine Deps before construction (e.g. injecting fake B10 context collectors
// for the pane.context projection tests).
func startDaemon(t *testing.T, mutate ...func(*Deps)) *daemonHarness {
	t.Helper()
	clock := platform.NewSystemClock()
	st, err := store.Open(filepath.Join(t.TempDir(), "db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })

	ctrl := control.New(control.Deps{Clock: clock, Store: NewTrustStore(st)})
	ctrl.Start()
	t.Cleanup(ctrl.Stop)

	svc, err := notify.NewService(st, notify.NopNotifier{}, clock, redact.New().Redact, slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatal(err)
	}

	deps := Deps{
		Control:     ctrl,
		Clock:       clock,
		PTY:         func() platform.PTY { return stayOpenPTY{output: []byte("wire-payload\r\n")} },
		Store:       st,
		SnapshotDir: t.TempDir(),
	}
	for _, m := range mutate {
		m(&deps)
	}
	engine, err := New(deps)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(engine.Close)

	shutdown := make(chan struct{})
	srv, err := NewServer(ServerConfig{
		Engine:     engine,
		Control:    ctrl,
		Store:      st,
		Notify:     svc,
		Metrics:    observability.NewRegistry(),
		BootID:     "boot-server-test",
		Peers:      fakePeers{uid: uint32(os.Getuid())},
		Clock:      clock,
		Log:        slog.New(slog.DiscardHandler),
		OnShutdown: func() { close(shutdown) },
	})
	if err != nil {
		t.Fatal(err)
	}

	spec := testSpec(t)
	ln, err := local.New().Listen(spec)
	if err != nil {
		t.Fatal(err)
	}
	serveDone := make(chan error, 1)
	go func() { serveDone <- srv.Serve(ln) }()
	t.Cleanup(func() {
		srv.Close()
		<-serveDone
	})

	cli, err := client.Dial(context.Background(), local.New(), spec, "server-test")
	if err != nil {
		t.Fatalf("client dial: %v", err)
	}
	t.Cleanup(func() { cli.Close() })
	return &daemonHarness{cli: cli, engine: engine, control: ctrl, notify: svc, store: st, spec: spec, shutdown: shutdown}
}

// call is a strict-typed unary helper failing the test on error.
func call[R any](t *testing.T, h *daemonHarness, method string, params any) R {
	t.Helper()
	var out R
	if err := h.cli.Call(context.Background(), method, params, &out); err != nil {
		t.Fatalf("%s: %v", method, err)
	}
	return out
}

// TestServerUnaryFlows drives the graph, surface, replay, snapshot, and
// diagnostics families end to end over the real wire: strict param decoding,
// typed errors, and revision-bearing results all pass through the exact
// production dispatch path.
func TestServerUnaryFlows(t *testing.T) {
	h := startDaemon(t)

	health := call[rpcapi.HealthResult](t, h, rpcapi.MethodDaemonHealth, nil)
	if health.BootID != "boot-server-test" || health.Protocol == "" {
		t.Fatalf("health = %+v", health)
	}

	created := call[rpcapi.SessionCreateResult](t, h, rpcapi.MethodSessionCreate, rpcapi.SessionCreateParams{Name: "wire"})
	sid := created.Session.ID
	list := call[rpcapi.SessionListResult](t, h, rpcapi.MethodSessionList, nil)
	if len(list.Sessions) != 1 || list.Sessions[0].ID != sid {
		t.Fatalf("session.list = %+v", list)
	}

	ws := call[rpcapi.WorkspaceCreateResult](t, h, rpcapi.MethodWorkspaceCreate, rpcapi.WorkspaceCreateParams{Session: sid, Name: "main"})
	sp := call[rpcapi.PaneSplitResult](t, h, rpcapi.MethodPaneSplit, rpcapi.PaneSplitParams{
		Session: sid, Workspace: ws.Workspace, Target: ws.FirstPane, Orientation: rpcapi.OrientVertical,
	})
	if sp.NewPane == "" || sp.Rev <= ws.Rev {
		t.Fatalf("pane.split = %+v", sp)
	}
	call[rpcapi.RevResult](t, h, rpcapi.MethodPaneFocus, rpcapi.PaneFocusParams{Session: sid, Workspace: ws.Workspace, Pane: sp.NewPane})
	call[rpcapi.RevResult](t, h, rpcapi.MethodPaneResize, rpcapi.PaneResizeParams{Session: sid, Workspace: ws.Workspace, Pane: sp.NewPane, Ratio: 0.3})

	spawned := call[rpcapi.SurfaceSpawnResult](t, h, rpcapi.MethodSurfaceSpawn, rpcapi.SurfaceSpawnParams{
		Session: sid, Workspace: ws.Workspace, Pane: ws.FirstPane, Argv: []string{"/bin/cat"}, Cwd: t.TempDir(),
	})

	// Raw output must become readable bounded replay over the wire.
	deadline := time.Now().Add(2 * time.Second)
	var replay rpcapi.ReplayReadResult
	for time.Now().Before(deadline) {
		replay = call[rpcapi.ReplayReadResult](t, h, rpcapi.MethodReplayRead, rpcapi.ReplayReadParams{Session: sid, Surface: spawned.Surface, FromSeq: 1})
		if len(replay.Chunks) > 0 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if len(replay.Chunks) == 0 {
		t.Fatal("replay.read returned no chunks")
	}
	raw, _ := base64.StdEncoding.DecodeString(replay.Chunks[0].DataB64)
	if !strings.Contains(string(raw), "wire-payload") {
		t.Fatalf("replay data = %q", raw)
	}

	// Lease-gated input: missing lease_id fails closed; a held lease writes.
	var inputErr error = h.cli.Call(context.Background(), rpcapi.MethodInputSend, rpcapi.InputSendParams{
		Session: sid, Surface: spawned.Surface, DataB64: base64.StdEncoding.EncodeToString([]byte("x")),
	}, nil)
	if client.CodeOf(inputErr) != v1.ErrInvalidArgument {
		t.Fatalf("input without lease_id must fail closed, got %v", inputErr)
	}
	sent := call[rpcapi.InputSendResult](t, h, rpcapi.MethodInputSend, rpcapi.InputSendParams{
		Session: sid, Surface: spawned.Surface, LeaseID: "cli-1",
		DataB64: base64.StdEncoding.EncodeToString([]byte("echo hi\n")),
	})
	if sent.Bytes != len("echo hi\n") {
		t.Fatalf("input.send = %+v", sent)
	}
	// A second writer identity is rejected while the lease is held.
	err := h.cli.Call(context.Background(), rpcapi.MethodInputSend, rpcapi.InputSendParams{
		Session: sid, Surface: spawned.Surface, LeaseID: "cli-2",
		DataB64: base64.StdEncoding.EncodeToString([]byte("y")),
	}, nil)
	if client.CodeOf(err) != v1.ErrConflict {
		t.Fatalf("second acquirer must be rejected with conflict (lease held), got %v", err)
	}
	// Takeover without confirmation fails closed; confirmed takeover displaces
	// the holder, whose next write is then rejected.
	err = h.cli.Call(context.Background(), rpcapi.MethodInputSend, rpcapi.InputSendParams{
		Session: sid, Surface: spawned.Surface, LeaseID: "cli-2", Takeover: true,
		DataB64: base64.StdEncoding.EncodeToString([]byte("y")),
	}, nil)
	if client.CodeOf(err) != v1.ErrInvalidArgument {
		t.Fatalf("unconfirmed takeover must fail closed, got %v", err)
	}
	call[rpcapi.InputSendResult](t, h, rpcapi.MethodInputSend, rpcapi.InputSendParams{
		Session: sid, Surface: spawned.Surface, LeaseID: "cli-2", Takeover: true, Confirm: true,
		DataB64: base64.StdEncoding.EncodeToString([]byte("y")),
	})
	err = h.cli.Call(context.Background(), rpcapi.MethodInputSend, rpcapi.InputSendParams{
		Session: sid, Surface: spawned.Surface, LeaseID: "cli-1",
		DataB64: base64.StdEncoding.EncodeToString([]byte("z")),
	}, nil)
	if client.CodeOf(err) != v1.ErrConflict {
		t.Fatalf("displaced holder must be rejected, got %v", err)
	}
	// Explicit release frees the lease for a fresh acquire.
	call[struct{}](t, h, rpcapi.MethodInputRelease, rpcapi.InputReleaseParams{Session: sid, Surface: spawned.Surface, LeaseID: "cli-2"})
	call[rpcapi.InputSendResult](t, h, rpcapi.MethodInputSend, rpcapi.InputSendParams{
		Session: sid, Surface: spawned.Surface, LeaseID: "cli-1",
		DataB64: base64.StdEncoding.EncodeToString([]byte("z")),
	})

	insp := call[rpcapi.PaneInspectResult](t, h, rpcapi.MethodPaneInspect, rpcapi.PaneInspectParams{Session: sid, Workspace: ws.Workspace, Pane: ws.FirstPane})
	if insp.LatestSeq == 0 || len(insp.Surfaces) < 2 {
		t.Fatalf("pane.inspect = %+v", insp)
	}

	saved := call[rpcapi.SnapshotSaveResult](t, h, rpcapi.MethodSnapshotSave, rpcapi.SnapshotSaveParams{Session: sid})
	if saved.CheckpointID == "" {
		t.Fatalf("snapshot.save = %+v", saved)
	}
	// In-daemon restore of the checkpoint this daemon just saved, while it
	// still owns the same live PTY identity: the surface reconciles LIVE over
	// the wire (ADR-0005 precedence rule 2), with the reason always present.
	restored := call[rpcapi.SnapshotRestoreResult](t, h, rpcapi.MethodSnapshotRestore, rpcapi.SnapshotRestoreParams{Session: sid})
	for _, s := range restored.Surfaces {
		if s.Reason == "" {
			t.Fatalf("wire restore left %s without a reason", s.Surface)
		}
		if s.Surface == spawned.Surface && s.Class != "live" {
			t.Fatalf("in-daemon wire restore classified still-owned %s as %q, want live", s.Surface, s.Class)
		}
	}

	// Stop requires confirmation over the wire (fail-closed matrix).
	err = h.cli.Call(context.Background(), rpcapi.MethodSurfaceStop, rpcapi.SurfaceStopParams{
		Session: sid, Workspace: ws.Workspace, Pane: ws.FirstPane, Surface: spawned.Surface,
	}, nil)
	if client.CodeOf(err) != v1.ErrInvalidArgument {
		t.Fatalf("unconfirmed surface.stop must fail closed, got %v", err)
	}

	dump := call[rpcapi.DiagnosticsDumpResult](t, h, rpcapi.MethodDiagnosticsDump, nil)
	var doc map[string]any
	if err := json.Unmarshal(dump.Dump, &doc); err != nil {
		t.Fatalf("diagnostics dump is not one JSON object: %v", err)
	}

	// Unknown params fields are contract violations end to end (strict decode).
	err = h.cli.Call(context.Background(), rpcapi.MethodSessionCreate, map[string]any{"name": "x", "bogus": true}, nil)
	if client.CodeOf(err) != v1.ErrInvalidArgument {
		t.Fatalf("unknown param field must be invalid_argument, got %v", err)
	}
}

// TestServerHookFamily drives project trust over the wire: unconfirmed
// approve/revoke fail closed, confirmed transitions bump the SQLite-backed
// epoch, and hook.list reflects grant state per project.
func TestServerHookFamily(t *testing.T) {
	h := startDaemon(t)
	project := t.TempDir()

	err := h.cli.Call(context.Background(), rpcapi.MethodHookApprove, rpcapi.HookApproveParams{Project: project}, nil)
	if client.CodeOf(err) != v1.ErrInvalidArgument {
		t.Fatalf("unconfirmed hook.approve must fail closed, got %v", err)
	}
	approved := call[rpcapi.EpochResult](t, h, rpcapi.MethodHookApprove, rpcapi.HookApproveParams{Project: project, Confirm: true})
	if approved.Epoch == 0 {
		t.Fatalf("approve epoch = %+v", approved)
	}
	grants := call[rpcapi.HookListResult](t, h, rpcapi.MethodHookList, rpcapi.HookListParams{Project: project})
	if grants.Grants == nil {
		t.Fatal("hook.list returned no list")
	}

	err = h.cli.Call(context.Background(), rpcapi.MethodHookRevoke, rpcapi.HookRevokeParams{Project: project}, nil)
	if client.CodeOf(err) != v1.ErrInvalidArgument {
		t.Fatalf("unconfirmed hook.revoke must fail closed, got %v", err)
	}
	revoked := call[rpcapi.EpochResult](t, h, rpcapi.MethodHookRevoke, rpcapi.HookRevokeParams{Project: project, Confirm: true})
	if revoked.Epoch <= approved.Epoch {
		t.Fatalf("revoke epoch %d must exceed approve epoch %d", revoked.Epoch, approved.Epoch)
	}
	call[struct{}](t, h, rpcapi.MethodHookDeny, rpcapi.HookDenyParams{Project: project})
}

// TestServerNotificationFamily lists and reads daemon-owned notifications over
// the wire, including unread accounting.
func TestServerNotificationFamily(t *testing.T) {
	h := startDaemon(t)
	created := call[rpcapi.SessionCreateResult](t, h, rpcapi.MethodSessionCreate, rpcapi.SessionCreateParams{Name: "n"})
	sid := created.Session.ID

	row, err := h.notify.Publish(context.Background(), notify.Input{Session: sid, Kind: "info", Title: "t1", Body: "b1"})
	if err != nil {
		t.Fatal(err)
	}
	list := call[rpcapi.NotificationListResult](t, h, rpcapi.MethodNotificationList, rpcapi.NotificationListParams{Session: sid})
	if len(list.Notifications) != 1 || list.Unread != 1 || list.Notifications[0].Read {
		t.Fatalf("notification.list = %+v", list)
	}
	read := call[rpcapi.NotificationReadResult](t, h, rpcapi.MethodNotificationRead, rpcapi.NotificationReadParams{ID: row.ID})
	if !read.Read {
		t.Fatalf("notification.read = %+v", read)
	}
	list = call[rpcapi.NotificationListResult](t, h, rpcapi.MethodNotificationList, rpcapi.NotificationListParams{Session: sid, UnreadOnly: true})
	if len(list.Notifications) != 0 || list.Unread != 0 {
		t.Fatalf("unread-only after read = %+v", list)
	}

	// Snapshot round trip: the committed notification export is the ONLY thing
	// restore imports — a post-checkpoint notification disappears, the
	// checkpointed one returns with its read state (ADR-0005).
	call[rpcapi.SnapshotSaveResult](t, h, rpcapi.MethodSnapshotSave, rpcapi.SnapshotSaveParams{Session: sid})
	if _, err := h.notify.Publish(context.Background(), notify.Input{Session: sid, Kind: "info", Title: "t2", Body: "post-checkpoint"}); err != nil {
		t.Fatal(err)
	}
	call[rpcapi.SnapshotRestoreResult](t, h, rpcapi.MethodSnapshotRestore, rpcapi.SnapshotRestoreParams{Session: sid})
	list = call[rpcapi.NotificationListResult](t, h, rpcapi.MethodNotificationList, rpcapi.NotificationListParams{Session: sid})
	if len(list.Notifications) != 1 || list.Notifications[0].ID != row.ID || !list.Notifications[0].Read {
		t.Fatalf("restore did not re-import the committed notification export: %+v", list)
	}
}

// TestServerAttachStream drives flow 12 over the wire: the attach_snapshot
// header frame arrives first (pane meta + cutover sequence), then replayed raw
// output as raw-body frames — replay ending at the cutover, live strictly
// after (ADR-0004).
func TestServerAttachStream(t *testing.T) {
	h := startDaemon(t)
	ctx := context.Background()
	created := call[rpcapi.SessionCreateResult](t, h, rpcapi.MethodSessionCreate, nil)
	sid := created.Session.ID
	ws := call[rpcapi.WorkspaceCreateResult](t, h, rpcapi.MethodWorkspaceCreate, rpcapi.WorkspaceCreateParams{Session: sid})
	spawned := call[rpcapi.SurfaceSpawnResult](t, h, rpcapi.MethodSurfaceSpawn, rpcapi.SurfaceSpawnParams{
		Session: sid, Workspace: ws.Workspace, Pane: ws.FirstPane, Argv: []string{"/bin/cat"}, Cwd: t.TempDir(),
	})

	// Wait for output to land so FromSeq=1 exercises the replay path.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		r := call[rpcapi.ReplayReadResult](t, h, rpcapi.MethodReplayRead, rpcapi.ReplayReadParams{Session: sid, Surface: spawned.Surface, FromSeq: 1})
		if len(r.Chunks) > 0 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	// The Client multiplexes one stream; open a second client for the stream.
	streamCli, err := client.Dial(ctx, local.New(), h.spec, "attach-test")
	if err != nil {
		t.Fatal(err)
	}
	defer streamCli.Close()
	sctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	st, err := streamCli.Stream(sctx, rpcapi.MethodAttach, rpcapi.AttachParams{Session: sid, Surface: spawned.Surface, FromSeq: 1})
	if err != nil {
		t.Fatal(err)
	}

	ev, _, err := st.Recv()
	if err != nil {
		t.Fatalf("first attach frame: %v", err)
	}
	if ev.Event != "attach_snapshot" {
		t.Fatalf("first frame = %+v, want attach_snapshot", ev)
	}
	var head struct {
		UpToSeq uint64 `json:"up_to_seq"`
		Rows    int    `json:"rows"`
		Cols    int    `json:"cols"`
	}
	if err := json.Unmarshal(ev.Payload, &head); err != nil {
		t.Fatal(err)
	}
	if head.Rows == 0 || head.Cols == 0 {
		t.Fatalf("attach_snapshot payload = %s", ev.Payload)
	}

	var got []byte
	for len(got) == 0 || !strings.Contains(string(got), "wire-payload") {
		ev, body, err := st.Recv()
		if err != nil {
			t.Fatalf("attach stream ended early (%v) with %q", err, got)
		}
		if ev.Event == "raw_output" {
			got = append(got, body...)
		}
	}
}

// TestServerEventStream drives flow 20 over the wire: committed graph events
// arrive on a live subscription in order with contiguous sequences.
func TestServerEventStream(t *testing.T) {
	h := startDaemon(t)
	ctx := context.Background()
	created := call[rpcapi.SessionCreateResult](t, h, rpcapi.MethodSessionCreate, nil)
	sid := created.Session.ID
	ws := call[rpcapi.WorkspaceCreateResult](t, h, rpcapi.MethodWorkspaceCreate, rpcapi.WorkspaceCreateParams{Session: sid})

	streamCli, err := client.Dial(ctx, local.New(), h.spec, "event-test")
	if err != nil {
		t.Fatal(err)
	}
	defer streamCli.Close()
	sctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	st, err := streamCli.Stream(sctx, rpcapi.MethodEventSubscribe, rpcapi.EventSubscribeParams{Session: sid, FromSeq: 1})
	if err != nil {
		t.Fatal(err)
	}

	call[rpcapi.PaneSplitResult](t, h, rpcapi.MethodPaneSplit, rpcapi.PaneSplitParams{
		Session: sid, Workspace: ws.Workspace, Target: ws.FirstPane, Orientation: rpcapi.OrientHorizontal,
	})

	var lastSeq uint64
	sawSplit := false
	for !sawSplit {
		ev, _, err := st.Recv()
		if err != nil {
			t.Fatalf("event stream ended early: %v", err)
		}
		if ev.Event == "heartbeat" {
			continue
		}
		if lastSeq != 0 && ev.Seq != lastSeq+1 {
			t.Fatalf("event gap: %d -> %d", lastSeq, ev.Seq)
		}
		lastSeq = ev.Seq
		if ev.Event == "pane_split" {
			sawSplit = true
		}
	}
}

// TestServerShutdown verifies a client-requested clean shutdown fires the
// OnShutdown hook exactly once and acknowledges first.
func TestServerShutdown(t *testing.T) {
	h := startDaemon(t)
	res := call[rpcapi.ShutdownResult](t, h, rpcapi.MethodDaemonShutdown, nil)
	if !res.Accepted {
		t.Fatalf("shutdown = %+v", res)
	}
	select {
	case <-h.shutdown:
	case <-time.After(2 * time.Second):
		t.Fatal("OnShutdown never fired")
	}
}

// TestServerReplayReadBoundedOverWire pins flow 14 at the protocol layer: a
// replay.read over a surface retaining far more than one page must ANSWER on
// the same connection (the encoded response stays inside the frame header
// cap), page contiguously to completion, and reject a bound below the next
// whole chunk with a typed error — the connection stays healthy throughout.
func TestServerReplayReadBoundedOverWire(t *testing.T) {
	payload := replayPayload(2 << 20) // 2 MiB retained >> one bounded page
	h := startDaemon(t, func(d *Deps) {
		d.PTY = func() platform.PTY { return stayOpenPTY{output: payload} }
	})

	created := call[rpcapi.SessionCreateResult](t, h, rpcapi.MethodSessionCreate, rpcapi.SessionCreateParams{Name: "replay-wire"})
	ws := call[rpcapi.WorkspaceCreateResult](t, h, rpcapi.MethodWorkspaceCreate, rpcapi.WorkspaceCreateParams{Session: created.Session.ID})
	sp := call[rpcapi.SurfaceSpawnResult](t, h, rpcapi.MethodSurfaceSpawn, rpcapi.SurfaceSpawnParams{
		Session: created.Session.ID, Workspace: ws.Workspace, Pane: ws.FirstPane,
		Argv: []string{"/bin/sh"}, Cwd: t.TempDir(),
	})

	// Wait for the full payload to land in the ring.
	var last uint64
	stable := 0
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) && stable < 3 {
		insp := call[rpcapi.PaneInspectResult](t, h, rpcapi.MethodPaneInspect, rpcapi.PaneInspectParams{
			Session: created.Session.ID, Workspace: ws.Workspace, Pane: ws.FirstPane,
		})
		if insp.LatestSeq > 0 && insp.LatestSeq == last {
			stable++
		} else {
			stable = 0
		}
		last = insp.LatestSeq
		time.Sleep(20 * time.Millisecond)
	}
	if stable < 3 {
		t.Fatal("payload never quiesced into the replay ring")
	}

	// Default bound over the real wire: the daemon must answer, not sever.
	var reassembled []byte
	var wantSeq uint64 = 1
	cursor := uint64(1)
	for {
		var page rpcapi.ReplayReadResult
		if err := h.cli.Call(context.Background(), rpcapi.MethodReplayRead, rpcapi.ReplayReadParams{
			Session: created.Session.ID, Surface: sp.Surface, FromSeq: cursor,
		}, &page); err != nil {
			t.Fatalf("replay.read from %d over the wire: %v", cursor, err)
		}
		if len(page.Chunks) == 0 {
			break
		}
		var pageBytes int64
		for _, c := range page.Chunks {
			if c.Seq != wantSeq {
				t.Fatalf("wire continuation broke sequence truth: got %d, want %d", c.Seq, wantSeq)
			}
			wantSeq++
			b, err := base64.StdEncoding.DecodeString(c.DataB64)
			if err != nil {
				t.Fatal(err)
			}
			pageBytes += int64(len(b))
			reassembled = append(reassembled, b...)
		}
		if pageBytes > replayReadMaxBytes {
			t.Fatalf("wire page decoded %d bytes > server cap %d", pageBytes, replayReadMaxBytes)
		}
		cursor = page.NextSeq
	}
	if len(reassembled) != len(payload) {
		t.Fatalf("wire paging reassembled %d bytes, want %d", len(reassembled), len(payload))
	}

	// A bound below the next whole chunk fails typed over the wire — and the
	// connection survives to serve the next call.
	err := h.cli.Call(context.Background(), rpcapi.MethodReplayRead, rpcapi.ReplayReadParams{
		Session: created.Session.ID, Surface: sp.Surface, FromSeq: 1, MaxBytes: 1,
	}, &rpcapi.ReplayReadResult{})
	if client.CodeOf(err) != v1.ErrInvalidArgument {
		t.Fatalf("max_bytes=1 over the wire: got %v (code %q), want invalid_argument", err, client.CodeOf(err))
	}
	if err := h.cli.Call(context.Background(), rpcapi.MethodDaemonHealth, nil, &rpcapi.HealthResult{}); err != nil {
		t.Fatalf("connection unhealthy after bounded replay traffic: %v", err)
	}
}
