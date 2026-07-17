//go:build linux

package context

import (
	"fmt"
	"os"
	"strings"
)

// NewCwdProber returns the Linux /proc-backed cwd prober. Runtime evidence is
// Linux-only (deferred to a Linux host); the author host covers callers with
// fakes.
func NewCwdProber() CwdProber { return procCwdProber{} }

type procCwdProber struct{}

func (procCwdProber) Cwd(pid int) (string, error) {
	cwd, err := os.Readlink(fmt.Sprintf("/proc/%d/cwd", pid))
	if err != nil {
		return "", fmt.Errorf("context: read /proc/%d/cwd: %w", pid, err)
	}
	return cwd, nil
}

// NewCommProber returns the Linux /proc-backed command-name prober.
func NewCommProber() CommProber { return procCommProber{} }

type procCommProber struct{}

func (procCommProber) Comm(pid int) (string, error) {
	b, err := os.ReadFile(fmt.Sprintf("/proc/%d/comm", pid))
	if err != nil {
		return "", fmt.Errorf("context: read /proc/%d/comm: %w", pid, err)
	}
	return strings.TrimSuffix(string(b), "\n"), nil
}
