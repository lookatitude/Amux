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
//   - after KillTree the grandchild is no longer alive (kill(pid,0) -> ESRCH),
//   - the cgroup is removed.
//
// FAIL (exit 1) if the grandchild survives, proving the mechanism is required.
package main

import (
	"bufio"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"golang.org/x/sys/unix"

	"github.com/amux-run/amux/internal/platform"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "FAIL:", err)
		os.Exit(1)
	}
	fmt.Println("PASS: grandchild reaped by cgroup KillTree")
}

func run() error {
	root := os.Getenv("AMUX_CGROUP_ROOT")
	if root == "" {
		return fmt.Errorf("set AMUX_CGROUP_ROOT to a delegated, writable cgroup-v2 subtree (deferred evidence: see file header)")
	}
	cg := filepath.Join(root, "spike")
	if err := os.MkdirAll(cg, 0o755); err != nil {
		return fmt.Errorf("mkdir cgroup %s: %w", cg, err)
	}
	defer os.Remove(cg)

	// A shell that double-forks a grandchild which changes its process group
	// (setsid) and sleeps, then prints the grandchild PID and exits — so the
	// grandchild is reparented to init and has escaped the process group.
	// Emit the PID through the capture pipe, then redirect the long-lived
	// grandchild before exec. Otherwise sleep inherits stdout, Scanner never
	// observes EOF, and the harness deadlocks before it can call cgroup.kill.
	script := `setsid sh -c 'echo GC:$$; exec sleep 300 >/dev/null 2>&1' & echo done`
	cmd := exec.Command("/bin/sh", "-c", script)
	cmd.SysProcAttr = platform.ContainedSysProcAttr(platform.ContainmentSpec{NewProcessGroup: true, Label: "spike"})
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start child: %w", err)
	}
	// Enrol the child into the cgroup; grandchildren inherit membership on fork.
	if err := os.WriteFile(filepath.Join(cg, "cgroup.procs"), []byte(strconv.Itoa(cmd.Process.Pid)), 0o644); err != nil {
		return fmt.Errorf("enrol child in cgroup: %w", err)
	}

	var gcPID int
	sc := bufio.NewScanner(stdout)
	for sc.Scan() {
		line := sc.Text()
		if strings.HasPrefix(line, "GC:") {
			gcPID, _ = strconv.Atoi(strings.TrimPrefix(line, "GC:"))
		}
	}
	_ = cmd.Wait()
	if gcPID == 0 {
		return fmt.Errorf("did not capture grandchild PID")
	}
	time.Sleep(50 * time.Millisecond)
	if !alive(gcPID) {
		return fmt.Errorf("grandchild %d already gone before KillTree (inconclusive)", gcPID)
	}

	// Simulate daemon death cleanup: cgroup.kill reaps the whole subtree.
	if err := os.WriteFile(filepath.Join(cg, "cgroup.kill"), []byte("1"), 0o644); err != nil {
		return fmt.Errorf("cgroup.kill: %w", err)
	}
	time.Sleep(50 * time.Millisecond)
	if alive(gcPID) {
		_ = unix.Kill(gcPID, unix.SIGKILL)
		return fmt.Errorf("grandchild %d survived cgroup KillTree", gcPID)
	}
	return nil
}

func alive(pid int) bool {
	err := unix.Kill(pid, 0)
	return err == nil || err == unix.EPERM
}
