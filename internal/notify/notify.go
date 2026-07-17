// Package notify is the daemon's notification service (ADR-0001 package map:
// "notification store and delivery adapters"; PRD F8). It realizes the ADR-0005
// authority rule for notifications: the live SQLite store (internal/store) is
// the SOLE authority for notification state. Publish persists the in-app row
// FIRST and only then attempts best-effort desktop delivery through the
// injected platform.Notifier seam (ADR-0006 §Notifier) — a delivery failure is
// logged and counted but NEVER creates, removes, or marks the stored row.
//
// Redaction posture (RED-8: no unredacted egress): Title and Body pass through
// the injected Redact seam with context "notification" BEFORE persistence and
// delivery. A redaction error fails Publish closed — nothing is stored and
// nothing is delivered.
//
// Delivery adapters live behind platform.Notifier and are replaceable without
// touching this service (ADR-0006): Linux ships a notify-send adapter, other
// platforms fail closed with platform.ErrUnsupportedPlatform, and NopNotifier
// serves headless daemons.
package notify

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync/atomic"

	"github.com/google/uuid"

	"github.com/amux-run/amux/internal/platform"
	"github.com/amux-run/amux/internal/store"
)

// RedactionContext is the redaction-policy context name for notification
// egress (mirrors RED-1 fixture naming).
const RedactionContext = "notification"

// Redact is the injected redaction seam. It receives the policy context name
// ("notification" here) and the payload, returning the redacted payload. An
// error means the payload must not egress — the caller fails closed.
type Redact func(redactionContext string, payload []byte) ([]byte, error)

// Notification kinds understood by the routing table. Unknown kinds are
// accepted (the store is schemaless about kind) but never trigger desktop
// delivery.
const (
	KindAttention = "attention" // agent needs the user (critical, desktop)
	KindError     = "error"     // failure the user must see (critical, desktop)
	KindLifecycle = "lifecycle" // pane/session started or exited (normal, desktop)
	KindInfo      = "info"      // informational, in-app only (low, no desktop)
)

// Input is one semantic notification to publish. Kind drives routing (see
// Route); Urgency is consulted only for kinds outside the routing table.
type Input struct {
	Session   string
	Workspace string
	Pane      string
	Kind      string
	Title     string
	Body      string
	Urgency   platform.NotifyUrgency
}

// Decision is the routing outcome for one Input: the desktop urgency hint and
// whether best-effort desktop delivery fires at all. The in-app row is stored
// unconditionally — Desktop=false only suppresses the advisory delivery.
type Decision struct {
	Urgency platform.NotifyUrgency
	Desktop bool
}

// Route maps a notification kind to its delivery decision:
//
//	attention, error → Critical, desktop delivery
//	lifecycle        → Normal, desktop delivery
//	info             → Low, in-app only
//
// An unknown kind keeps the caller-supplied Urgency and stays in-app only
// (fail closed on delivery, never on storage).
func Route(in Input) Decision {
	switch in.Kind {
	case KindAttention, KindError:
		return Decision{Urgency: platform.NotifyUrgencyCritical, Desktop: true}
	case KindLifecycle:
		return Decision{Urgency: platform.NotifyUrgencyNormal, Desktop: true}
	case KindInfo:
		return Decision{Urgency: platform.NotifyUrgencyLow, Desktop: false}
	default:
		return Decision{Urgency: in.Urgency, Desktop: false}
	}
}

// Service publishes notifications (store-first, delivery-advisory) and fronts
// the read-state API of the authoritative store.
type Service struct {
	store    *store.Store
	notifier platform.Notifier
	clock    platform.Clock
	redact   Redact
	log      *slog.Logger

	deliveryFailures atomic.Uint64
}

