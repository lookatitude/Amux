//go:build darwin || linux

package pty

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"syscall"

	"github.com/creack/pty"

	"github.com/amux-run/amux/internal/platform"
)

// unixPTY is the platform.PTY implementation for Unix hosts, wrapping
// github.com/creack/pty (the mechanism spikes/pty proved on the author host).
// Linux is the supported product platform; the darwin build exists so the
// author host can run the portable test suite (ADR-0006).
type unixPTY struct{}

// New returns the production platform.PTY implementation.
func New() platform.PTY { return unixPTY{} }

// defaultSize is applied when the caller leaves PTYSpec.Size zero; a 0x0
// terminal confuses most curses programs, so an unset geometry gets the
// conventional 80x24 rather than failing the spawn.
var defaultSize = platform.PTYSize{Rows: 24, Cols: 80}

// Start validates spec fail-closed, then launches spec.Argv on a fresh
// pseudo-terminal: new session (Setsid; creack/pty sets Setctty so the PTY
// becomes the controlling terminal, which also makes the child its own
// process-group leader — satisfying ContainmentSpec.NewProcessGroup), explicit
// environment only (spec.Env verbatim; nil or empty means an EMPTY child
// environment, never the daemon's os.Environ()), and spec.Dir as the working
// directory.
func (unixPTY) Start(spec platform.PTYSpec) (platform.PTYHandle, error) {
	if len(spec.Argv) == 0 {
		return nil, fmt.Errorf("pty: start: empty Argv")
	}
	path := spec.Argv[0]
	if filepath.IsAbs(path) {
		if _, err := os.Stat(path); err != nil {
			return nil, fmt.Errorf("pty: start: argv[0] %q: %w", path, err)
		}
	} else {
		resolved, err := exec.LookPath(path)
		if err != nil {
			return nil, fmt.Errorf("pty: start: argv[0] %q not resolvable: %w", path, err)
		}
		path = resolved
	}
	if spec.Dir == "" {
		return nil, fmt.Errorf("pty: start: empty Dir (an explicit working directory is required)")
	}
	if info, err := os.Stat(spec.Dir); err != nil {
		return nil, fmt.Errorf("pty: start: Dir %q: %w", spec.Dir, err)
	} else if !info.IsDir() {
		return nil, fmt.Errorf("pty: start: Dir %q is not a directory", spec.Dir)
	}

	cmd := exec.Command(path, spec.Argv[1:]...)
	// Explicit environment only: exec.Cmd inherits os.Environ() when Env is
	// nil, so an empty spec.Env must be materialized as an empty (non-nil)
	// slice to keep the daemon environment out of the child.
	cmd.Env = append([]string{}, spec.Env...)
	cmd.Dir = spec.Dir
	// New session + parent-death fast path where available. creack/pty's
	// StartWithSize forces Setsid/Setctty on this same SysProcAttr, making the
	// fresh PTY the controlling terminal and the child a session (and thus
	// process-group) leader.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}

	size := spec.Size
	if size.Rows == 0 || size.Cols == 0 {
		size = defaultSize
	}
	master, err := pty.StartWithSize(cmd, &pty.Winsize{Rows: size.Rows, Cols: size.Cols})
	if err != nil {
		return nil, fmt.Errorf("pty: start %q: %w", path, err)
	}
	return &unixHandle{master: master, cmd: cmd, pid: cmd.Process.Pid}, nil
}

// unixHandle is one live pseudo-terminal session. It is the only type that
// touches creack/pty; callers see platform.PTYHandle.
type unixHandle struct {
	master *os.File
	cmd    *exec.Cmd
	pid    int

	waitOnce sync.Once
	waitExit platform.PTYExit
	waitErr  error

	closeOnce sync.Once
}

// Read yields master-side output bytes. After the child (and every slave-side
// holder) is gone the read fails with EOF/EIO, which callers treat as
// end-of-output.
func (h *unixHandle) Read(p []byte) (int, error) { return h.master.Read(p) }

// Write injects input bytes on the master side.
func (h *unixHandle) Write(p []byte) (int, error) { return h.master.Write(p) }

