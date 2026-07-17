//go:build linux

package platform

import (
	"fmt"
	"syscall"

	"golang.org/x/sys/unix"
)

// linuxProcessInspector reads live process context via the PTY master and
// standard process signalling — the production ProcessInspector for the B10
// pane-context collectors (PRD F8). It never owns or alters a process: both
// probes are read-only queries.
type linuxProcessInspector struct{}

// NewLinuxProcessInspector returns the TIOCGPGRP-backed inspector.
func NewLinuxProcessInspector() ProcessInspector { return linuxProcessInspector{} }

// ForegroundPID returns the foreground process group leader of the PTY whose
// master descriptor is ptyFD.
func (linuxProcessInspector) ForegroundPID(ptyFD uintptr) (int, error) {
	pgrp, err := unix.IoctlGetInt(int(ptyFD), unix.TIOCGPGRP)
	if err != nil {
		return 0, fmt.Errorf("platform: TIOCGPGRP: %w", err)
	}
	return pgrp, nil
}

// Alive reports whether pid is a live process (signal 0 probe; EPERM still
// proves existence).
func (linuxProcessInspector) Alive(pid int) (bool, error) {
	if pid <= 0 {
		return false, nil
	}
	err := syscall.Kill(pid, 0)
	switch err {
	case nil, syscall.EPERM:
		return true, nil
	case syscall.ESRCH:
		return false, nil
	default:
		return false, err
	}
}