// NewService wires the notification service. Every dependency is mandatory
// except logger (nil falls back to slog.Default()): the store is the
// authority, the notifier is the advisory delivery seam (use NopNotifier for
// headless), the clock stamps rows, and redact guards egress — a nil redactor
// would silently disable RED-8, so it is refused.
func NewService(st *store.Store, notifier platform.Notifier, clock platform.Clock, redact Redact, logger *slog.Logger) (*Service, error) {
	switch {
	case st == nil:
		return nil, errors.New("notify: store is required")
	case notifier == nil:
		return nil, errors.New("notify: notifier is required (use NopNotifier for headless)")
	case clock == nil:
		return nil, errors.New("notify: clock is required")
	case redact == nil:
		return nil, errors.New("notify: redactor is required (RED-8: no unredacted egress)")
	}
	if logger == nil {
		logger = slog.Default()
	}
	return &Service{store: st, notifier: notifier, clock: clock, redact: redact, log: logger}, nil
}

// Publish records one notification and returns the stored row. Order is
// load-bearing (ADR-0005):
//
//  1. Redact Title and Body (context "notification"); a redaction error fails
//     closed — nothing is stored, nothing is delivered.
//  2. Persist the row (UUIDv7 id, clock timestamp). The store is authoritative;
//     a store error fails Publish.
//  3. If Route says so, attempt ONE best-effort desktop delivery. A delivery
//     error is logged and counted but never alters the stored row and never
//     fails Publish.
func (s *Service) Publish(ctx context.Context, in Input) (store.NotificationRow, error) {
	if err := ctx.Err(); err != nil {
		return store.NotificationRow{}, fmt.Errorf("notify: publish: %w", err)
	}
	title, err := s.redact(RedactionContext, []byte(in.Title))
	if err != nil {
		return store.NotificationRow{}, fmt.Errorf("notify: title redaction failed, refusing to publish: %w", err)
	}
	body, err := s.redact(RedactionContext, []byte(in.Body))
	if err != nil {
		return store.NotificationRow{}, fmt.Errorf("notify: body redaction failed, refusing to publish: %w", err)
	}
	id, err := uuid.NewV7()
	if err != nil {
		return store.NotificationRow{}, fmt.Errorf("notify: new id: %w", err)
	}
	row := store.NotificationRow{
		ID:        id.String(),
		Session:   in.Session,
		Workspace: in.Workspace,
		Pane:      in.Pane,
		Kind:      in.Kind,
		Title:     string(title),
		Body:      string(body),
		CreatedMS: s.clock.NowUnixMilli(),
	}
	if err := s.store.InsertNotification(row); err != nil {
		return store.NotificationRow{}, err
	}

	if d := Route(in); d.Desktop {
		n := platform.Notification{Title: row.Title, Body: row.Body, Urgency: d.Urgency}
		if err := s.notifier.Notify(n); err != nil {
			// Advisory only: the authoritative row above stays exactly as
			// committed (ADR-0006 §Notifier).
			s.deliveryFailures.Add(1)
			s.log.Warn("notify: desktop delivery failed (advisory, row retained)",
				"id", row.ID, "kind", row.Kind, "error", err)
		}
	}
	return row, nil
}

// DeliveryFailures reports how many advisory desktop deliveries have failed
// since the service started (observability only; no row state).
func (s *Service) DeliveryFailures() uint64 { return s.deliveryFailures.Load() }

// MarkRead marks one notification read at the current clock time. Idempotent;
// store.ErrNotFound for an unknown id.
func (s *Service) MarkRead(id string) error {
	return s.store.MarkRead(id, s.clock.NowUnixMilli())
}

// MarkAllRead marks every unread notification of a session read now and
// returns how many it marked.
func (s *Service) MarkAllRead(session string) (int64, error) {
	return s.store.MarkAllRead(session, s.clock.NowUnixMilli())
}

// List returns a session's notifications newest-first. With unreadOnly true
// only unread rows are returned; limit <= 0 means no limit.
func (s *Service) List(session string, unreadOnly bool, limit int) ([]store.NotificationRow, error) {
	return s.store.ListNotifications(session, unreadOnly, limit)
}

// LatestUnread returns the newest unread notification of a session, or
// store.ErrNotFound when everything is read.
func (s *Service) LatestUnread(session string) (store.NotificationRow, error) {
	return s.store.LatestUnread(session)
}

// CountUnread returns the number of unread notifications for a session.
func (s *Service) CountUnread(session string) (int64, error) {
	return s.store.CountUnread(session)
}
