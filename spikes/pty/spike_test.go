//go:build darwin || linux

package ptyspike

import (
	"bytes"
	"io"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/creack/pty"
)

// TestPTYRoundTrip proves creack/pty spawns a child on a real PTY and its output
// is readable from the master side — the minimal capability the frozen PTY
// interface (ADR-0006) wraps. Runs on the Unix author host; the Linux CI lane
// re-runs it natively.
func TestPTYRoundTrip(t *testing.T) {
	cmd := exec.Command("/bin/sh", "-c", "printf amux-pty-ok")
	ptmx, err := pty.Start(cmd)
	if err != nil {
		t.Fatalf("pty.Start: %v", err)
	}
	defer func() { _ = ptmx.Close() }()

	// Read until the child exits (EOF on the master) or a deadline.
	done := make(chan struct{})
	var buf bytes.Buffer
	go func() {
		_, _ = io.Copy(&buf, ptmx)
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("timed out reading PTY output")
	}
	_ = cmd.Wait()

	if !strings.Contains(buf.String(), "amux-pty-ok") {
		t.Fatalf("PTY output missing expected marker; got %q", buf.String())
	}
}

// TestPTYResize proves the resize primitive the supervisor needs (SIGWINCH path)
// is callable through the same library.
func TestPTYResize(t *testing.T) {
	cmd := exec.Command("/bin/sh", "-c", "sleep 0.1")
	ptmx, err := pty.Start(cmd)
	if err != nil {
		t.Fatalf("pty.Start: %v", err)
	}
	defer func() { _ = ptmx.Close() }()
	if err := pty.Setsize(ptmx, &pty.Winsize{Rows: 40, Cols: 120}); err != nil {
		t.Fatalf("pty.Setsize: %v", err)
	}
	_ = cmd.Wait()
}
