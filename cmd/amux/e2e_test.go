// e2e_test.go is the black-box end-to-end suite for the 20 required CLI flows
// (spec "Required CLI flow contract"): it builds the real `amux` binary, boots
// a real daemon serving the owner-only socket, spawns real PTY processes, and
// drives every flow through the binary's argv/exit-code/--json contracts.
//
// Platform note: on Linux the daemon starts as a subprocess via `amux daemon
// start` (flow 1) with the production SO_PEERCRED seam. Off Linux that seam
// fails closed by design (ADR-0006), so the daemon runs in-process with an
// injected owner-UID seam — every other byte of the stack (binary, client,
// wire, engine, PTYs, persistence) is production code. The subprocess path is
// re-verified on the Linux CI lane (T6 prerequisite).
package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/amux-run/amux/internal/config"
	"github.com/amux-run/amux/internal/daemon"
)

var amuxBin string

func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "amuxbin")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	amuxBin = filepath.Join(dir, "amux")
	build := exec.Command("go", "build", "-o", amuxBin, ".")
	build.Stdout, build.Stderr = os.Stdout, os.Stderr
	if err := build.Run(); err != nil {
		fmt.Fprintln(os.Stderr, "building amux for e2e:", err)
		os.Exit(1)
	}
	code := m.Run()
	os.RemoveAll(dir)
	os.Exit(code)
}

type selfPeers struct{}

func (selfPeers) PeerUID(uintptr) (uint32, error) { return uint32(os.Getuid()), nil }

// e2eEnv is one running daemon plus the CLI invocation plumbing.
type e2eEnv struct {
	t      *testing.T
	socket string
	env    map[string]string
	// stop tears the daemon down; waitStopped blocks until it exited.
	stopped <-chan error
}

// startE2EDaemon boots the daemon for the suite and returns the harness.
func startE2EDaemon(t *testing.T) *e2eEnv {
	t.Helper()
	base, err := os.MkdirTemp("", "amuxe2e")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(base) })
	canon, err := filepath.EvalSymlinks(base)
	if err != nil {
		t.Fatal(err)
	}
	env := map[string]string{
		"HOME":            canon,
		"XDG_RUNTIME_DIR": filepath.Join(canon, "run"),
		"PATH":            os.Getenv("PATH"),
	}
	if err := os.MkdirAll(env["XDG_RUNTIME_DIR"], 0o700); err != nil {
		t.Fatal(err)
	}
	paths, err := config.Resolve(func(k string) string { return env[k] })
	if err != nil {
		t.Fatal(err)
	}
	h := &e2eEnv{t: t, socket: paths.SocketPath(), env: env}
	h.boot()
	return h
}

