//go:build soak

// soak_test.go is the T6 QA soak workload (plan work package Q7). It boots the
// EXACT production daemon assembly in-process (daemon.Run: XDG paths, config,
// SQLite store, engine, owner-only socket — only the Linux-only peer-credential
// seam is injected off Linux, mirroring cmd/amux/e2e_test.go), spawns the
// spec-mandated concurrent PTY population, and drives a seeded stimulus mix of
// input, resize, focus, split/close, attach/detach, stop/restart, and snapshot
// operations for the requested duration while a dedicated subscriber asserts
// event contiguity.
//
// Pass conditions (spec "Performance and reliability", plan T6 criteria):
//   - no daemon crash (daemon.Run must not return until shutdown, then nil),
//   - no unrecovered event gap (contiguous seq; a typed event_gap terminal is
//     recovered via snapshot refresh + resubscribe and counted),
//   - zero orphan processes after teardown,
//   - no unbounded goroutine / file-descriptor / heap / child-count trend
//     (first-quartile median vs last-quartile median with explicit slack).
//
// Determinism: all stimulus randomness derives from -soak.seed; PTY workloads
// are fixed /bin/sh generator loops and /bin/cat echo surfaces. Metrics are
// sampled from the daemon's diagnostics.dump plus OS-level fd/descendant
// scans and written as JSONL for the harness's trend evidence.
package soak

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"math/rand"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"runtime/pprof"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/amux-run/amux/internal/client"
	"github.com/amux-run/amux/internal/config"
	"github.com/amux-run/amux/internal/daemon"
	"github.com/amux-run/amux/internal/platform"
	"github.com/amux-run/amux/internal/rpcapi"
	"github.com/amux-run/amux/internal/transport/local"
)

var (
	soakDuration = flag.Duration("soak.duration", 90*time.Second, "total stimulus duration")
	soakSeed     = flag.Int64("soak.seed", 1, "deterministic stimulus seed")
	soakPTYs     = flag.Int("soak.ptys", 20, "concurrent PTY population")
	soakPprof    = flag.String("soak.pprof", "", "directory for start/end heap+goroutine profiles")
	soakMetrics  = flag.String("soak.metrics", "", "JSONL metrics output path")
)

// selfPeers injects the owner UID where the production SO_PEERCRED seam is
// Linux-only (ADR-0006). On Linux the production seam is used unmodified.
type selfPeers struct{}

func (selfPeers) PeerUID(uintptr) (uint32, error) { return uint32(os.Getuid()), nil }

// sample is one metrics observation (also the JSONL line shape).
type sample struct {
	TMS        int64  `json:"t_ms"`
	Goroutines int    `json:"goroutines"`
	HeapAlloc  uint64 `json:"heap_alloc_bytes"`
	FDs        int    `json:"fds"`
	Children   int    `json:"children"`
	Events     uint64 `json:"events_total"`
	Gaps       uint64 `json:"gaps_recovered"`
	Ops        uint64 `json:"ops_total"`
}

// paneRef is one spawned PTY surface under stimulus.
type paneRef struct {
	workspace string
	pane      string
	surface   string
	echo      bool // /bin/cat surface accepting input pings
}

