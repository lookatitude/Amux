package context

import (
	"fmt"

	"github.com/amux-run/amux/internal/platform"
)

// CwdProber resolves a process's current working directory. Linux implements
// it via /proc/<pid>/cwd; other platforms fail closed (ADR-0006). Tests
// inject fakes — the /proc behavior itself is Linux-only runtime evidence,
// deferred to a Linux host.
type CwdProber interface {
	Cwd(pid int) (string, error)
}

// CommProber resolves a process's short command name. Linux implements it via
// /proc/<pid>/comm; other platforms fail closed.
type CommProber interface {
	Comm(pid int) (string, error)
}

// ForegroundCollector reads a pane's foreground process context through the
// platform.ProcessInspector seam (frozen in ADR-0006). Inspector failures are
// fail-closed errors; the command-name lookup is advisory because the
// foreground process may exit between the two probes.
type ForegroundCollector struct {
	Inspector platform.ProcessInspector
	Comm      CommProber // optional; nil leaves the command name empty
}

// Collect returns the foreground process group leader of the PTY behind
// masterFD and, when a CommProber is wired and succeeds, its command name.
func (f ForegroundCollector) Collect(masterFD uintptr) (pid int, cmd string, err error) {
	if f.Inspector == nil {
		return 0, "", fmt.Errorf("context: foreground collector: %w", platform.ErrUnsupportedPlatform)
	}
	pid, err = f.Inspector.ForegroundPID(masterFD)
	if err != nil {
		return 0, "", fmt.Errorf("context: foreground pid: %w", err)
	}
	if f.Comm != nil {
		if c, cerr := f.Comm.Comm(pid); cerr == nil {
			cmd = c
		}
	}
	return pid, cmd, nil
}
