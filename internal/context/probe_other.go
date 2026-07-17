//go:build !linux

package context

import (
	"fmt"

	"github.com/amux-run/amux/internal/platform"
)

// NewCwdProber fails closed off Linux (ADR-0006): there is no /proc cwd
// mechanism here and Amux never claims a capability the platform doesn't
// implement. Tests inject fakes.
func NewCwdProber() CwdProber { return unsupportedProber{} }

// NewCommProber fails closed off Linux.
func NewCommProber() CommProber { return unsupportedProber{} }

type unsupportedProber struct{}

func (unsupportedProber) Cwd(pid int) (string, error) {
	return "", fmt.Errorf("context: cwd probe: %w", platform.ErrUnsupportedPlatform)
}

func (unsupportedProber) Comm(pid int) (string, error) {
	return "", fmt.Errorf("context: comm probe: %w", platform.ErrUnsupportedPlatform)
}
