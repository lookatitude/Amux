//go:build linux

package platform

import (
	"fmt"
	"os"
	"strconv"

	"golang.org/x/sys/unix"
)

// linuxDescriptorLaunch implements race-safe, descriptor-bound executable launch
// (spec "Linearizable hook launch contract"; PRD F9). It opens the executable
// with openat2 using RESOLVE_NO_SYMLINKS|RESOLVE_NO_MAGICLINKS|RESOLVE_BENEATH so
// no symlink or /proc magic-link can redirect resolution, captures the resolved
// inode's dev/ino for digest+epoch validation, and then execs THAT descriptor
// (fexecve semantics via /proc/self/fd) so a rename or byte-replacement between
// validation and exec cannot substitute a different object. Path-only
// revalidation is explicitly insufficient (spec).
//
// DEFERRED RUNTIME EVIDENCE (Linux host required; cannot run on the macOS author
// host — openat2 and the /proc/self/fd exec path are Linux-only): this type
// compiles under GOOS=linux; its race-safety must be proven on a Linux host with
// the harness in spikes/launch, which races symlink/rename/byte/config/root
// replacement against the launch. See
// docs/adr/0006-platform-interfaces.md §"Deferred Linux evidence".
type linuxDescriptorLaunch struct{}

// NewLinuxDescriptorLaunch returns the Linux descriptor-bound launcher.
func NewLinuxDescriptorLaunch() DescriptorLaunch { return linuxDescriptorLaunch{} }

func (linuxDescriptorLaunch) OpenBound(dirFD int, path string) (int, FSIdentity, error) {
	how := &unix.OpenHow{
		Flags:   unix.O_RDONLY | unix.O_CLOEXEC,
		Resolve: unix.RESOLVE_NO_SYMLINKS | unix.RESOLVE_NO_MAGICLINKS | unix.RESOLVE_BENEATH,
	}
	fd, err := unix.Openat2(dirFD, path, how)
	if err != nil {
		return -1, FSIdentity{}, fmt.Errorf("openat2 %q: %w", path, err)
	}
	var st unix.Stat_t
	if err := unix.Fstat(fd, &st); err != nil {
		_ = unix.Close(fd)
		return -1, FSIdentity{}, fmt.Errorf("fstat bound fd: %w", err)
	}
	return fd, FSIdentity{Dev: uint64(st.Dev), Ino: uint64(st.Ino)}, nil
}

// LaunchBound execs the already-open, already-validated descriptor. It uses the
// /proc/self/fd/<fd> path with the fd kept open across StartProcess, which the
// kernel resolves to the exact open file description — the Go-idiomatic fexecve.
// The caller MUST have re-checked digest+epoch against the fd's FSIdentity
// immediately before calling this (the linearization point).
func (linuxDescriptorLaunch) LaunchBound(fd int, argv []string, env []string, spec LaunchSpec) (int, error) {
	if len(argv) == 0 {
		return -1, fmt.Errorf("empty argv")
	}
	// Ensure the descriptor is inheritable by the child so /proc/self/fd is
	// valid in the forked-but-not-yet-exec'd child.
	if _, err := unix.FcntlInt(uintptr(fd), unix.F_SETFD, 0); err != nil {
		return -1, fmt.Errorf("clear cloexec: %w", err)
	}
	attr := &os.ProcAttr{
		Dir:   spec.Dir,
		Env:   env,
		Files: []*os.File{nil, nil, nil}, // no inherited std streams by default
		Sys: &unix.SysProcAttr{
			Setpgid:   spec.Containment.NewProcessGroup,
			Pdeathsig: unix.SIGKILL,
		},
	}
	procPath := "/proc/self/fd/" + strconv.Itoa(fd)
	proc, err := os.StartProcess(procPath, argv, attr)
	if err != nil {
		return -1, fmt.Errorf("execveat via %s: %w", procPath, err)
	}
	return proc.Pid, nil
}
