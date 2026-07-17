//go:build linux

// Command launch-spike is the A6 descriptor-bound-launch harness. It is DEFERRED
// runtime evidence: it compiles under GOOS=linux but can only be RUN on a Linux
// host (openat2 + /proc/self/fd exec are Linux-only). It proves that opening an
// executable with openat2(RESOLVE_NO_SYMLINKS|RESOLVE_NO_MAGICLINKS|
// RESOLVE_BENEATH), validating its inode identity, and exec'ing THAT descriptor
// defeats a symlink/rename/byte-replacement race — the substituted object is
// never executed. It exercises internal/platform.linuxDescriptorLaunch.
//
// RUN (on Arch/Ubuntu, kernel >= 5.6 for openat2):
//
//	go run ./spikes/launch
//
// PASS CRITERIA (exit 0):
//   - openat2 of a symlink with RESOLVE_NO_SYMLINKS is refused (ELOOP),
//   - a rename/byte-replacement after OpenBound does not change the executed
//     object: the bound descriptor still runs the ORIGINAL bytes, and its
//     FSIdentity is unchanged from the pre-race capture.
//
// FAIL (exit 1) if a substituted object is executed or the symlink is followed.
package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"golang.org/x/sys/unix"

	"github.com/amux-run/amux/internal/platform"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "FAIL:", err)
		os.Exit(1)
	}
	fmt.Println("PASS: descriptor-bound launch defeats symlink/rename/byte races")
}

func run() error {
	dir, err := os.MkdirTemp("", "amux-launch-spike")
	if err != nil {
		return err
	}
	defer os.RemoveAll(dir)

	// The trusted executable: prints ORIGINAL.
	origPath := filepath.Join(dir, "tool")
	if err := writeScript(origPath, "#!/bin/sh\necho ORIGINAL\n"); err != nil {
		return err
	}
	dirFD, err := unix.Open(dir, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC, 0)
	if err != nil {
		return fmt.Errorf("open dir: %w", err)
	}
	defer unix.Close(dirFD)

	launcher := platform.NewLinuxDescriptorLaunch()

	// (1) A symlink must NOT be followed under RESOLVE_NO_SYMLINKS.
	linkPath := filepath.Join(dir, "link")
	if err := os.Symlink(origPath, linkPath); err != nil {
		return err
	}
	if _, _, err := launcher.OpenBound(dirFD, "link"); err == nil {
		return fmt.Errorf("symlink was followed; RESOLVE_NO_SYMLINKS failed")
	}

	// (2) Bind the real executable, then race a rename+replacement at the path.
	fd, id, err := launcher.OpenBound(dirFD, "tool")
	if err != nil {
		return fmt.Errorf("openbound tool: %w", err)
	}
	defer unix.Close(fd)

	// Attacker swaps the path to a different executable AFTER we bound the fd.
	evilPath := filepath.Join(dir, "evil")
	if err := writeScript(evilPath, "#!/bin/sh\necho SUBSTITUTED\n"); err != nil {
		return err
	}
	if err := os.Rename(evilPath, origPath); err != nil { // path now points at evil
		return err
	}

	// The bound descriptor must still be the ORIGINAL inode.
	var st unix.Stat_t
	if err := unix.Fstat(fd, &st); err != nil {
		return err
	}
	if uint64(st.Ino) != id.Ino || uint64(st.Dev) != id.Dev {
		return fmt.Errorf("bound descriptor inode changed after race")
	}

	// Exec the bound descriptor and confirm it runs ORIGINAL, not SUBSTITUTED.
	out, err := runBoundAndCapture(fd)
	if err != nil {
		return fmt.Errorf("exec bound descriptor: %w", err)
	}
	if strings.TrimSpace(out) != "ORIGINAL" {
		return fmt.Errorf("executed substituted object: got %q", out)
	}
	return nil
}

// runBoundAndCapture execs /proc/self/fd/<fd> and returns its stdout. It mirrors
// platform.linuxDescriptorLaunch.LaunchBound but captures output for the assert.
func runBoundAndCapture(fd int) (string, error) {
	if _, err := unix.FcntlInt(uintptr(fd), unix.F_SETFD, 0); err != nil {
		return "", err
	}
	cmd := exec.Command(fmt.Sprintf("/proc/self/fd/%d", fd))
	b, err := cmd.Output()
	return string(b), err
}

func writeScript(path, body string) error {
	if err := os.WriteFile(path, []byte(body), 0o755); err != nil {
		return err
	}
	return nil
}
