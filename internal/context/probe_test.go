package context

import (
	"errors"
	"runtime"
	"testing"

	"github.com/amux-run/amux/internal/platform"
)

// fakeInspector is a deterministic platform.ProcessInspector.
type fakeInspector struct {
	pid   int
	err   error
	alive bool
}

func (f fakeInspector) ForegroundPID(uintptr) (int, error) { return f.pid, f.err }
func (f fakeInspector) Alive(int) (bool, error)            { return f.alive, nil }

// fakeComm is a deterministic CommProber.
type fakeComm struct {
	comm string
	err  error
}

func (f fakeComm) Comm(int) (string, error) { return f.comm, f.err }

func TestForegroundCollector(t *testing.T) {
	fc := ForegroundCollector{
		Inspector: fakeInspector{pid: 42, alive: true},
		Comm:      fakeComm{comm: "vim"},
	}
	pid, cmd, err := fc.Collect(3)
	if err != nil {
		t.Fatalf("collect: %v", err)
	}
	if pid != 42 || cmd != "vim" {
		t.Fatalf("pid=%d cmd=%q, want 42/vim", pid, cmd)
	}
}

func TestForegroundCollectorInspectorErrorFailsClosed(t *testing.T) {
	boom := errors.New("tcgetpgrp failed")
	fc := ForegroundCollector{Inspector: fakeInspector{err: boom}}
	if _, _, err := fc.Collect(3); !errors.Is(err, boom) {
		t.Fatalf("err = %v, want wrapped inspector error", err)
	}
}

func TestForegroundCollectorCommFailureIsAdvisory(t *testing.T) {
	fc := ForegroundCollector{
		Inspector: fakeInspector{pid: 7},
		Comm:      fakeComm{err: errors.New("process gone")},
	}
	pid, cmd, err := fc.Collect(3)
	if err != nil {
		t.Fatalf("comm failure must not fail the collection: %v", err)
	}
	if pid != 7 || cmd != "" {
		t.Fatalf("pid=%d cmd=%q, want 7 with empty cmd", pid, cmd)
	}
}

func TestForegroundCollectorNilInspectorFailsClosed(t *testing.T) {
	if _, _, err := (ForegroundCollector{}).Collect(3); !errors.Is(err, platform.ErrUnsupportedPlatform) {
		t.Fatalf("err = %v, want ErrUnsupportedPlatform", err)
	}
}

func TestProbersFailClosedOffLinux(t *testing.T) {
	if runtime.GOOS == "linux" {
		t.Skip("linux has real /proc probers; fail-closed applies off Linux only")
	}
	if _, err := NewCwdProber().Cwd(1); !errors.Is(err, platform.ErrUnsupportedPlatform) {
		t.Fatalf("cwd prober err = %v, want ErrUnsupportedPlatform", err)
	}
	if _, err := NewCommProber().Comm(1); !errors.Is(err, platform.ErrUnsupportedPlatform) {
		t.Fatalf("comm prober err = %v, want ErrUnsupportedPlatform", err)
	}
}
