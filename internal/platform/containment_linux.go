//go:build linux

package platform

import (
	"fmt"
	"os"
	"path/filepath"
	"syscall"
	"time"

	"golang.org/x/sys/unix"
)

// linuxContainment implements daemon-death descendant containment with a
// cgroup-v2 subtree plus a PR_SET_PDEATHSIG fast path (ADR-0006 §containment).
// The cgroup is the robust mechanism: even a double-forked grandchild that
// escapes its process group and reparents to init remains a member of the
// cgroup, so writing "1" to cgroup.kill reaps the entire subtree atomically.
// PR_SET_PDEATHSIG(SIGKILL) is a best-effort fast path for the common
// direct-child case.
//
// DEFERRED RUNTIME EVIDENCE (Linux host required, cannot run on the macOS author
// host): this type compiles under GOOS=linux but its kill-tree behavior must be
// proven on a cgroup-v2 Linux host with the harness in
// spikes/containment. See docs/adr/0006-platform-interfaces.md §"Deferred Linux
// evidence" for the exact commands and pass criteria.
type linuxContainment struct {
	// cgroupRoot is the delegated cgroup-v2 mount subtree amuxd may write to,
	// e.g. /sys/fs/cgroup/amux.<pid>. Empty means cgroup containment is
	// unavailable and Prepare falls back to pdeathsig only (fail-closed noted).
	cgroupRoot string
}

// NewLinuxContainment returns a containment backed by the given delegated
// cgroup-v2 root. If cgroupRoot is empty, only the pdeathsig fast path is used
// and KillTree cannot guarantee grandchild reaping — callers must treat that as
// reduced containment (ADR-0006 fail-closed fallback).
func NewLinuxContainment(cgroupRoot string) Containment {
	return &linuxContainment{cgroupRoot: cgroupRoot}
}

func (c *linuxContainment) Prepare(spec ContainmentSpec) (ContainmentHandle, error) {
	h := &linuxContainmentHandle{cgroupFD: -1}
	if c.cgroupRoot != "" {
		dir := filepath.Join(c.cgroupRoot, sanitizeLabel(spec.Label))
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return nil, fmt.Errorf("create cgroup %s: %w", dir, err)
		}
		h.cgroupDir = dir
		fd, err := unix.Open(dir, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC, 0)
		if err != nil {
			_ = os.Remove(dir)
			return nil, fmt.Errorf("open cgroup %s: %w", dir, err)
		}
		h.cgroupFD = fd
	}
	return h, nil
}

// SysProcAttr returns the non-cgroup SysProcAttr a caller should apply to a
// contained child: a new process group (so signals can target the whole group)
// and SIGKILL parent-death signaling. Production PTY launches add the prepared
// CgroupFD through SysProcAttr.UseCgroupFD so membership is atomic at clone.
func ContainedSysProcAttr(spec ContainmentSpec) *syscall.SysProcAttr {
	attr := &syscall.SysProcAttr{Pdeathsig: syscall.SIGKILL}
	if spec.NewProcessGroup {
		attr.Setpgid = true
	}
	return attr
}

type linuxContainmentHandle struct {
	cgroupDir string
	cgroupFD  int
}

// CgroupFD exposes the prepared cgroup directory solely to the Supervisor's
// optional pre-enrollment seam. The PTY launcher passes it to clone3 via
// SysProcAttr.UseCgroupFD, closing the fork-before-AddPID escape window.
func (h *linuxContainmentHandle) CgroupFD() int { return h.cgroupFD }

// AddPID is the fallback for a PTY seam that cannot consume CgroupFD. The
// supported Linux PTY path uses atomic clone-time placement and skips this
// post-start operation because it cannot close the fork-before-enroll race.
func (h *linuxContainmentHandle) AddPID(pid int) error {
	if h.cgroupDir == "" {
		return nil // pdeathsig-only mode; nothing to enroll
	}
	return os.WriteFile(filepath.Join(h.cgroupDir, "cgroup.procs"), []byte(fmt.Sprintf("%d", pid)), 0o644)
}

// KillTree reaps the entire descendant subtree. With a cgroup it writes "1" to
// cgroup.kill (atomic SIGKILL of all members incl. reparented grandchildren);
// without one it can only signal the known process group, which is why cgroup
// mode is required for the full containment guarantee.
func (h *linuxContainmentHandle) KillTree() error {
	if h.cgroupDir == "" {
		return fmt.Errorf("containment: no cgroup; cannot guarantee grandchild reaping")
	}
	return os.WriteFile(filepath.Join(h.cgroupDir, "cgroup.kill"), []byte("1"), 0o644)
}

// Close removes the (now-empty) cgroup directory. It retries briefly because the
// kernel removes the cgroup only once all members have exited.
func (h *linuxContainmentHandle) Close() error {
	if h.cgroupFD >= 0 {
		_ = unix.Close(h.cgroupFD)
		h.cgroupFD = -1
	}
	if h.cgroupDir == "" {
		return nil
	}
	var err error
	for i := 0; i < 50; i++ {
		if err = os.Remove(h.cgroupDir); err == nil {
			return nil
		}
		time.Sleep(2 * time.Millisecond)
	}
	return err
}

func sanitizeLabel(s string) string {
	if s == "" {
		return "child"
	}
	out := make([]rune, 0, len(s))
	for _, r := range s {
		if r == '/' || r == 0 {
			r = '_'
		}
		out = append(out, r)
	}
	return string(out)
}