func TestSoak(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("soak workload targets Unix PTYs")
	}
	base := t.TempDir()
	canon, err := filepath.EvalSymlinks(base)
	if err != nil {
		t.Fatal(err)
	}
	env := map[string]string{
		"HOME":            canon,
		"XDG_RUNTIME_DIR": filepath.Join(canon, "run"),
	}
	if err := os.MkdirAll(env["XDG_RUNTIME_DIR"], 0o700); err != nil {
		t.Fatal(err)
	}
	getenv := func(k string) string { return env[k] }
	paths, err := config.Resolve(getenv)
	if err != nil {
		t.Fatal(err)
	}
	spec := platform.TransportSpec{SocketPath: paths.SocketPath(), OwnerUID: uint32(os.Getuid())}

	// --- boot the production assembly in-process ---------------------------
	var peers platform.PeerCredentials
	if runtime.GOOS != "linux" {
		peers = selfPeers{}
	}
	ready := make(chan struct{})
	runErr := make(chan error, 1)
	daemonCtx, stopDaemon := context.WithCancel(context.Background())
	defer stopDaemon()
	go func() {
		runErr <- daemon.Run(daemonCtx, daemon.RunOptions{
			Getenv: getenv,
			Logger: slog.New(slog.DiscardHandler),
			Peers:  peers,
			Ready:  ready,
		})
	}()
	select {
	case <-ready:
	case err := <-runErr:
		t.Fatalf("daemon exited before ready: %v", err)
	case <-time.After(30 * time.Second):
		t.Fatal("daemon never became ready")
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ctl, err := client.Dial(ctx, local.New(), spec, "soak-driver")
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer ctl.Close()

	writePprof(t, "start")

	// --- build the graph: one session, PTYs spread over workspaces ----------
	var created rpcapi.SessionCreateResult
	mustCall(t, ctl, rpcapi.MethodSessionCreate, rpcapi.SessionCreateParams{Name: "soak"}, &created)
	sid := created.Session.ID

	ptys := *soakPTYs
	if ptys < 1 {
		ptys = 1
	}
	perWS := 5
	rng := rand.New(rand.NewSource(*soakSeed))
	var panes []paneRef
	for len(panes) < ptys {
		var ws rpcapi.WorkspaceCreateResult
		mustCall(t, ctl, rpcapi.MethodWorkspaceCreate, rpcapi.WorkspaceCreateParams{
			Session: sid, Name: fmt.Sprintf("soak-%d", len(panes)/perWS), FirstPaneCwd: canon,
		}, &ws)
		wsPanes := []string{ws.FirstPane}
		for len(wsPanes) < perWS && len(panes)+len(wsPanes) < ptys {
			orient := rpcapi.OrientHorizontal
			if rng.Intn(2) == 1 {
				orient = rpcapi.OrientVertical
			}
			var split rpcapi.PaneSplitResult
			mustCall(t, ctl, rpcapi.MethodPaneSplit, rpcapi.PaneSplitParams{
				Session: sid, Workspace: ws.Workspace, Target: wsPanes[rng.Intn(len(wsPanes))],
				Orientation: orient, NewPaneCwd: canon,
			}, &split)
			wsPanes = append(wsPanes, split.NewPane)
		}
		for _, p := range wsPanes {
			idx := len(panes)
			echo := idx%2 == 1 // half generators, half input-echo surfaces
			argv := []string{"/bin/sh", "-c",
				fmt.Sprintf(`i=0; while :; do echo "soak-g%d-$i"; i=$((i+1)); sleep 0.25; done`, idx)}
			if echo {
				argv = []string{"/bin/cat"}
			}
			var sp rpcapi.SurfaceSpawnResult
			mustCall(t, ctl, rpcapi.MethodSurfaceSpawn, rpcapi.SurfaceSpawnParams{
				Session: sid, Workspace: ws.Workspace, Pane: p,
				Title: fmt.Sprintf("soak-%d", idx), Argv: argv, Cwd: canon,
				Cols: 120, Rows: 32,
			}, &sp)
			panes = append(panes, paneRef{workspace: ws.Workspace, pane: p, surface: sp.Surface, echo: echo})
		}
	}
	t.Logf("soak: %d PTY surfaces across %d workspaces, seed=%d, duration=%s",
		len(panes), (len(panes)+perWS-1)/perWS, *soakSeed, *soakDuration)

	// Baseline output cursors: every generator must have advanced past these
	// by the end of the run (a silently dead PTY is a failure, not noise).
	startSeq := map[string]uint64{}
	for _, p := range panes {
		if !p.echo {
			var insp rpcapi.PaneInspectResult
			mustCall(t, ctl, rpcapi.MethodPaneInspect, rpcapi.PaneInspectParams{
				Session: sid, Workspace: p.workspace, Pane: p.pane,
			}, &insp)
			startSeq[p.surface] = insp.LatestSeq
		}
	}

	// --- counters shared across goroutines ---------------------------------
	var eventsTotal, gapsRecovered, opsTotal atomic.Uint64
	subFail := make(chan error, 1)
	subCtx, stopSub := context.WithCancel(ctx)
	defer stopSub()

	// --- subscriber: contiguity + typed-gap recovery ------------------------
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		sub, err := client.Dial(subCtx, local.New(), spec, "soak-subscriber")
		if err != nil {
			subFail <- fmt.Errorf("subscriber dial: %w", err)
			return
		}
		defer sub.Close()
		next := uint64(1)
		for {
			stream, err := sub.Stream(subCtx, rpcapi.MethodEventSubscribe,
				rpcapi.EventSubscribeParams{Session: sid, FromSeq: next})
			if err != nil {
				if subCtx.Err() != nil {
					return
				}
				subFail <- fmt.Errorf("event.subscribe from %d: %w", next, err)
				return
			}
			for {
				ev, _, err := stream.Recv()
				if err != nil {
					if subCtx.Err() != nil {
						return
					}
					var ce *client.Error
					if errors.As(err, &ce) && ce.Code == "event_gap" {
						// Documented recovery: snapshot refresh yields a fresh
						// valid cursor; resume strictly after it.
						var snap rpcapi.SnapshotSaveResult
						if err := sub.Call(subCtx, rpcapi.MethodSnapshotSave,
							rpcapi.SnapshotSaveParams{Session: sid}, &snap); err != nil {
							if subCtx.Err() == nil {
								subFail <- fmt.Errorf("gap recovery snapshot: %w", err)
							}
							return
						}
						next = snap.Cursor + 1
						gapsRecovered.Add(1)
						break // resubscribe
					}
					subFail <- fmt.Errorf("event stream terminal at seq %d: %w", next, err)
					return
				}
				if ev.Event == "heartbeat" || ev.Seq == 0 {
					continue
				}
				if ev.Seq != next {
					subFail <- fmt.Errorf("event contiguity violated: got seq %d, want %d (event %q)", ev.Seq, next, ev.Event)
					return
				}
				next++
				eventsTotal.Add(1)
			}
		}
	}()

	// --- metrics sampler -----------------------------------------------------
	var samplesMu sync.Mutex
	var samples []sample
	var metricsFile *os.File
	if *soakMetrics != "" {
		if err := os.MkdirAll(filepath.Dir(*soakMetrics), 0o755); err != nil {
			t.Fatal(err)
		}
		metricsFile, err = os.Create(*soakMetrics)
		if err != nil {
			t.Fatal(err)
		}
		defer metricsFile.Close()
	}
	takeSample := func() {
		var dump rpcapi.DiagnosticsDumpResult
		if err := ctl.Call(ctx, rpcapi.MethodDiagnosticsDump, nil, &dump); err != nil {
			return // sampled best-effort; hard gates run on collected samples
		}
		var doc struct {
			Runtime struct {
				NumGoroutine   int    `json:"num_goroutine"`
				HeapAllocBytes uint64 `json:"heap_alloc_bytes"`
			} `json:"runtime"`
		}
		_ = json.Unmarshal(dump.Dump, &doc)
		s := sample{
			TMS:        time.Now().UnixMilli(),
			Goroutines: doc.Runtime.NumGoroutine,
			HeapAlloc:  doc.Runtime.HeapAllocBytes,
			FDs:        countFDs(),
			Children:   countDescendants(t),
			Events:     eventsTotal.Load(),
			Gaps:       gapsRecovered.Load(),
			Ops:        opsTotal.Load(),
		}
		samplesMu.Lock()
		samples = append(samples, s)
		samplesMu.Unlock()
		if metricsFile != nil {
			line, _ := json.Marshal(s)
			fmt.Fprintf(metricsFile, "%s\n", line)
		}
	}
	samplerDone := make(chan struct{})
	wg.Add(1)
	go func() {
		defer wg.Done()
		tick := time.NewTicker(5 * time.Second)
		defer tick.Stop()
		for {
			select {
			case <-samplerDone:
				return
			case <-tick.C:
				takeSample()
			}
		}
	}()

	// --- seeded stimulus loop ------------------------------------------------
	deadline := time.Now().Add(*soakDuration)
	lease := "soak-driver"
	pingN := 0
	lastSnapshot := time.Now()
	lastChurn := time.Now()
	echoCursor := map[string]uint64{}
	for time.Now().Before(deadline) {
		if err := drainFailure(subFail); err != nil {
			t.Fatalf("subscriber: %v", err)
		}
		p := panes[rng.Intn(len(panes))]
		switch op := rng.Intn(10); {
		case op < 3: // input ping to an echo surface, verified via replay
			ep := p
			if !ep.echo {
				for _, cand := range panes {
					if cand.echo {
						ep = cand
						break
					}
				}
			}
			pingN++
			marker := fmt.Sprintf("soak-ping-%d", pingN)
			var sent rpcapi.InputSendResult
			mustCall(t, ctl, rpcapi.MethodInputSend, rpcapi.InputSendParams{
				Session: sid, Surface: ep.surface, LeaseID: lease,
				DataB64: base64.StdEncoding.EncodeToString([]byte(marker + "\n")),
			}, &sent)
			if pingN%25 == 1 { // sampled end-to-end echo verification
				waitReplayContains(t, ctx, ctl, sid, ep.surface, echoCursor, marker, 15*time.Second)
			}
		case op < 5: // resize
			mustCall(t, ctl, rpcapi.MethodPaneResize, rpcapi.PaneResizeParams{
				Session: sid, Workspace: p.workspace, Pane: p.pane,
				Ratio: 0.2 + 0.6*rng.Float64(),
			}, &rpcapi.RevResult{})
		case op < 6: // focus
			mustCall(t, ctl, rpcapi.MethodPaneFocus, rpcapi.PaneFocusParams{
				Session: sid, Workspace: p.workspace, Pane: p.pane,
			}, &rpcapi.RevResult{})
		case op < 8: // attach/detach churn on a dedicated connection
			attachOnce(t, spec, sid, p.surface)
		case op < 9: // transient split+close (graph churn, PTY count stable)
			var split rpcapi.PaneSplitResult
			mustCall(t, ctl, rpcapi.MethodPaneSplit, rpcapi.PaneSplitParams{
				Session: sid, Workspace: p.workspace, Target: p.pane,
				Orientation: rpcapi.OrientVertical, NewPaneCwd: canon,
			}, &split)
			mustCall(t, ctl, rpcapi.MethodPaneClose, rpcapi.PaneCloseParams{
				Session: sid, Workspace: p.workspace, Pane: split.NewPane,
			}, &rpcapi.RevResult{})
		default: // inspect (read path)
			mustCall(t, ctl, rpcapi.MethodPaneInspect, rpcapi.PaneInspectParams{
				Session: sid, Workspace: p.workspace, Pane: p.pane,
			}, &rpcapi.PaneInspectResult{})
		}
		opsTotal.Add(1)

		if time.Since(lastSnapshot) > time.Minute {
			mustCall(t, ctl, rpcapi.MethodSnapshotSave, rpcapi.SnapshotSaveParams{Session: sid}, &rpcapi.SnapshotSaveResult{})
			lastSnapshot = time.Now()
			opsTotal.Add(1)
		}
		if time.Since(lastChurn) > 2*time.Minute { // stop/restart a generator
			gp := panes[0]
			var stopped rpcapi.SurfaceStopResult
			mustCall(t, ctl, rpcapi.MethodSurfaceStop, rpcapi.SurfaceStopParams{
				Session: sid, Workspace: gp.workspace, Pane: gp.pane, Surface: gp.surface, Confirm: true,
			}, &stopped)
			if stopped.Class == "" {
				t.Fatal("surface.stop returned an empty class")
			}
			// Reap is asynchronous (e2e flow 18/19 contract): poll restart
			// until the surface has left the live state and relaunches.
			restartBy := time.Now().Add(15 * time.Second)
			for {
				var restarted rpcapi.SurfaceRestartResult
				err := ctl.Call(ctx, rpcapi.MethodSurfaceRestart, rpcapi.SurfaceRestartParams{
					Session: sid, Workspace: gp.workspace, Pane: gp.pane, Surface: gp.surface,
				}, &restarted)
				if err == nil {
					if restarted.Class != "restarted" {
						t.Fatalf("surface.restart class = %q, want restarted", restarted.Class)
					}
					break
				}
				if time.Now().After(restartBy) {
					t.Fatalf("surface.restart never succeeded after stop: %v", err)
				}
				time.Sleep(200 * time.Millisecond)
			}
			lastChurn = time.Now()
			opsTotal.Add(2)
		}
		time.Sleep(time.Duration(50+rng.Intn(100)) * time.Millisecond)
	}
	takeSample() // final observation before teardown

	// --- verdicts ------------------------------------------------------------
	if err := drainFailure(subFail); err != nil {
		t.Fatalf("subscriber: %v", err)
	}
	select {
	case err := <-runErr:
		t.Fatalf("daemon crashed during soak: %v", err)
	default:
	}
	mustCall(t, ctl, rpcapi.MethodDaemonHealth, nil, &rpcapi.HealthResult{})
	if eventsTotal.Load() == 0 {
		t.Fatal("subscriber observed zero events over the whole soak")
	}
	for _, p := range panes {
		if p.echo {
			continue
		}
		var insp rpcapi.PaneInspectResult
		mustCall(t, ctl, rpcapi.MethodPaneInspect, rpcapi.PaneInspectParams{
			Session: sid, Workspace: p.workspace, Pane: p.pane,
		}, &insp)
		if insp.LatestSeq <= startSeq[p.surface] {
			t.Errorf("generator %s produced no output during the soak (seq %d -> %d)",
				p.surface, startSeq[p.surface], insp.LatestSeq)
		}
	}
	t.Logf("soak: ops=%d events=%d gaps_recovered=%d", opsTotal.Load(), eventsTotal.Load(), gapsRecovered.Load())

	writePprof(t, "end")

	// Zero-orphan gate: destroy the graph, then every PTY child must be gone.
	mustCall(t, ctl, rpcapi.MethodSessionDestroy, rpcapi.SessionDestroyParams{Session: sid}, &rpcapi.RevResult{})
	waitZeroDescendants(t, 30*time.Second, "after session.destroy")

	stopSub()
	var down rpcapi.ShutdownResult
	mustCall(t, ctl, rpcapi.MethodDaemonShutdown, nil, &down)
	if !down.Accepted {
		t.Fatalf("daemon.shutdown = %+v", down)
	}
	select {
	case err := <-runErr:
		if err != nil {
			t.Fatalf("daemon.Run returned error at shutdown: %v", err)
		}
	case <-time.After(30 * time.Second):
		t.Fatal("daemon never exited after accepted shutdown")
	}
	waitZeroDescendants(t, 15*time.Second, "after daemon shutdown")

	close(samplerDone)
	wg.Wait()

	// Trend gates: last-quartile median vs first-quartile median.
	samplesMu.Lock()
	defer samplesMu.Unlock()
	if len(samples) >= 8 {
		assertTrend(t, samples, "goroutines", func(s sample) float64 { return float64(s.Goroutines) }, 1.5, 50)
		assertTrend(t, samples, "fds", func(s sample) float64 { return float64(s.FDs) }, 1.5, 20)
		assertTrend(t, samples, "heap_alloc_bytes", func(s sample) float64 { return float64(s.HeapAlloc) }, 2.0, 64<<20)
		assertTrend(t, samples, "children", func(s sample) float64 { return float64(s.Children) }, 1.0, 4)
	} else {
		t.Logf("soak: %d samples — trend gates need >=8 (short smoke run); trends not evaluated", len(samples))
	}
}

