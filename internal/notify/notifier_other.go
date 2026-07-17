//go:build !linux

package notify

import (
	"fmt"

	"github.com/amux-run/amux/internal/platform"
)

// NewDesktopNotifier fails closed off Linux (ADR-0006): there is no desktop
// delivery mechanism on this platform, and Amux never claims a capability the
// platform doesn't implement. Tests inject fakes; a real non-Linux adapter is
// a supported-platform change requiring spec confirmation.
func NewDesktopNotifier() platform.Notifier { return unsupportedNotifier{} }

type unsupportedNotifier struct{}

func (unsupportedNotifier) Notify(platform.Notification) error {
	return fmt.Errorf("notify: desktop delivery: %w", platform.ErrUnsupportedPlatform)
}
