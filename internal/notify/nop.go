package notify

import "github.com/amux-run/amux/internal/platform"

// NopNotifier is the headless delivery adapter: it accepts every notification
// and delivers nowhere. Wire it when the daemon has no desktop session — the
// authoritative in-app store still records everything (ADR-0005), only the
// advisory desktop hop disappears.
type NopNotifier struct{}

// Notify discards the notification and reports success.
func (NopNotifier) Notify(platform.Notification) error { return nil }

// Compile-time seam check.
var _ platform.Notifier = NopNotifier{}
