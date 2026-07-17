//go:build darwin || linux

package pty

import (
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/amux-run/amux/internal/platform"
)

// recorder captures OnOutput/OnExit callbacks with polling-based waits so
// every assertion is bounded by the watchdog instead of hanging.
type recorder struct {
	mu    sync.Mutex
	out   map[string][]byte
	exits map[string][]exitEvent
}

type exitEvent struct {
	exit   platform.PTYExit
	reason string
}

func newRecorder() *recorder {
	return &recorder{out: map[string][]byte{}, exits: map[string][]exitEvent{}}
}

func (r *recorder) onOutput(id string, p []byte) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.out[id] = append(r.out[id], p...)
}

func (r *recorder) onExit(id string, exit platform.PTYExit, reason string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.exits[id] = append(r.exits[id], exitEvent{exit: exit, reason: reason})
}

func (r *recorder) output(id string) string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return string(r.out[id])
}

func (r *recorder) exitCount(id string) int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.exits[id])
}

// waitExit blocks (bounded) until at least one OnExit for id, returning the
// FIRST event; exactly-once tests separately assert exitCount stays 1.
func (r *recorder) waitExit(t *testing.T, id string) exitEvent {
	t.Helper()
	deadline := time.Now().Add(watchdog)
	for time.Now().Before(deadline) {
		r.mu.Lock()
		if evs := r.exits[id]; len(evs) > 0 {
			ev := evs[0]
			r.mu.Unlock()
			return ev
		}
		r.mu.Unlock()
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for OnExit(%q)", id)
	return exitEvent{}
}

func (r *recorder) waitOutputContains(t *testing.T, id, substr string) {
	t.Helper()
	deadline := time.Now().Add(watchdog)
	for time.Now().Before(deadline) {
		if strings.Contains(r.output(id), substr) {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for output of %q to contain %q; have %q",
		id, substr, r.output(id))
}

// newTestSupervisor builds a Supervisor over the real PTY with the given
// grace and a fresh recorder. Containment is deliberately nil: on this host
// the portable process-group baseline is under test (see doc.go — a TEST
// baseline, not a support claim).
func newTestSupervisor(t *testing.T, graceMS int64) (*Supervisor, *recorder) {
	t.Helper()
	rec := newRecorder()
	sup, err := NewSupervisor(SupervisorConfig{
		PTY:      New(),
		GraceMS:  graceMS,
		OnExit:   rec.onExit,
		OnOutput: rec.onOutput,
	})
	if err != nil {
		t.Fatalf("NewSupervisor: %v", err)
	}
	t.Cleanup(func() { _ = sup.StopAll() })
	return sup, rec
}

func TestNewSupervisorRequiresPTY(t *testing.T) {
	if _, err := NewSupervisor(SupervisorConfig{}); err == nil {
		t.Fatal("NewSupervisor accepted a nil PTY seam; want fail-closed error")
	}
}

func TestSpawnOutputAndExitExactlyOnce(t *testing.T) {
	sup, rec := newTestSupervisor(t, 0)
	if err := sup.Spawn("hello", testSpec(t, "printf hello")); err != nil {
		t.Fatalf("Spawn: %v", err)
	}
	ev := rec.waitExit(t, "hello")
	if ev.exit.Code != 0 || ev.exit.Signal != "" || ev.reason != ReasonExited {
		t.Fatalf("exit = %+v reason %q; want Code 0 reason %q", ev.exit, ev.reason, ReasonExited)
	}
	rec.waitOutputContains(t, "hello", "hello")
	// Prove exactly-once: give any duplicate a chance to arrive, then count.
	time.Sleep(50 * time.Millisecond)
	if n := rec.exitCount("hello"); n != 1 {
		t.Fatalf("OnExit fired %d times; want exactly 1", n)
	}
	if sup.Alive("hello") {
		t.Fatal("Alive after exit; want retired")
	}
}

func TestExitCodeAndSignalClassification(t *testing.T) {
	sup, rec := newTestSupervisor(t, 0)

	if err := sup.Spawn("code7", testSpec(t, "exit 7")); err != nil {
		t.Fatalf("Spawn: %v", err)
	}
	ev := rec.waitExit(t, "code7")
	if ev.exit.Code != 7 || ev.exit.Signal != "" || ev.reason != ReasonExited {
		t.Fatalf("code7 exit = %+v reason %q; want Code 7 reason %q", ev.exit, ev.reason, ReasonExited)
	}

	if err := sup.Spawn("killed", testSpec(t, "echo ready; sleep 30")); err != nil {
		t.Fatalf("Spawn: %v", err)
	}
	rec.waitOutputContains(t, "killed", "ready")
	if err := sup.Signal("killed", syscall.SIGKILL); err != nil {
		t.Fatalf("Signal: %v", err)
	}
	ev = rec.waitExit(t, "killed")
	if ev.exit.Signal != "SIGKILL" || ev.reason != ReasonSignaled {
		t.Fatalf("killed exit = %+v reason %q; want Signal SIGKILL reason %q",
			ev.exit, ev.reason, ReasonSignaled)
	}
}

func TestInputEchoedThroughCat(t *testing.T) {
	sup, rec := newTestSupervisor(t, 0)
	if err := sup.Spawn("cat", testSpec(t, "cat")); err != nil {
		t.Fatalf("Spawn: %v", err)
	}
	if err := sup.Input("cat", []byte("abc\n")); err != nil {
		t.Fatalf("Input: %v", err)
	}
	// The PTY echoes the typed line and cat writes it back.
	rec.waitOutputContains(t, "cat", "abc")
	if err := sup.Stop("cat"); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	rec.waitExit(t, "cat")
}

func TestResizeVisibleToChild(t *testing.T) {
	sup, rec := newTestSupervisor(t, 0)
	// The child blocks on read until we send a newline AFTER resizing, then
	// reports its window size from the controlling terminal (TIOCGWINSZ).
	if err := sup.Spawn("size", testSpec(t, "read line; stty size")); err != nil {
		t.Fatalf("Spawn: %v", err)
	}
	if err := sup.Resize("size", platform.PTYSize{Rows: 40, Cols: 100}); err != nil {
		t.Fatalf("Resize: %v", err)
	}
	if err := sup.Input("size", []byte("\n")); err != nil {
		t.Fatalf("Input: %v", err)
	}
	rec.waitExit(t, "size")
	rec.waitOutputContains(t, "size", "40 100")
}

func TestSignalTargetsWholeProcessGroup(t *testing.T) {
	sup, rec := newTestSupervisor(t, 0)
	script := `echo SELF:$$; sleep 30 & echo CHILD:$!; wait`
	if err := sup.Spawn("group", testSpec(t, script)); err != nil {
		t.Fatalf("Spawn: %v", err)
	}
	rec.waitOutputContains(t, "group", "CHILD:")

	pids := regexp.MustCompile(`(SELF|CHILD):(\d+)`).FindAllStringSubmatch(rec.output("group"), -1)
	if len(pids) != 2 {
		t.Fatalf("expected SELF and CHILD pids in output %q", rec.output("group"))
	}
	var shellPID, sleepPID int
	for _, m := range pids {
		pid, err := strconv.Atoi(m[2])
		if err != nil {
			t.Fatalf("bad pid %q: %v", m[2], err)
		}
		if m[1] == "SELF" {
			shellPID = pid
		} else {
			sleepPID = pid
		}
	}

	if err := sup.Signal("group", syscall.SIGTERM); err != nil {
		t.Fatalf("Signal: %v", err)
	}
	ev := rec.waitExit(t, "group")
	if ev.exit.Signal != "SIGTERM" || ev.reason != ReasonSignaled {
		t.Fatalf("exit = %+v reason %q; want SIGTERM/%q", ev.exit, ev.reason, ReasonSignaled)
	}
	// Both group members must die: the shell (direct child, reaped by the
	// supervisor) and the backgrounded sleep (reaped by init after the group
	// signal). Poll — init's reap is asynchronous.
	for _, pid := range []int{shellPID, sleepPID} {
		deadline := time.Now().Add(watchdog)
		for pidAlive(pid) {
			if time.Now().After(deadline) {
				t.Fatalf("pid %d still alive after group SIGTERM", pid)
			}
			time.Sleep(5 * time.Millisecond)
		}
	}
}

func TestStopGracefulNoEscalation(t *testing.T) {
	sup, rec := newTestSupervisor(t, 5000)
	if err := sup.Spawn("polite", testSpec(t, "echo ready; sleep 30")); err != nil {
		t.Fatalf("Spawn: %v", err)
	}
	rec.waitOutputContains(t, "polite", "ready")
	if err := sup.Stop("polite"); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	ev := rec.waitExit(t, "polite")
	if ev.exit.Signal != "SIGTERM" || ev.reason != ReasonStopped {
		t.Fatalf("exit = %+v reason %q; want SIGTERM/%q", ev.exit, ev.reason, ReasonStopped)
	}
	if n := sup.Escalations(); n != 0 {
		t.Fatalf("Escalations = %d; want 0 for a graceful stop", n)
	}
}

func TestStopEscalatesToSIGKILLAfterGrace(t *testing.T) {
	const graceMS = 100
	sup, rec := newTestSupervisor(t, graceMS)
	// trap "" TERM makes the shell ignore SIGTERM, and exec'd children
	// (sleep) inherit the ignored disposition, so only the escalation kills.
	if err := sup.Spawn("stubborn", testSpec(t, `trap "" TERM; echo ready; sleep 30`)); err != nil {
		t.Fatalf("Spawn: %v", err)
	}
	rec.waitOutputContains(t, "stubborn", "ready")

	start := time.Now()
	if err := sup.Stop("stubborn"); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	ev := rec.waitExit(t, "stubborn")
	elapsed := time.Since(start)

	if ev.exit.Signal != "SIGKILL" || ev.reason != ReasonStopped {
		t.Fatalf("exit = %+v reason %q; want SIGKILL/%q", ev.exit, ev.reason, ReasonStopped)
	}
	if n := sup.Escalations(); n != 1 {
		t.Fatalf("Escalations = %d; want exactly 1 (escalation must actually fire)", n)
	}
	if elapsed < graceMS*time.Millisecond {
		t.Fatalf("exit after %v, before the %dms grace elapsed — SIGTERM must not have killed it", elapsed, graceMS)
	}
}

func TestStopAllLeavesZeroOrphans(t *testing.T) {
	sup, rec := newTestSupervisor(t, 200)
	const n = 5
	for i := 0; i < n; i++ {
		id := fmt.Sprintf("worker-%d", i)
		if err := sup.Spawn(id, testSpec(t, "echo ready; sleep 30")); err != nil {
			t.Fatalf("Spawn %s: %v", id, err)
		}
		rec.waitOutputContains(t, id, "ready")
	}
	if got := len(sup.Live()); got != n {
		t.Fatalf("Live = %d; want %d", got, n)
	}

	if err := sup.StopAll(); err != nil {
		t.Fatalf("StopAll: %v", err)
	}
	if got := sup.Live(); len(got) != 0 {
		t.Fatalf("Live after StopAll = %v; want empty", got)
	}
	for i := 0; i < n; i++ {
		id := fmt.Sprintf("worker-%d", i)
		ev := rec.waitExit(t, id)
		if ev.reason != ReasonDaemonShutdown {
			t.Fatalf("%s reason = %q; want %q", id, ev.reason, ReasonDaemonShutdown)
		}
	}
	if orphans := sup.OrphanScan(pidAlive); len(orphans) != 0 {
		t.Fatalf("OrphanScan found live pids after StopAll: %v", orphans)
	}
}

func TestDoubleStopAndStopAfterExitAreNoOps(t *testing.T) {
	sup, rec := newTestSupervisor(t, 200)
	if err := sup.Spawn("idem", testSpec(t, "echo ready; sleep 30")); err != nil {
		t.Fatalf("Spawn: %v", err)
	}
	rec.waitOutputContains(t, "idem", "ready")
	if err := sup.Stop("idem"); err != nil {
		t.Fatalf("first Stop: %v", err)
	}
	if err := sup.Stop("idem"); err != nil {
		t.Fatalf("double Stop: %v; want clean no-op", err)
	}
	rec.waitExit(t, "idem")
	if err := sup.Stop("idem"); err != nil {
		t.Fatalf("Stop after exit: %v; want clean no-op", err)
	}
	if err := sup.Stop("never-existed"); err != nil {
		t.Fatalf("Stop of unknown id: %v; want clean no-op", err)
	}
	time.Sleep(50 * time.Millisecond)
	if n := rec.exitCount("idem"); n != 1 {
		t.Fatalf("OnExit fired %d times; want exactly 1", n)
	}
}

func TestSpawnConflictIsTyped(t *testing.T) {
	sup, rec := newTestSupervisor(t, 200)
	if err := sup.Spawn("dup", testSpec(t, "echo ready; sleep 30")); err != nil {
		t.Fatalf("Spawn: %v", err)
	}
	rec.waitOutputContains(t, "dup", "ready")
	err := sup.Spawn("dup", testSpec(t, "true"))
	var conflict *ConflictError
	if !errors.As(err, &conflict) || conflict.ID != "dup" {
		t.Fatalf("second Spawn = %v; want *ConflictError{dup}", err)
	}
	// The id becomes reusable once the first process has been retired.
	if err := sup.Stop("dup"); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	rec.waitExit(t, "dup")
	if err := sup.Spawn("dup", testSpec(t, "true")); err != nil {
		t.Fatalf("re-Spawn after exit: %v; want id reusable", err)
	}
}

func TestNotFoundIsTyped(t *testing.T) {
	sup, _ := newTestSupervisor(t, 200)
	var notFound *NotFoundError
	if err := sup.Input("ghost", []byte("x")); !errors.As(err, &notFound) {
		t.Fatalf("Input = %v; want *NotFoundError", err)
	}
	if err := sup.Resize("ghost", platform.PTYSize{Rows: 1, Cols: 1}); !errors.As(err, &notFound) {
		t.Fatalf("Resize = %v; want *NotFoundError", err)
	}
	if err := sup.Signal("ghost", syscall.SIGTERM); !errors.As(err, &notFound) {
		t.Fatalf("Signal = %v; want *NotFoundError", err)
	}
	if sup.Alive("ghost") {
		t.Fatal("Alive(ghost) = true")
	}
}

func TestEnvHygieneChildSeesOnlySpecEnv(t *testing.T) {
	sup, rec := newTestSupervisor(t, 0)
	spec := platform.PTYSpec{
		Argv: []string{"/usr/bin/env"},
		Dir:  t.TempDir(),
		Env:  []string{"AMUX_B5_A=alpha", "AMUX_B5_B=beta"},
		Size: platform.PTYSize{Rows: 24, Cols: 80},
	}
	if err := sup.Spawn("env", spec); err != nil {
		t.Fatalf("Spawn: %v", err)
	}
	ev := rec.waitExit(t, "env")
	if ev.exit.Code != 0 {
		t.Fatalf("env exit = %+v; want Code 0", ev.exit)
	}
	rec.waitOutputContains(t, "env", "AMUX_B5_B=beta")

	got := map[string]bool{}
	for _, line := range strings.FieldsFunc(rec.output("env"), func(r rune) bool { return r == '\r' || r == '\n' }) {
		if line != "" {
			got[line] = true
		}
	}
	want := map[string]bool{"AMUX_B5_A=alpha": true, "AMUX_B5_B=beta": true}
	if len(got) != len(want) {
		t.Fatalf("child environment = %v; want exactly %v (no daemon environment may leak)", got, want)
	}
	for k := range want {
		if !got[k] {
			t.Fatalf("child environment missing %q; got %v", k, got)
		}
	}
}

func TestEnvHygieneNilEnvMeansEmpty(t *testing.T) {
	sup, rec := newTestSupervisor(t, 0)
	spec := platform.PTYSpec{
		Argv: []string{"/usr/bin/env"},
		Dir:  t.TempDir(),
		Env:  nil, // must NOT fall back to os.Environ()
		Size: platform.PTYSize{Rows: 24, Cols: 80},
	}
	if err := sup.Spawn("empty-env", spec); err != nil {
		t.Fatalf("Spawn: %v", err)
	}
	ev := rec.waitExit(t, "empty-env")
	if ev.exit.Code != 0 {
		t.Fatalf("env exit = %+v; want Code 0", ev.exit)
	}
	if out := strings.TrimSpace(rec.output("empty-env")); strings.Contains(out, "=") {
		t.Fatalf("child with nil Env saw variables: %q (ambient daemon environment leaked)", out)
	}
}

func TestOnExitExactlyOnceUnderRacingStopAndNaturalExit(t *testing.T) {
	sup, rec := newTestSupervisor(t, 50)
	const iterations = 20
	ids := make([]string, 0, iterations)
	var wg sync.WaitGroup
	for i := 0; i < iterations; i++ {
		id := fmt.Sprintf("race-%d", i)
		ids = append(ids, id)
		if err := sup.Spawn(id, testSpec(t, "sleep 0.02")); err != nil {
			t.Fatalf("Spawn %s: %v", id, err)
		}
		wg.Add(1)
		go func(id string) {
			defer wg.Done()
			// Race Stop against the child's natural ~20ms exit.
			time.Sleep(10 * time.Millisecond)
			_ = sup.Stop(id)
		}(id)
	}
	wg.Wait()
	for _, id := range ids {
		ev := rec.waitExit(t, id)
		if ev.reason != ReasonExited && ev.reason != ReasonStopped && ev.reason != ReasonSignaled {
			t.Fatalf("%s reason = %q; want one of exited/stopped/signaled", id, ev.reason)
		}
	}
	time.Sleep(100 * time.Millisecond) // let any duplicate event surface
	for _, id := range ids {
		if n := rec.exitCount(id); n != 1 {
			t.Fatalf("%s: OnExit fired %d times; want exactly 1", id, n)
		}
	}
	if orphans := sup.OrphanScan(pidAlive); len(orphans) != 0 {
		t.Fatalf("OrphanScan found live pids: %v", orphans)
	}
}

func TestConcurrentSpawnInputStopStress(t *testing.T) {
	sup, rec := newTestSupervisor(t, 100)
	const workers = 6
	const perWorker = 3
	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			for i := 0; i < perWorker; i++ {
				id := fmt.Sprintf("stress-%d-%d", w, i)
				if err := sup.Spawn(id, testSpec(t, "cat")); err != nil {
					t.Errorf("Spawn %s: %v", id, err)
					return
				}
				_ = sup.Input(id, []byte("ping\n"))
				_ = sup.Resize(id, platform.PTYSize{Rows: 30, Cols: 90})
				_ = sup.Stop(id)
			}
		}(w)
	}
	wg.Wait()
	for w := 0; w < workers; w++ {
		for i := 0; i < perWorker; i++ {
			rec.waitExit(t, fmt.Sprintf("stress-%d-%d", w, i))
		}
	}
	if got := sup.Live(); len(got) != 0 {
		t.Fatalf("Live after stress = %v; want empty", got)
	}
	if orphans := sup.OrphanScan(pidAlive); len(orphans) != 0 {
		t.Fatalf("OrphanScan found live pids: %v", orphans)
	}
}
