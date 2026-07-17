//go:build darwin || linux

package pty

import (
	"bytes"
	"errors"
	"io"
	"os"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/amux-run/amux/internal/platform"
)

// watchdog is the generous per-wait deadline every blocking assertion uses so
// a regression hangs a test for seconds, never forever.
const watchdog = 15 * time.Second

// pidAlive is the kill(pid, 0) liveness probe used by the orphan checks.
func pidAlive(pid int) bool { return syscall.Kill(pid, 0) == nil }

// testSpec returns a minimal valid PTYSpec running script under /bin/sh with
// an explicit two-entry environment (PATH so the script can find coreutils).
func testSpec(t *testing.T, script string) platform.PTYSpec {
	t.Helper()
	return platform.PTYSpec{
		Argv: []string{"/bin/sh", "-c", script},
		Dir:  t.TempDir(),
		Env:  []string{"PATH=/usr/bin:/bin"},
		Size: platform.PTYSize{Rows: 24, Cols: 80},
	}
}

// drain reads the master until EOF/EIO in the background and returns the
// collected output once the pump finishes (or fails the test on timeout).
func drain(t *testing.T, h platform.PTYHandle) func() string {
	t.Helper()
	var buf bytes.Buffer
	done := make(chan struct{})
	go func() {
		defer close(done)
		_, _ = io.Copy(&buf, h)
	}()
	return func() string {
		select {
		case <-done:
			return buf.String()
		case <-time.After(watchdog):
			t.Fatal("timed out draining PTY output")
			return ""
		}
	}
}

func TestStartValidationFailsClosed(t *testing.T) {
	p := New()
	dir := t.TempDir()
	valid := testSpec(t, "true")

	cases := []struct {
		name string
		mut  func(*platform.PTYSpec)
	}{
		{"empty argv", func(s *platform.PTYSpec) { s.Argv = nil }},
		{"absolute argv0 missing", func(s *platform.PTYSpec) { s.Argv = []string{dir + "/no-such-binary"} }},
		{"relative argv0 unresolvable", func(s *platform.PTYSpec) { s.Argv = []string{"amux-no-such-command-b5"} }},
		{"empty dir", func(s *platform.PTYSpec) { s.Dir = "" }},
		{"missing dir", func(s *platform.PTYSpec) { s.Dir = dir + "/nope" }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			spec := valid
			spec.Argv = append([]string{}, valid.Argv...)
			tc.mut(&spec)
			if _, err := p.Start(spec); err == nil {
				t.Fatal("Start succeeded; want fail-closed validation error")
			}
		})
	}

	t.Run("dir is a file", func(t *testing.T) {
		file := dir + "/plain-file"
		if err := os.WriteFile(file, []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
		spec := valid
		spec.Dir = file
		if _, err := p.Start(spec); err == nil {
			t.Fatal("Start succeeded with a non-directory Dir; want error")
		}
	})
}

func TestHandleOutputWaitAndCloseIdempotent(t *testing.T) {
	h, err := New().Start(testSpec(t, "printf hello"))
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	out := drain(t, h)

	exit, err := h.Wait()
	if err != nil {
		t.Fatalf("Wait: %v", err)
	}
	if exit.Code != 0 || exit.Signal != "" {
		t.Fatalf("exit = %+v; want Code 0, no signal", exit)
	}
	// Wait is reap-exactly-once: a second call returns the cached result.
	again, err := h.Wait()
	if err != nil || again != exit {
		t.Fatalf("second Wait = %+v, %v; want cached %+v, nil", again, err, exit)
	}
	if h.MasterFD() == 0 {
		t.Fatal("MasterFD returned 0")
	}
	if got := out(); !strings.Contains(got, "hello") {
		t.Fatalf("output %q missing %q", got, "hello")
	}
	if err := h.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if err := h.Close(); err != nil {
		t.Fatalf("second Close: %v; want idempotent nil", err)
	}
}

func TestHandleSignalDeathClassification(t *testing.T) {
	h, err := New().Start(testSpec(t, "sleep 30"))
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer func() { _ = h.Close() }()
	out := drain(t, h)

	if err := h.Signal(syscall.SIGKILL); err != nil {
		t.Fatalf("Signal: %v", err)
	}
	exit, err := h.Wait()
	if err != nil {
		t.Fatalf("Wait: %v", err)
	}
	if exit.Signal != "SIGKILL" {
		t.Fatalf("exit = %+v; want Signal %q", exit, "SIGKILL")
	}
	_ = out()
}

func TestHandleExitCodeClassification(t *testing.T) {
	h, err := New().Start(testSpec(t, "exit 7"))
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer func() { _ = h.Close() }()
	out := drain(t, h)

	exit, err := h.Wait()
	if err != nil {
		t.Fatalf("Wait: %v", err)
	}
	if exit.Code != 7 || exit.Signal != "" {
		t.Fatalf("exit = %+v; want Code 7, no signal", exit)
	}
	_ = out()
}

func TestHandleRejectsNonSyscallSignal(t *testing.T) {
	h, err := New().Start(testSpec(t, "sleep 30"))
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer func() {
		_ = h.Signal(syscall.SIGKILL)
		_, _ = h.Wait()
		_ = h.Close()
	}()
	if err := h.Signal(fakeSignal{}); err == nil {
		t.Fatal("Signal accepted a non-syscall signal; want error")
	}
}

type fakeSignal struct{}

func (fakeSignal) String() string { return "fake" }
func (fakeSignal) Signal()        {}

// TestUnsupportedSignalNameFallback pins the numeric fallback for signals
// outside the mapped POSIX set.
func TestSignalNameMapping(t *testing.T) {
	if got := signalName(syscall.SIGKILL); got != "SIGKILL" {
		t.Fatalf("signalName(SIGKILL) = %q", got)
	}
	if got := signalName(syscall.SIGTERM); got != "SIGTERM" {
		t.Fatalf("signalName(SIGTERM) = %q", got)
	}
	if got := signalName(syscall.Signal(63)); got != "SIG63" {
		t.Fatalf("signalName(63) = %q; want numeric fallback", got)
	}
}

// TestConflictAndNotFoundErrorTypes pins the typed-error contract.
func TestTypedErrorStrings(t *testing.T) {
	var conflict error = &ConflictError{ID: "a"}
	var notFound error = &NotFoundError{ID: "b"}
	var ce *ConflictError
	var ne *NotFoundError
	if !errors.As(conflict, &ce) || ce.ID != "a" {
		t.Fatalf("ConflictError does not round-trip errors.As: %v", conflict)
	}
	if !errors.As(notFound, &ne) || ne.ID != "b" {
		t.Fatalf("NotFoundError does not round-trip errors.As: %v", notFound)
	}
}
