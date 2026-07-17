//go:build linux

package notify

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"time"

	"github.com/amux-run/amux/internal/platform"
)

// notifySendTimeout bounds one delivery attempt. A hung notification daemon
// must never stall the publisher — delivery is advisory (ADR-0006 §Notifier).
const notifySendTimeout = 2 * time.Second

// notifySendNotifier delivers via the freedesktop `notify-send` CLI. It is one
// replaceable adapter behind platform.Notifier (ADR-0006): swapping it for a
// direct D-Bus implementation (or anything else) touches only this file — the
// service and the authoritative store never see the mechanism. Every error it
// returns is advisory; the caller logs and counts but never mutates the
// stored notification.
type notifySendNotifier struct {
	timeout time.Duration
}

// NewDesktopNotifier returns the Linux desktop delivery adapter (notify-send,
// 2 s timeout per attempt).
func NewDesktopNotifier() platform.Notifier {
	return &notifySendNotifier{timeout: notifySendTimeout}
}

// Notify runs one bounded notify-send invocation: no stdin, an explicit small
// environment (only the variables desktop delivery needs), positional args
// terminated with "--" so a title starting with "-" cannot become a flag.
func (n *notifySendNotifier) Notify(nn platform.Notification) error {
	ctx, cancel := context.WithTimeout(context.Background(), n.timeout)
	defer cancel()

	path, err := exec.LookPath("notify-send")
	if err != nil {
		return fmt.Errorf("notify: notify-send not found (advisory): %w", err)
	}
	cmd := exec.CommandContext(ctx, path,
		"--app-name=amux",
		"--urgency="+urgencyArg(nn.Urgency),
		"--", nn.Title, nn.Body)
	cmd.Stdin = nil // stdin-free: the adapter never feeds a child input
	cmd.Env = desktopEnv()
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("notify: notify-send delivery failed (advisory): %w", err)
	}
	return nil
}

// urgencyArg maps the seam's urgency hint to notify-send's vocabulary.
func urgencyArg(u platform.NotifyUrgency) string {
	switch u {
	case platform.NotifyUrgencyCritical:
		return "critical"
	case platform.NotifyUrgencyNormal:
		return "normal"
	default:
		return "low"
	}
}

// desktopEnv builds the explicit minimal environment for notify-send: only
// what locating the binary and reaching the session bus requires. The
// daemon's full environment (which may reference secrets) never leaks into
// the child.
func desktopEnv() []string {
	keep := []string{
		"PATH", "HOME",
		"DBUS_SESSION_BUS_ADDRESS", "XDG_RUNTIME_DIR",
		"DISPLAY", "WAYLAND_DISPLAY",
	}
	env := make([]string, 0, len(keep))
	for _, k := range keep {
		if v, ok := os.LookupEnv(k); ok {
			env = append(env, k+"="+v)
		}
	}
	return env
}