// Resize applies new geometry via TIOCSWINSZ; the kernel delivers SIGWINCH to
// the foreground process group implicitly.
func (h *unixHandle) Resize(size platform.PTYSize) error {
	if err := pty.Setsize(h.master, &pty.Winsize{Rows: size.Rows, Cols: size.Cols}); err != nil {
		return fmt.Errorf("pty: resize: %w", err)
	}
	return nil
}

// Signal sends sig to the child's whole process group (kill(-pgid)) so the
// entire job tree receives it, not just the immediate child. The child is a
// session leader (Setsid), so its pgid equals its pid.
func (h *unixHandle) Signal(sig os.Signal) error {
	s, ok := sig.(syscall.Signal)
	if !ok {
		return fmt.Errorf("pty: signal: unsupported signal type %T", sig)
	}
	if err := syscall.Kill(-h.pid, s); err != nil {
		return fmt.Errorf("pty: signal %v to group %d: %w", s, h.pid, err)
	}
	return nil
}

// Wait blocks until the child terminates and reaps it exactly once,
// classifying the termination: a normal exit yields PTYExit{Code}, a signal
// death yields PTYExit{Signal: "SIGXXX"} whose Code must not be trusted.
// Extra calls return the cached first result instead of double-reaping.
func (h *unixHandle) Wait() (platform.PTYExit, error) {
	h.waitOnce.Do(func() {
		err := h.cmd.Wait()
		state := h.cmd.ProcessState
		if state == nil {
			h.waitErr = fmt.Errorf("pty: wait: no process state: %w", err)
			return
		}
		ws, ok := state.Sys().(syscall.WaitStatus)
		if !ok {
			h.waitErr = fmt.Errorf("pty: wait: unexpected wait status type %T", state.Sys())
			return
		}
		switch {
		case ws.Exited():
			h.waitExit = platform.PTYExit{Code: ws.ExitStatus()}
		case ws.Signaled():
			h.waitExit = platform.PTYExit{Code: int(ws.Signal()), Signal: signalName(ws.Signal())}
		default:
			h.waitErr = fmt.Errorf("pty: wait: child neither exited nor signaled (status %#x)", uint32(ws))
		}
	})
	return h.waitExit, h.waitErr
}

// MasterFD exposes the master descriptor solely for
// ProcessInspector.ForegroundPID queries.
func (h *unixHandle) MasterFD() uintptr { return h.master.Fd() }

// PID returns the child process id. It is not part of platform.PTYHandle; the
// Supervisor discovers it via an unexported interface assertion for orphan
// accounting and containment enrollment.
func (h *unixHandle) PID() int { return h.pid }

// Close releases the master descriptor. Idempotent, and it never kills the
// child — termination is Signal/containment territory (ADR-0004: detach never
// stops a process).
func (h *unixHandle) Close() error {
	h.closeOnce.Do(func() { _ = h.master.Close() })
	return nil
}

// signalName renders a syscall.Signal as its conventional "SIGXXX" name for
// PTYExit.Signal. syscall.Signal.String() yields prose ("killed"), so the
// portable POSIX set is mapped explicitly; anything else falls back to a
// numeric form.
func signalName(s syscall.Signal) string {
	switch s {
	case syscall.SIGHUP:
		return "SIGHUP"
	case syscall.SIGINT:
		return "SIGINT"
	case syscall.SIGQUIT:
		return "SIGQUIT"
	case syscall.SIGABRT:
		return "SIGABRT"
	case syscall.SIGKILL:
		return "SIGKILL"
	case syscall.SIGUSR1:
		return "SIGUSR1"
	case syscall.SIGUSR2:
		return "SIGUSR2"
	case syscall.SIGSEGV:
		return "SIGSEGV"
	case syscall.SIGPIPE:
		return "SIGPIPE"
	case syscall.SIGALRM:
		return "SIGALRM"
	case syscall.SIGTERM:
		return "SIGTERM"
	default:
		return fmt.Sprintf("SIG%d", int(s))
	}
}
