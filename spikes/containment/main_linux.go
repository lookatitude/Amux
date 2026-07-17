//go:build linux

// Command containment-spike is the A6 Linux descendant-containment harness. It
// is DEFERRED runtime evidence: it compiles under GOOS=linux but can only be RUN
// on a cgroup-v2 Linux host with a delegated, writable cgroup subtree. It proves
// that killing the "daemon" reaps a double-forked grandchild that has escaped
// its process group and reparented to init, using the same cgroup-v2 mechanism
// internal/platform.linuxContainment implements.
//
// RUN (on Arch/Ubuntu, cgroup v2):
//
//	# as a user with a delegated cgroup subtree, or as root:
//	sudo mkdir -p /sys/fs/cgroup/amux-spike
//	sudo AMUX_CGROUP_ROOT=/sys/fs/cgroup/amux-spike go run ./spikes/containment
//
// PASS CRITERIA (exit 0):
//   - the grandchild PID is captured,
//   - after KillTree the grandchild is no longer executable (ESRCH or a
//     killed-but-not-yet-reaped zombie in /proc),
//   - the cgroup is removed.
//
// FAIL (exit 1) if the grandchild survives, proving the mechanism is required.
package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"golang.org/x/sys/unix"

	"github.com/amux-run/amux/internal/platform"
	amuxpty "github.com/amux-run/amux/internal/pty"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "FAIL:", err)
		os.Exit(1)
	}
	fmt.Println("PASS: grandchild terminated by cgroup KillTree")
}

func run() error {
	root := os.Getenv("AMUX_CGROUP_ROOT")
	if root == "" {
		return fmt.Errorf("set AMUX_CGROUP_ROOT to a delegated, writable cgroup-v2 subtree (deferred evidence: see file header)")
	}
	cg := filepath.Join(root, "spike")
	output := make(chan []byte, 16)
	exited := make(chan struct{}, 1)
	sup, err := amuxpty.NewSupervisor(amuxpty.SupervisorConfig{
		PTY:         amuxpty.New(),
		Containment: amuxpty.PlatformContainment(root),
		GraceMS:     50,
		OnOutput: func(_ string, data []byte) {
			output <- data
		},
		OnExit: func(_ string, _ platform.PTYExit, _ string) {
			exited <- struct{}{}
		},
	})
	if err != nil {
		return fmt.Errorf("new supervisor: %w", err)
	}
	defer sup.StopAll()

	// The parent and its setsid grandchild both ignore SIGTERM, forcing the
	// Supervisor through its real cgroup KillTree escalation. The grandchild
	// redirects after publishing its PID so it cannot hold the PTY open.
	script := `trap "" TERM; setsid sh -c 'echo GC:$$; exec sleep 300 >/dev/null 2>&1' & wait`
	if err := sup.Spawn("spike", platform.PTYSpec{
		Argv: []string{"/bin/sh", "-c", script},
		Dir:  "/",
		Env:  []string{"PATH=/usr/bin:/bin"},
		Size: platform.PTYSize{Rows: 24, Cols: 80},
		Containment: platform.ContainmentSpec{
			NewProcessGroup: true,
			Label:           "spike",
		},
	}); err != nil {
		return fmt.Errorf("spawn contained child: %w", err)
	}
	var gcPID int
	var captured strings.Builder
	captureDeadline := time.NewTimer(5 * time.Second)
	defer captureDeadline.Stop()
	for gcPID == 0 {
		select {
		case data := <-output:
			captured.Write(data)
			for _, field := range strings.Fields(captured.String()) {
				if strings.HasPrefix(field, "GC:") {
					gcPID, _ = strconv.Atoi(strings.TrimPrefix(field, "GC:"))
				}
			}
		case <-captureDeadline.C:
			return fmt.Errorf("did not capture grandchild PID; output=%q", captured.String())
		}
	}
	if !alive(gcPID) {
		return fmt.Errorf("grandchild %d already gone before KillTree (inconclusive)", gcPID)
	}
	if !cgroupContainsPID(cg, gcPID) {
		return fmt.Errorf("grandchild %d was not atomically enrolled in %s", gcPID, cg)
	}

	if err := sup.Stop("spike"); err != nil {
		return fmt.Errorf("stop contained child: %w", err)
	}
	select {
	case <-exited:
	case <-time.After(5 * time.Second):
		return fmt.Errorf("supervisor did not retire after cgroup KillTree")
	}
	deadline := time.Now().Add(2 * time.Second)
	for alive(gcPID) {
		if time.Now().After(deadline) {
			_ = unix.Kill(gcPID, unix.SIGKILL)
			return fmt.Errorf("grandchild %d survived cgroup KillTree", gcPID)
		}
		time.Sleep(10 * time.Millisecond)
	}
	if _, err := os.Stat(cg); !os.IsNotExist(err) {
		return fmt.Errorf("containment cgroup still exists after retirement: %v", err)
	}
	return nil
}

func cgroupContainsPID(cgroupDir string, pid int) bool {
	data, err := os.ReadFile(filepath.Join(cgroupDir, "cgroup.procs"))
	if err != nil {
		return false
	}
	want := strconv.Itoa(pid)
	for _, line := range strings.Fields(string(data)) {
		if line == want {
			return true
		}
	}
	return false
}

func alive(pid int) bool {
	err := unix.Kill(pid, 0)
	if err != nil && err != unix.EPERM {
		return false
	}
	// kill(pid, 0) also succeeds for zombies. A zombie cannot execute and has
	// already been killed by cgroup.kill; whether PID 1 has reaped it yet is an
	// init-system property, not descendant-containment failure.
	stat, readErr := os.ReadFile(filepath.Join("/proc", strconv.Itoa(pid), "stat"))
	if readErr != nil {
		return true // fail closed unless disappearance was proved by kill(0)
	}
	end := strings.LastIndexByte(string(stat), ')')
	if end < 0 {
		return true
	}
	fields := strings.Fields(string(stat[end+1:]))
	if len(fields) == 0 {
		return true
	}
	return fields[0] != "Z" && fields[0] != "X"
}