// boot starts a NEW daemon incarnation over the harness's environment and
// waits until it answers health. Called once by startE2EDaemon and again after
// a clean `daemon stop` to model a fresh daemon over the same durable state.
func (h *e2eEnv) boot() {
	t := h.t
	t.Helper()
	stopped := make(chan error, 1)
	if runtime.GOOS == "linux" {
		// Flow 1 (start daemon) through the real binary + production peers.
		cmd := exec.Command(amuxBin, "daemon", "start")
		cmd.Env = envList(h.env)
		cmd.Stderr = os.Stderr
		if err := cmd.Start(); err != nil {
			t.Fatal(err)
		}
		go func() { stopped <- cmd.Wait() }()
		t.Cleanup(func() { _ = cmd.Process.Kill() })
	} else {
		// Off Linux the production SO_PEERCRED seam fails closed (ADR-0006):
		// run the same production assembly in-process with the seam injected.
		ready := make(chan struct{})
		go func() {
			stopped <- daemon.Run(context.Background(), daemon.RunOptions{
				Getenv: func(k string) string { return h.env[k] },
				Logger: slog.New(slog.DiscardHandler),
				Peers:  selfPeers{},
				Ready:  ready,
			})
		}()
		select {
		case <-ready:
		case err := <-stopped:
			t.Fatalf("daemon exited before ready: %v", err)
		case <-time.After(15 * time.Second):
			t.Fatal("daemon never became ready")
		}
	}
	h.stopped = stopped

	// Flow 1 verification: health over the real socket via the real binary.
	deadline := time.Now().Add(15 * time.Second)
	for {
		if _, _, code := h.run("daemon", "health"); code == 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("daemon health never succeeded")
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func envList(env map[string]string) []string {
	out := make([]string, 0, len(env))
	for k, v := range env {
		out = append(out, k+"="+v)
	}
	return out
}

// run executes the amux binary black-box and returns stdout, stderr, exit code.
func (h *e2eEnv) run(args ...string) (string, string, int) {
	h.t.Helper()
	full := append([]string{"--socket", h.socket}, args...)
	cmd := exec.Command(amuxBin, full...)
	cmd.Env = envList(h.env)
	cmd.Stdin = nil // no TTY: confirmations must fail closed without --yes
	var out, errb strings.Builder
	cmd.Stdout, cmd.Stderr = &out, &errb
	err := cmd.Run()
	code := 0
	var xe *exec.ExitError
	if errors.As(err, &xe) {
		code = xe.ExitCode()
	} else if err != nil {
		h.t.Fatalf("exec amux %v: %v", args, err)
	}
	return out.String(), errb.String(), code
}

// mustJSON runs the CLI with --json and decodes stdout into out.
func (h *e2eEnv) mustJSON(out any, args ...string) {
	h.t.Helper()
	stdout, stderr, code := h.run(append([]string{"--json"}, args...)...)
	if code != 0 {
		h.t.Fatalf("amux %v: exit %d, stderr %q", args, code, stderr)
	}
	if err := json.Unmarshal([]byte(stdout), out); err != nil {
		h.t.Fatalf("amux %v: bad JSON %q: %v", args, stdout, err)
	}
}

// must runs the CLI expecting success and returns stdout.
func (h *e2eEnv) must(args ...string) string {
	h.t.Helper()
	stdout, stderr, code := h.run(args...)
	if code != 0 {
		h.t.Fatalf("amux %v: exit %d, stderr %q", args, code, stderr)
	}
	return stdout
}

// TestTwentyFlows drives the complete required CLI flow contract in order.
func TestTwentyFlows(t *testing.T) {
	if testing.Short() {
		t.Skip("e2e suite builds and execs the real binary")
	}
	h := startE2EDaemon(t) // flow 1: start daemon (verified via daemon health)

	// Flow 3: create session.
	var created struct {
		Session struct {
			ID string `json:"id"`
		} `json:"session"`
	}
	h.mustJSON(&created, "session", "create", "--name", "e2e")
	sid := created.Session.ID
	if sid == "" {
		t.Fatal("no session id")
	}

	// Flow 4: list sessions.
	var sessions struct {
		Sessions []struct {
			ID   string `json:"id"`
			Name string `json:"name"`
		} `json:"sessions"`
	}
	h.mustJSON(&sessions, "session", "list")
	if len(sessions.Sessions) != 1 || sessions.Sessions[0].ID != sid || sessions.Sessions[0].Name != "e2e" {
		t.Fatalf("session list = %+v", sessions)
	}

	// Flow 5: create workspace.
	var ws struct {
		Workspace    string `json:"workspace"`
		FirstPane    string `json:"first_pane"`
		FirstSurface string `json:"first_surface"`
		Rev          uint64 `json:"rev"`
	}
	h.mustJSON(&ws, "workspace", "create", "-s", sid, "--name", "main", "--cwd", h.env["HOME"])
	if ws.Workspace == "" || ws.FirstPane == "" || ws.Rev == 0 {
		t.Fatalf("workspace create = %+v", ws)
	}

	// Flow 6: list workspaces.
	var wsl struct {
		Workspaces []struct {
			ID        string `json:"id"`
			PaneCount int    `json:"pane_count"`
		} `json:"workspaces"`
	}
	h.mustJSON(&wsl, "workspace", "list", "-s", sid)
	if len(wsl.Workspaces) != 1 || wsl.Workspaces[0].ID != ws.Workspace {
		t.Fatalf("workspace list = %+v", wsl)
	}

	// Flow 7: split horizontally. Flow 8: split vertically.
	var split1, split2 struct {
		NewPane    string  `json:"new_pane"`
		NewSurface string  `json:"new_surface"`
		Ratio      float64 `json:"ratio"`
		Rev        uint64  `json:"rev"`
	}
	h.mustJSON(&split1, "pane", "split", ws.FirstPane, "-s", sid, "-w", ws.Workspace, "-o", "horizontal")
	if split1.NewPane == "" {
		t.Fatalf("horizontal split = %+v", split1)
	}
	h.mustJSON(&split2, "pane", "split", split1.NewPane, "-s", sid, "-w", ws.Workspace, "-o", "vertical", "--ratio", "0.6")
	if split2.NewPane == "" || split2.Rev <= split1.Rev {
		t.Fatalf("vertical split = %+v", split2)
	}

	// Flow 9: focus pane. Flow 10: resize pane.
	var rev struct {
		Rev uint64 `json:"rev"`
	}
	h.mustJSON(&rev, "pane", "focus", split2.NewPane, "-s", sid, "-w", ws.Workspace)
	h.mustJSON(&rev, "pane", "resize", split2.NewPane, "-s", sid, "-w", ws.Workspace, "--ratio", "0.4")

	// Flow 11: spawn a real terminal surface (a shell that marks its output
	// then stays alive on cat, so attach/input/replay are deterministic).
	var spawned struct {
		Surface string `json:"surface"`
		Rev     uint64 `json:"rev"`
	}
	h.mustJSON(&spawned, "surface", "spawn", "-s", sid, "-w", ws.Workspace, "-p", ws.FirstPane,
		"--title", "e2e-shell", "--", "/bin/sh", "-c", "echo e2e-hello; exec cat")
	if spawned.Surface == "" {
		t.Fatalf("spawn = %+v", spawned)
	}

	// Flow 11 (automatic policy): a second surface under an explicit automatic
	// restart policy. Its marker embeds $(pwd), so the restore relaunches below
	// prove the replacement runs the recorded argv in the recorded cwd (the
	// pane cwd, HOME).
	autoMarker := "e2e-auto " + h.env["HOME"]
	var autoSpawned struct {
		Surface string `json:"surface"`
		Rev     uint64 `json:"rev"`
	}
	h.mustJSON(&autoSpawned, "surface", "spawn", "-s", sid, "-w", ws.Workspace, "-p", ws.FirstPane,
		"--title", "e2e-auto", "--restart-policy", "automatic",
		"--", "/bin/sh", "-c", `echo "e2e-auto $(pwd)"; exec cat`)
	if autoSpawned.Surface == "" {
		t.Fatalf("automatic spawn = %+v", autoSpawned)
	}
	waitFor(t, 5*time.Second, func() bool {
		out, _, code := h.run("replay", "read", "-s", sid, "--surface", autoSpawned.Surface)
		return code == 0 && strings.Contains(out, autoMarker)
	}, "automatic surface never emitted its cwd marker")

	// Flow 14 (first read): bounded replay must surface the marker.
	waitFor(t, 5*time.Second, func() bool {
		out, _, code := h.run("replay", "read", "-s", sid, "--surface", spawned.Surface)
		return code == 0 && strings.Contains(out, "e2e-hello")
	}, "replay never contained the spawn marker")

	// Flow 14 (explicit bound + continuation): --max-bytes caps the decoded
	// page, next_seq is sequence truth for the next page, and an invalid
	// bound fails typed (exit 2), never severs the connection.
	var bounded struct {
		Chunks []struct {
			Seq     uint64 `json:"seq"`
			DataB64 string `json:"data_b64"`
		} `json:"chunks"`
		NextSeq   uint64 `json:"next_seq"`
		LatestSeq uint64 `json:"latest_seq"`
	}
	h.mustJSON(&bounded, "replay", "read", "-s", sid, "--surface", spawned.Surface, "--max-bytes", "65536")
	if len(bounded.Chunks) == 0 {
		t.Fatal("bounded replay read returned no chunks")
	}
	var boundedBytes int
	for _, c := range bounded.Chunks {
		b, err := base64.StdEncoding.DecodeString(c.DataB64)
		if err != nil {
			t.Fatalf("chunk %d: bad base64: %v", c.Seq, err)
		}
		boundedBytes += len(b)
	}
	if boundedBytes > 65536 {
		t.Fatalf("bounded replay decoded %d bytes > --max-bytes 65536", boundedBytes)
	}
	if bounded.NextSeq != bounded.Chunks[len(bounded.Chunks)-1].Seq+1 {
		t.Fatalf("bounded replay next_seq = %d, want last returned seq+1 = %d",
			bounded.NextSeq, bounded.Chunks[len(bounded.Chunks)-1].Seq+1)
	}
	var cont struct {
		Chunks []struct {
			Seq uint64 `json:"seq"`
		} `json:"chunks"`
	}
	h.mustJSON(&cont, "replay", "read", "-s", sid, "--surface", spawned.Surface,
		"--from-seq", fmt.Sprint(bounded.NextSeq), "--max-bytes", "65536")
	for _, c := range cont.Chunks {
		if c.Seq < bounded.NextSeq {
			t.Fatalf("continuation re-served seq %d (< next_seq %d): duplicate replay", c.Seq, bounded.NextSeq)
		}
	}
	if _, stderr, code := h.run("replay", "read", "-s", sid, "--surface", spawned.Surface, "--max-bytes=-1"); code != 2 {
		t.Fatalf("negative --max-bytes exit = %d (stderr %q), want 2 (typed invalid_argument)", code, stderr)
	}

	// Flow 12: attach — snapshot header then replayed output on stdout.
	attached := h.must("attach", spawned.Surface, "-s", sid, "--from-seq", "1", "--max-frames", "1")
	if !strings.Contains(attached, "e2e-hello") {
		t.Fatalf("attach output = %q", attached)
	}
	// Attach with --json emits the attach_snapshot header + framed data.
	attachedJSON := h.must("attach", spawned.Surface, "-s", sid, "--from-seq", "1", "--max-frames", "1", "--json")
	if !strings.Contains(attachedJSON, "attach_snapshot") || !strings.Contains(attachedJSON, "up_to_seq") {
		t.Fatalf("attach --json = %q", attachedJSON)
	}

	// Flow 13: send input under a lease; cat echoes it back into the replay.
	var sent struct {
		Bytes int `json:"bytes"`
	}
	h.mustJSON(&sent, "input", "send", "-s", sid, "--surface", spawned.Surface, "--lease", "cli-e2e", "--data", "e2e-typed\n")
	if sent.Bytes != len("e2e-typed\n") {
		t.Fatalf("input send = %+v", sent)
	}
	waitFor(t, 5*time.Second, func() bool {
		out, _, code := h.run("replay", "read", "-s", sid, "--surface", spawned.Surface)
		return code == 0 && strings.Contains(out, "e2e-typed")
	}, "replay never echoed the typed input")

	// Lease discipline: a second lease identity is rejected (exit 4 conflict),
	// and an unconfirmed takeover fails closed (exit 2).
	if _, _, code := h.run("input", "send", "-s", sid, "--surface", spawned.Surface, "--lease", "intruder", "--data", "x"); code != 4 {
		t.Fatalf("second lease holder exit = %d, want 4", code)
	}
	if _, _, code := h.run("input", "send", "-s", sid, "--surface", spawned.Surface, "--lease", "intruder", "--data", "x", "--takeover"); code != 2 {
		t.Fatalf("unconfirmed takeover exit = %d, want 2", code)
	}

	// Flow 15: inspect pane state.
	var insp struct {
		Pane      string `json:"pane"`
		LatestSeq uint64 `json:"latest_seq"`
		Surfaces  []struct {
			ID    string `json:"id"`
			Title string `json:"title"`
		} `json:"surfaces"`
	}
	h.mustJSON(&insp, "inspect", ws.FirstPane, "-s", sid, "-w", ws.Workspace)
	if insp.LatestSeq == 0 || len(insp.Surfaces) < 2 {
		t.Fatalf("inspect = %+v", insp)
	}

	// Flow 16: save snapshot.
	var saved struct {
		CheckpointID string `json:"checkpoint_id"`
		Cursor       uint64 `json:"cursor"`
	}
	h.mustJSON(&saved, "snapshot", "save", "-s", sid)
	if saved.CheckpointID == "" {
		t.Fatalf("snapshot save = %+v", saved)
	}

	// Flow 17 (in-daemon): restoring the checkpoint this daemon just saved,
	// while it still owns the SAME live PTY (the `cat` shell), reconciles the
	// surface LIVE — without stopping or restarting it. Every classification
	// carries a reason; nothing claims process-memory resurrection (the class
	// is live because the process never died, not because it was rebuilt).
	var restored struct {
		Session  string `json:"session"`
		Surfaces []struct {
			Surface string `json:"surface"`
			Class   string `json:"class"`
			Reason  string `json:"reason"`
		} `json:"surfaces"`
	}
	h.mustJSON(&restored, "restore", "-s", sid)
	if restored.Session != sid || len(restored.Surfaces) == 0 {
		t.Fatalf("restore = %+v", restored)
	}
	for _, s := range restored.Surfaces {
		if s.Reason == "" {
			t.Fatalf("restore left %s without a reason", s.Surface)
		}
		if s.Surface == spawned.Surface {
			if s.Class != "live" {
				t.Fatalf("in-daemon restore classified still-owned %s as %q (%s), want live", s.Surface, s.Class, s.Reason)
			}
			if s.Reason != "reconciled to still-owned pty identity" {
				t.Fatalf("live reason = %q", s.Reason)
			}
		}
		// Live precedes restarted (ADR-0005): a still-owned automatic-policy
		// surface reconciles live — it is NOT relaunched while it runs.
		if s.Surface == autoSpawned.Surface && s.Class != "live" {
			t.Fatalf("in-daemon restore classified still-owned automatic-policy %s as %q (%s), want live", s.Surface, s.Class, s.Reason)
		}
	}
	replayAfter := h.must("replay", "read", "-s", sid, "--surface", spawned.Surface)
	if !strings.Contains(replayAfter, "e2e-hello") {
		t.Fatalf("restored replay lost history: %q", replayAfter)
	}
	// The SAME process still answers input after the live reconcile (it was
	// never stopped/relaunched: exactly one spawn marker in the replay).
	h.mustJSON(&sent, "input", "send", "-s", sid, "--surface", spawned.Surface, "--lease", "cli-e2e", "--data", "post-restore\n")
	waitFor(t, 5*time.Second, func() bool {
		out, _, code := h.run("replay", "read", "-s", sid, "--surface", spawned.Surface)
		return code == 0 && strings.Contains(out, "post-restore")
	}, "live-reconciled surface stopped answering input")
	if got := strings.Count(h.must("replay", "read", "-s", sid, "--surface", spawned.Surface), "e2e-hello"); got != 1 {
		t.Fatalf("live reconcile relaunched the process: %d spawn markers", got)
	}

	// Flow 17 (in-daemon, automatic policy): stop the automatic surface, then
	// restore the same checkpoint. `restarted` is completed behavior
	// (ADR-0005): the restore itself launches the replacement process — its
	// fresh cwd marker joins the restored history and it answers input — while
	// the still-owned manual surface reconciles live untouched. The stop is
	// polled to `stopped` first so the checkpoint's ownership attestation no
	// longer vouches for the surface (reap is asynchronous).
	var autoStopped struct {
		Class string `json:"class"`
	}
	waitFor(t, 5*time.Second, func() bool {
		out, _, code := h.run("--json", "stop", autoSpawned.Surface, "-s", sid, "-w", ws.Workspace, "-p", ws.FirstPane, "--yes")
		return code == 0 && json.Unmarshal([]byte(out), &autoStopped) == nil && autoStopped.Class == "stopped"
	}, "automatic surface never reached stopped before the relaunch restore")
	var restored2 struct {
		Surfaces []struct {
			Surface string `json:"surface"`
			Class   string `json:"class"`
			Reason  string `json:"reason"`
		} `json:"surfaces"`
	}
	h.mustJSON(&restored2, "restore", "-s", sid)
	for _, s := range restored2.Surfaces {
		switch s.Surface {
		case spawned.Surface:
			if s.Class != "live" {
				t.Fatalf("second in-daemon restore classified still-owned %s as %q (%s), want live", s.Surface, s.Class, s.Reason)
			}
		case autoSpawned.Surface:
			if s.Class != "restarted" {
				t.Fatalf("automatic-policy restore classified %s as %q (%s), want restarted", s.Surface, s.Class, s.Reason)
			}
			if s.Reason != "relaunched under automatic restart policy" {
				t.Fatalf("restarted reason = %q", s.Reason)
			}
		}
	}
	// The replacement genuinely launched: exactly one fresh cwd marker joins
	// the restored history (2 total — same executable/argv/cwd), and it
	// answers input under a fresh lease (leases are never restored).
	waitFor(t, 5*time.Second, func() bool {
		out, _, code := h.run("replay", "read", "-s", sid, "--surface", autoSpawned.Surface, "--from-seq", "1")
		return code == 0 && strings.Count(out, autoMarker) == 2
	}, "restore never relaunched the automatic surface")
	h.mustJSON(&sent, "input", "send", "-s", sid, "--surface", autoSpawned.Surface, "--lease", "auto-e2e", "--data", "auto-typed\n")
	waitFor(t, 5*time.Second, func() bool {
		out, _, code := h.run("replay", "read", "-s", sid, "--surface", autoSpawned.Surface)
		return code == 0 && strings.Contains(out, "auto-typed")
	}, "relaunched automatic surface never answered input")
	// Restarted means LIVE now: an operator restart right after must refuse.
	if _, _, code := h.run("restart", autoSpawned.Surface, "-s", sid, "-w", ws.Workspace, "-p", ws.FirstPane); code != 4 {
		t.Fatalf("restart of relaunched automatic surface exit = %d, want 4 (conflict)", code)
	}

	// Flow 19: stop the surface process. Unconfirmed fails closed (exit 2);
	// --yes stops the LIVE process (reap is asynchronous; flow 18 polls it).
	if _, _, code := h.run("stop", spawned.Surface, "-s", sid, "-w", ws.Workspace, "-p", ws.FirstPane); code != 2 {
		t.Fatalf("unconfirmed stop exit = %d, want 2", code)
	}
	var stopres struct {
		Class string `json:"class"`
	}
	h.mustJSON(&stopres, "stop", spawned.Surface, "-s", sid, "-w", ws.Workspace, "-p", ws.FirstPane, "--yes")
	if stopres.Class == "" {
		t.Fatalf("stop = %+v", stopres)
	}

	// Flow 18: restart the stopped surface — a NEW process, classified
	// restarted, whose fresh marker lands after the retained history. The
	// restart is polled because the stop above reaps asynchronously.
	var restarted struct {
		Class string `json:"class"`
	}
	waitFor(t, 5*time.Second, func() bool {
		out, _, code := h.run("--json", "restart", spawned.Surface, "-s", sid, "-w", ws.Workspace, "-p", ws.FirstPane)
		return code == 0 && json.Unmarshal([]byte(out), &restarted) == nil
	}, "surface never became restartable after the confirmed stop")
	if restarted.Class != "restarted" {
		t.Fatalf("restart class = %+v", restarted)
	}
	waitFor(t, 5*time.Second, func() bool {
		out, _, code := h.run("replay", "read", "-s", sid, "--surface", spawned.Surface, "--from-seq", "1")
		return code == 0 && strings.Count(out, "e2e-hello") >= 2
	}, "restarted process never re-emitted its marker")

	// Flow 20: subscribe to events — a committed post-restore mutation replays
	// deterministically from its cursor as one JSON line per event. A cursor
	// before the retained window is a TYPED event_gap boundary (exit 7), never
	// a silent bridge: the restore truncated replay at the checkpoint cursor.
	h.must("workspace", "rename", ws.Workspace, "renamed-e2e", "-s", sid)
	if _, stderr, code := h.run("event", "subscribe", "-s", sid, "--from-seq", "1", "--max-events", "1"); code != 7 || !strings.Contains(stderr, "event_gap") {
		t.Fatalf("pre-window cursor: exit %d stderr %q, want typed event_gap exit 7", code, stderr)
	}
	subOut := h.must("event", "subscribe", "-s", sid, "--from-seq", fmt.Sprint(saved.Cursor+1), "--max-events", "1", "--json")
	var ev struct {
		Event string `json:"event"`
		Seq   uint64 `json:"seq"`
	}
	if err := json.Unmarshal([]byte(firstLine(subOut)), &ev); err != nil {
		t.Fatalf("event line %q: %v", subOut, err)
	}
	if ev.Event == "" || ev.Seq == 0 {
		t.Fatalf("event = %+v", ev)
	}

	// Confirmation matrix fail-closed spot checks (destroy class) + a typed
	// not_found exit code.
	if _, _, code := h.run("session", "destroy", sid); code != 2 {
		t.Fatalf("unconfirmed session destroy exit = %d, want 2", code)
	}
	if _, _, code := h.run("workspace", "list", "-s", "no-such-session"); code != 3 {
		t.Fatalf("unknown session exit = %d, want 3", code)
	}

	// Flow 2: stop daemon (confirmed) — the daemon exits cleanly and the
	// socket stops answering (exit 9: unreachable).
	if _, _, code := h.run("daemon", "stop"); code != 2 {
		t.Fatalf("unconfirmed daemon stop exit = %d, want 2", code)
	}
	h.must("daemon", "stop", "--yes")
	select {
	case err := <-h.stopped:
		if err != nil {
			t.Fatalf("daemon exited with %v after clean stop", err)
		}
	case <-time.After(15 * time.Second):
		t.Fatal("daemon never exited after stop")
	}
	if _, _, code := h.run("daemon", "health"); code != 9 {
		t.Fatalf("health after stop exit = %d, want 9", code)
	}

	// Fresh-daemon restore (flow 17, clean-daemon variant): a NEW daemon
	// incarnation owns no PTYs, so the SAME checkpoint that reconciled live
	// in-daemon can never classify live here (ADR-0005: fresh daemon is
	// structurally excluded). The replay history still restores from the
	// committed sidecar.
	h.boot()
	var fresh struct {
		Session  string `json:"session"`
		Surfaces []struct {
			Surface string `json:"surface"`
			Class   string `json:"class"`
			Reason  string `json:"reason"`
		} `json:"surfaces"`
	}
	h.mustJSON(&fresh, "restore", "-s", sid)
	if fresh.Session != sid || len(fresh.Surfaces) == 0 {
		t.Fatalf("fresh restore = %+v", fresh)
	}
	for _, s := range fresh.Surfaces {
		if s.Class == "live" {
			t.Fatalf("fresh-daemon restore classified %s live (resurrection claim)", s.Surface)
		}
		if s.Reason == "" {
			t.Fatalf("fresh restore left %s without a reason", s.Surface)
		}
		if s.Surface == spawned.Surface && s.Class != "stopped" {
			t.Fatalf("fresh restore classified manual-policy %s as %q", s.Surface, s.Class)
		}
		// Fresh-daemon automatic restore is a REAL replacement launch under
		// the new daemon's supervisor, reported only as restarted.
		if s.Surface == autoSpawned.Surface {
			if s.Class != "restarted" {
				t.Fatalf("fresh restore classified automatic-policy %s as %q (%s), want restarted", s.Surface, s.Class, s.Reason)
			}
			if s.Reason != "relaunched under automatic restart policy" {
				t.Fatalf("fresh restarted reason = %q", s.Reason)
			}
		}
	}
	freshReplay := h.must("replay", "read", "-s", sid, "--surface", spawned.Surface)
	if !strings.Contains(freshReplay, "e2e-hello") {
		t.Fatalf("fresh restore lost persisted replay: %q", freshReplay)
	}
	// The manual surface was NOT launched: exactly the checkpoint's marker.
	if got := strings.Count(freshReplay, "e2e-hello"); got != 1 {
		t.Fatalf("fresh restore launched the manual surface: %d markers", got)
	}
	// The automatic replacement really runs under the fresh daemon: its new
	// cwd marker joins the checkpoint history and it answers input.
	waitFor(t, 5*time.Second, func() bool {
		out, _, code := h.run("replay", "read", "-s", sid, "--surface", autoSpawned.Surface, "--from-seq", "1")
		return code == 0 && strings.Count(out, autoMarker) == 2
	}, "fresh-daemon restore never relaunched the automatic surface")
	h.mustJSON(&sent, "input", "send", "-s", sid, "--surface", autoSpawned.Surface, "--lease", "fresh-e2e", "--data", "fresh-typed\n")
	waitFor(t, 5*time.Second, func() bool {
		out, _, code := h.run("replay", "read", "-s", sid, "--surface", autoSpawned.Surface)
		return code == 0 && strings.Contains(out, "fresh-typed")
	}, "fresh-daemon relaunched surface never answered input")
	h.must("daemon", "stop", "--yes")
	select {
	case err := <-h.stopped:
		if err != nil {
			t.Fatalf("second daemon exited with %v after clean stop", err)
		}
	case <-time.After(15 * time.Second):
		t.Fatal("second daemon never exited after stop")
	}
}

func waitFor(t *testing.T, d time.Duration, cond func() bool, msg string) {
	t.Helper()
	deadline := time.Now().Add(d)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(25 * time.Millisecond)
	}
	t.Fatal(msg)
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}
