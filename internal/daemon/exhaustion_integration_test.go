//go:build integration

package daemon

// Resource-exhaustion acceptance (T6 QA, work package Q5): the blocking
// `integration-resource-exhaustion` check from
// docs/security/readiness-manifest.json:
//
//	go test -count=1 -tags integration -run 'ResourceExhaustion' ./internal/daemon
//
// It drives the EXACT production assembly (Run) with hostile load and asserts
// the bounded-resource contract instead of a crash: a PTY output flood far
// beyond the replay floor keeps the daemon healthy with bounded heap, replay
// reads stay capped at MaxBytes, and a burst of concurrent clients is either
// served or refused with a typed error — never wedged.

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"log/slog"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/amux-run/amux/internal/client"
	"github.com/amux-run/amux/internal/rpcapi"
	"github.com/amux-run/amux/internal/transport/local"
)

func TestResourceExhaustionOutputFloodAndClientBurst(t *testing.T) {
	if testing.Short() {
		t.Skip("floods a real PTY")
	}
	getenv, spec := testEnv(t)
	ready := make(chan struct{})
	runErr := make(chan error, 1)
	go func() {
		runErr <- Run(context.Background(), RunOptions{
			Getenv: getenv,
			Logger: slog.New(slog.DiscardHandler),
			Peers:  fakePeers{uid: uint32(os.Getuid())},
			Ready:  ready,
		})
	}()
	select {
	case <-ready:
	case err := <-runErr:
		t.Fatalf("Run exited before ready: %v", err)
	case <-time.After(30 * time.Second):
		t.Fatal("daemon never became ready")
	}

	ctx := context.Background()
	ctl, err := client.Dial(ctx, local.New(), spec, "exhaustion")
	if err != nil {
		t.Fatal(err)
	}
	defer ctl.Close()

	var created rpcapi.SessionCreateResult
	if err := ctl.Call(ctx, rpcapi.MethodSessionCreate, rpcapi.SessionCreateParams{Name: "flood"}, &created); err != nil {
		t.Fatal(err)
	}
	sid := created.Session.ID
	var ws rpcapi.WorkspaceCreateResult
	if err := ctl.Call(ctx, rpcapi.MethodWorkspaceCreate, rpcapi.WorkspaceCreateParams{Session: sid}, &ws); err != nil {
		t.Fatal(err)
	}

	// Flood: ~48 MiB through the PTY, three times the 16 MiB replay floor.
	var sp rpcapi.SurfaceSpawnResult
	if err := ctl.Call(ctx, rpcapi.MethodSurfaceSpawn, rpcapi.SurfaceSpawnParams{
		Session: sid, Workspace: ws.Workspace, Pane: ws.FirstPane,
		Argv: []string{"/bin/sh", "-c", "yes flood-payload-0123456789abcdef | head -c 50331648; echo FLOOD-DONE"},
		Cwd:  getenv("HOME"), Cols: 120, Rows: 32,
	}, &sp); err != nil {
		t.Fatal(err)
	}

	// Wait for the flood to finish (the exit marker reaches the replay tail).
	floodDone := false
	var lastSeq uint64
	deadline := time.Now().Add(4 * time.Minute)
	for time.Now().Before(deadline) {
		var insp rpcapi.PaneInspectResult
		if err := ctl.Call(ctx, rpcapi.MethodPaneInspect, rpcapi.PaneInspectParams{
			Session: sid, Workspace: ws.Workspace, Pane: ws.FirstPane,
		}, &insp); err != nil {
			t.Fatal(err)
		}
		if insp.LatestSeq == lastSeq && lastSeq > 0 {
			floodDone = true
			break
		}
		lastSeq = insp.LatestSeq
		time.Sleep(2 * time.Second)
	}
	if !floodDone {
		t.Fatal("output flood never quiesced")
	}

	// Daemon memory posture after the flood stays bounded (the ring retains
	// its floor, not the whole flood).
	var dump rpcapi.DiagnosticsDumpResult
	if err := ctl.Call(ctx, rpcapi.MethodDiagnosticsDump, nil, &dump); err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Runtime struct {
			HeapAllocBytes uint64 `json:"heap_alloc_bytes"`
		} `json:"runtime"`
	}
	if err := json.Unmarshal(dump.Dump, &doc); err != nil {
		t.Fatal(err)
	}
	if doc.Runtime.HeapAllocBytes > 512<<20 {
		t.Fatalf("heap after 48 MiB flood = %d bytes — resource exhaustion", doc.Runtime.HeapAllocBytes)
	}

	// Client burst: 100 concurrent dial+health cycles are all served or
	// refused typed — the daemon never wedges.
	var wg sync.WaitGroup
	errs := make(chan error, 100)
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			cctx, cancel := context.WithTimeout(ctx, 30*time.Second)
			defer cancel()
			c, err := client.Dial(cctx, local.New(), spec, "burst")
			if err != nil {
				errs <- err
				return
			}
			defer c.Close()
			errs <- c.Call(cctx, rpcapi.MethodDaemonHealth, nil, &rpcapi.HealthResult{})
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("client burst: %v", err)
		}
	}

	// Bounded replay (flow 14): a MaxBytes-capped read over the flooded
	// surface must return a bounded, framed response on a surviving
	// connection. The flood exceeded the 16 MiB retention floor, so the
	// cursor-1 read is expected to hit the typed replay_gap boundary — whose
	// STRUCTURED details (never the human message) carry the oldest retained
	// sequence automation continues from.
	rc, err := client.Dial(ctx, local.New(), spec, "replay-bound")
	if err != nil {
		t.Fatal(err)
	}
	defer rc.Close()
	var oldest uint64 = 1
	var res rpcapi.ReplayReadResult
	err = rc.Call(ctx, rpcapi.MethodReplayRead, rpcapi.ReplayReadParams{
		Session: sid, Surface: sp.Surface, FromSeq: oldest, MaxBytes: 1 << 20,
	}, &res)
	var ce *client.Error
	if errors.As(err, &ce) && ce.Code == "replay_gap" {
		var gap rpcapi.ReplayGapDetails
		if len(ce.Details) == 0 {
			t.Fatalf("replay_gap carries no structured details: %+v", ce)
		}
		if derr := json.Unmarshal(ce.Details, &gap); derr != nil {
			t.Fatalf("replay_gap details do not decode: %v (%s)", derr, ce.Details)
		}
		if gap.OldestRetained <= 1 || gap.LatestSeq < gap.OldestRetained {
			t.Fatalf("replay_gap details boundary is not actionable: %+v", gap)
		}
		oldest = gap.OldestRetained
		err = rc.Call(ctx, rpcapi.MethodReplayRead, rpcapi.ReplayReadParams{
			Session: sid, Surface: sp.Surface, FromSeq: oldest, MaxBytes: 1 << 20,
		}, &res)
	}
	if err != nil {
		t.Fatalf("bounded replay.read (MaxBytes=1MiB) over a flooded surface failed: %v — the response must stay under v1.MaxHeaderBytes and keep the connection alive (CLI flow 14 over a flooded surface)", err)
	}
	if len(res.Chunks) == 0 || res.Chunks[0].Seq != oldest {
		t.Fatalf("replay page must start exactly at the requested cursor %d (got %d chunks, first seq %d)",
			oldest, len(res.Chunks), res.Chunks[0].Seq)
	}
	var got int64
	for _, c := range res.Chunks {
		b, _ := base64.StdEncoding.DecodeString(c.DataB64)
		got += int64(len(b))
	}
	if got > 1<<20 {
		t.Fatalf("replay.read returned %d decoded bytes despite MaxBytes=%d", got, 1<<20)
	}

	// Continuation is contiguous and duplicate-free: page the rest of the
	// retained window from next_seq and require unbroken sequence truth.
	wantSeq := res.NextSeq
	pages := 0
	for {
		var page rpcapi.ReplayReadResult
		if err := rc.Call(ctx, rpcapi.MethodReplayRead, rpcapi.ReplayReadParams{
			Session: sid, Surface: sp.Surface, FromSeq: wantSeq, MaxBytes: 1 << 20,
		}, &page); err != nil {
			t.Fatalf("continuation from %d: %v", wantSeq, err)
		}
		if len(page.Chunks) == 0 {
			break
		}
		for _, c := range page.Chunks {
			if c.Seq != wantSeq {
				t.Fatalf("continuation broke sequence truth: got seq %d, want %d", c.Seq, wantSeq)
			}
			wantSeq++
		}
		pages++
	}
	if pages == 0 {
		t.Fatal("a 16 MiB retained window must take more than one 1 MiB page")
	}

	// A bound below the next whole chunk fails typed (chunks are never split),
	// and the connection stays healthy for further calls.
	err = rc.Call(ctx, rpcapi.MethodReplayRead, rpcapi.ReplayReadParams{
		Session: sid, Surface: sp.Surface, FromSeq: oldest, MaxBytes: 1,
	}, &rpcapi.ReplayReadResult{})
	if client.CodeOf(err) != "invalid_argument" {
		t.Fatalf("MaxBytes=1: got %v (code %q), want typed invalid_argument", err, client.CodeOf(err))
	}
	if err := rc.Call(ctx, rpcapi.MethodDaemonHealth, nil, &rpcapi.HealthResult{}); err != nil {
		t.Fatalf("replay-bound connection unhealthy after bounded paging: %v", err)
	}

	var down rpcapi.ShutdownResult
	if err := ctl.Call(ctx, rpcapi.MethodDaemonShutdown, nil, &down); err != nil || !down.Accepted {
		t.Fatalf("shutdown after exhaustion run: %v %+v", err, down)
	}
	if err := <-runErr; err != nil {
		t.Fatalf("daemon.Run returned error: %v", err)
	}
}
