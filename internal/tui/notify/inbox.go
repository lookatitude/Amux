// Package notify is the terminal UI's notification inbox and hook-trust
// presentation (U6). It is a pure view over daemon-owned semantic state: the
// inbox never decides read/unread authority (that is the daemon's
// notification.read), and the trust card never decides authorization (that is
// the daemon's hook.approve/deny/revoke behind the frozen confirmation matrix).
// Read-state changes and trust decisions are surfaced as INTENTS the caller
// sends to the daemon; this package only projects, orders, and formats.
package notify

import (
	"sort"

	"github.com/amux-run/amux/internal/tui/model"
)

// Inbox is a presentation view over a snapshot of notifications. Dismissal is a
// LOCAL hide (the wire has no dismiss verb; hiding claims no durable authority);
// read-state changes go to the daemon via ReadIntent.
type Inbox struct {
	items  []model.Notification
	hidden map[string]bool
	cursor int
}

// NewInbox builds an inbox over items, ordered newest-first for display.
func NewInbox(items []model.Notification) *Inbox {
	cp := append([]model.Notification(nil), items...)
	sort.SliceStable(cp, func(i, j int) bool {
		if cp[i].CreatedMS != cp[j].CreatedMS {
			return cp[i].CreatedMS > cp[j].CreatedMS // newest first
		}
		return cp[i].ID < cp[j].ID
	})
	return &Inbox{items: cp, hidden: map[string]bool{}}
}

// Visible returns the non-hidden notifications in display order.
func (b *Inbox) Visible() []model.Notification {
	out := make([]model.Notification, 0, len(b.items))
	for _, n := range b.items {
		if !b.hidden[n.ID] {
			out = append(out, n)
		}
	}
	return out
}

// Unread counts visible unread notifications.
func (b *Inbox) Unread() int {
	n := 0
	for _, it := range b.Visible() {
		if !it.Read {
			n++
		}
	}
	return n
}

// UnreadForPane counts visible unread notifications routed to a pane.
func (b *Inbox) UnreadForPane(pane string) int {
	n := 0
	for _, it := range b.Visible() {
		if !it.Read && it.Pane == pane {
			n++
		}
	}
	return n
}

// LatestUnread returns the newest visible unread notification (the focus-
// routing target). ok is false when there is none.
func (b *Inbox) LatestUnread() (model.Notification, bool) {
	for _, n := range b.Visible() { // already newest-first
		if !n.Read {
			return n, true
		}
	}
	return model.Notification{}, false
}

// RouteTarget returns the pane the latest unread notification targets, for
// latest-unread focus routing. ok is false when there is no routed unread.
func (b *Inbox) RouteTarget() (string, bool) {
	if n, ok := b.LatestUnread(); ok && n.Pane != "" {
		return n.Pane, true
	}
	return "", false
}

// DeliveryFailures returns visible notifications whose OS-notifier delivery
// failed; the inbox remains the authoritative surface regardless (ADR-0005).
func (b *Inbox) DeliveryFailures() []model.Notification {
	var out []model.Notification
	for _, n := range b.Visible() {
		if n.Delivery == model.DeliveryFailed {
			out = append(out, n)
		}
	}
	return out
}

// ReadIntent is the daemon call the caller must issue to mark a notification
// read (rpcapi.NotificationReadParams{ID}). The inbox does not flip Read itself
// — it reflects the daemon's committed state on the next snapshot.
type ReadIntent struct {
	ID string
}

// MarkRead returns the ReadIntent for the notification id, or ok=false when it
// is absent or already read.
func (b *Inbox) MarkRead(id string) (ReadIntent, bool) {
	for _, n := range b.items {
		if n.ID == id {
			if n.Read {
				return ReadIntent{}, false
			}
			return ReadIntent{ID: id}, true
		}
	}
	return ReadIntent{}, false
}

// Dismiss hides a notification locally (presentation-only; no durable effect).
func (b *Inbox) Dismiss(id string) { b.hidden[id] = true }

// Cursor returns the currently selected visible notification.
func (b *Inbox) Cursor() (model.Notification, bool) {
	vis := b.Visible()
	if len(vis) == 0 {
		return model.Notification{}, false
	}
	if b.cursor < 0 {
		b.cursor = 0
	}
	if b.cursor >= len(vis) {
		b.cursor = len(vis) - 1
	}
	return vis[b.cursor], true
}

// FocusLatestUnread moves the cursor to the latest unread notification.
func (b *Inbox) FocusLatestUnread() bool {
	target, ok := b.LatestUnread()
	if !ok {
		return false
	}
	for i, n := range b.Visible() {
		if n.ID == target.ID {
			b.cursor = i
			return true
		}
	}
	return false
}

// Next/Prev move the cursor within the visible list.
func (b *Inbox) Next() { b.cursor++ }
func (b *Inbox) Prev() { b.cursor-- }