// mustCall fails the test on any RPC error (the soak treats every driver op as
// a hard invariant; benign typed errors are handled at call sites).
func mustCall(t *testing.T, c *client.Client, method string, params, result any) {
	t.Helper()
	if err := c.Call(context.Background(), method, params, result); err != nil {
		t.Fatalf("%s: %v", method, err)
	}
}

func drainFailure(ch <-chan error) error {
	select {
	case err := <-ch:
		return err
	default:
		return nil
	}
}

// waitReplayContains polls bounded replay from the caller's cursor until the
// marker appears (sampled end-to-end input->PTY->replay verification).
func waitReplayContains(t *testing.T, ctx context.Context, c *client.Client, sid, surface string, cursor map[string]uint64, marker string, within time.Duration) {
	t.Helper()
	deadline := time.Now().Add(within)
	var buf strings.Builder
	for time.Now().Before(deadline) {
		var res rpcapi.ReplayReadResult
		err := c.Call(ctx, rpcapi.MethodReplayRead, rpcapi.ReplayReadParams{
			Session: sid, Surface: surface, FromSeq: cursor[surface], MaxBytes: 1 << 20,
		}, &res)
		if err != nil {
			var ce *client.Error
			if errors.As(err, &ce) && ce.Code == "replay_gap" {
				cursor[surface] = 0 // window trimmed: restart from oldest retained
				continue
			}
			t.Fatalf("replay.read: %v", err)
		}
		for _, ch := range res.Chunks {
			b, _ := base64.StdEncoding.DecodeString(ch.DataB64)
			buf.Write(b)
		}
		cursor[surface] = res.NextSeq
		if strings.Contains(buf.String(), marker) {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("marker %q never appeared in replay of %s within %s", marker, surface, within)
}

// attachOnce exercises the attach contract on a dedicated connection: dial,
// attach with bounded replay, read a handful of frames, detach by closing.
func attachOnce(t *testing.T, spec platform.TransportSpec, sid, surface string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	c, err := client.Dial(ctx, local.New(), spec, "soak-attach")
	if err != nil {
		t.Fatalf("attach dial: %v", err)
	}
	defer c.Close()
	stream, err := c.Stream(ctx, rpcapi.MethodAttach, rpcapi.AttachParams{Session: sid, Surface: surface})
	if err != nil {
		t.Fatalf("attach %s: %v", surface, err)
	}
	for i := 0; i < 5; i++ {
		if _, _, err := stream.Recv(); err != nil {
			if ctx.Err() != nil || errors.Is(err, io.EOF) {
				// Bounded read window elapsed, or the server ended the stream
				// cleanly ({"done":true} is a documented terminal condition).
				return
			}
			t.Fatalf("attach recv: %v", err)
		}
	}
}

// countFDs returns the process's open descriptor count (best-effort).
func countFDs() int {
	dir := "/proc/self/fd"
	if runtime.GOOS == "darwin" {
		dir = "/dev/fd"
	}
	ents, err := os.ReadDir(dir)
	if err != nil {
		return -1
	}
	return len(ents)
}

// countDescendants counts live descendant processes of this test process.
// The daemon runs in-process, so every PTY child it manages is our descendant.
func countDescendants(t *testing.T) int {
	pids, err := descendantPIDs()
	if err != nil {
		return -1
	}
	return len(pids)
}

// descendantPIDs builds a pid->ppid map (via /proc on Linux, `ps` elsewhere)
// and returns every transitive child of this process.
func descendantPIDs() ([]int, error) {
	parent := map[int]int{}
	if runtime.GOOS == "linux" {
		ents, err := os.ReadDir("/proc")
		if err != nil {
			return nil, err
		}
		for _, e := range ents {
			pid, err := strconv.Atoi(e.Name())
			if err != nil {
				continue
			}
			b, err := os.ReadFile(filepath.Join("/proc", e.Name(), "stat"))
			if err != nil {
				continue
			}
			// stat: pid (comm) state ppid ... — comm may contain spaces/parens.
			s := string(b)
			close := strings.LastIndexByte(s, ')')
			if close < 0 {
				continue
			}
			fields := strings.Fields(s[close+1:])
			if len(fields) < 2 {
				continue
			}
			ppid, err := strconv.Atoi(fields[1])
			if err != nil {
				continue
			}
			parent[pid] = ppid
		}
	} else {
		cmd := exec.Command("ps", "-axo", "pid=,ppid=")
		out, err := cmd.Output()
		if err != nil {
			return nil, err
		}
		// The scanning `ps` lists itself as our child; it is not a workload
		// descendant.
		scanner := cmd.Process.Pid
		for _, line := range strings.Split(string(out), "\n") {
			fields := strings.Fields(line)
			if len(fields) != 2 {
				continue
			}
			pid, err1 := strconv.Atoi(fields[0])
			ppid, err2 := strconv.Atoi(fields[1])
			if err1 != nil || err2 != nil || pid == scanner {
				continue
			}
			parent[pid] = ppid
		}
	}
	self := os.Getpid()
	var out []int
	for pid := range parent {
		for p, hops := pid, 0; p > 1 && hops < 64; hops++ {
			pp, ok := parent[p]
			if !ok {
				break
			}
			if pp == self {
				out = append(out, pid)
				break
			}
			p = pp
		}
	}
	return out, nil
}

// waitZeroDescendants polls until no descendant process remains (the spec's
// zero-orphan gate) or fails with the survivors listed.
func waitZeroDescendants(t *testing.T, within time.Duration, when string) {
	t.Helper()
	deadline := time.Now().Add(within)
	logged := false
	for {
		pids, err := descendantPIDs()
		if err != nil {
			t.Logf("descendant scan unavailable (%v) — orphan gate cannot run here", err)
			return
		}
		if len(pids) == 0 {
			return
		}
		if !logged {
			logged = true
			detail, _ := exec.Command("ps", "-o", "pid,ppid,stat,command", "-p",
				strings.Trim(strings.Join(strings.Fields(fmt.Sprint(pids)), ","), "[]")).CombinedOutput()
			t.Logf("descendants still present %s: %v\n%s", when, pids, detail)
		}
		if time.Now().After(deadline) {
			detail, _ := exec.Command("ps", "-o", "pid,ppid,stat,command", "-p",
				strings.Trim(strings.Join(strings.Fields(fmt.Sprint(pids)), ","), "[]")).CombinedOutput()
			t.Fatalf("orphaned descendants %s: %v\n%s", when, pids, detail)
		}
		time.Sleep(200 * time.Millisecond)
	}
}

// assertTrend gates unbounded growth: median(last quartile) must not exceed
// median(first quartile)*ratio + slack.
func assertTrend(t *testing.T, samples []sample, name string, get func(sample) float64, ratio, slack float64) {
	t.Helper()
	n := len(samples)
	q := n / 4
	first, last := median(samples[:q], get), median(samples[n-q:], get)
	limit := first*ratio + slack
	t.Logf("soak trend %s: first-quartile median=%.0f last-quartile median=%.0f limit=%.0f", name, first, last, limit)
	if last > limit {
		t.Errorf("unbounded %s trend: first-quartile median %.0f -> last-quartile median %.0f exceeds limit %.0f", name, first, last, limit)
	}
}

func median(s []sample, get func(sample) float64) float64 {
	vals := make([]float64, len(s))
	for i, x := range s {
		vals[i] = get(x)
	}
	sort.Float64s(vals)
	return vals[len(vals)/2]
}

// writePprof captures heap+goroutine profiles into -soak.pprof when set.
func writePprof(t *testing.T, phase string) {
	t.Helper()
	if *soakPprof == "" {
		return
	}
	if err := os.MkdirAll(*soakPprof, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, prof := range []string{"heap", "goroutine"} {
		f, err := os.Create(filepath.Join(*soakPprof, phase+"-"+prof+".pprof"))
		if err != nil {
			t.Fatal(err)
		}
		if err := pprof.Lookup(prof).WriteTo(f, 0); err != nil {
			t.Fatal(err)
		}
		f.Close()
	}
}
