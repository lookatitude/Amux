package notify

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/amux-run/amux/internal/platform"
	"github.com/amux-run/amux/internal/store"
)

// identityRedact is the permissive test redactor: payloads pass unchanged.
func identityRedact(_ string, p []byte) ([]byte, error) { return p, nil }

// fakeNotifier records deliveries and can fail on demand. onNotify (if set)
// runs inside Notify so tests can observe the store state at delivery time.
type fakeNotifier struct {
	mu       sync.Mutex
	calls    []platform.Notification
	err      error
	onNotify func()
}

func (f *fakeNotifier) Notify(n platform.Notification) error {
	f.mu.Lock()
	f.calls = append(f.calls, n)
	f.mu.Unlock()
	if f.onNotify != nil {
		f.onNotify()
	}
	return f.err
}

func (f *fakeNotifier) callCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.calls)
}

func newTestService(t *testing.T, n platform.Notifier, clock platform.Clock, redact Redact) (*Service, *store.Store) {
	t.Helper()
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	svc, err := NewService(st, n, clock, redact, logger)
	if err != nil {
		t.Fatalf("new service: %v", err)
	}
	return svc, st
}

func TestPublishPersistsBeforeDelivery(t *testing.T) {
	clock := platform.NewFakeClock(1_000)
	fn := &fakeNotifier{}
	var svc *Service
	var rowsAtDelivery int
	fn.onNotify = func() {
		rows, err := svc.List("s1", false, 0)
		if err != nil {
			t.Errorf("list at delivery time: %v", err)
		}
		rowsAtDelivery = len(rows)
	}
	svc, _ = newTestService(t, fn, clock, identityRedact)

	row, err := svc.Publish(context.Background(), Input{
		Session: "s1", Workspace: "w1", Pane: "p1",
		Kind: KindAttention, Title: "needs you", Body: "approve the tool",
	})
	if err != nil {
		t.Fatalf("publish: %v", err)
	}
	if fn.callCount() != 1 {
		t.Fatalf("notifier calls = %d, want 1", fn.callCount())
	}
	if rowsAtDelivery != 1 {
		t.Fatalf("rows visible at delivery time = %d, want 1 (persist must precede delivery)", rowsAtDelivery)
	}
	if row.ID == "" || row.CreatedMS != 1_000 || row.ReadAtMS != 0 {
		t.Fatalf("unexpected row: %+v", row)
	}
	fn.mu.Lock()
	got := fn.calls[0]
	fn.mu.Unlock()
	if got.Title != "needs you" || got.Body != "approve the tool" || got.Urgency != platform.NotifyUrgencyCritical {
		t.Fatalf("delivered payload = %+v", got)
	}
}

func TestPublishDeliveryFailureLeavesRowIntact(t *testing.T) {
	clock := platform.NewFakeClock(2_000)
	fn := &fakeNotifier{err: errors.New("dbus is down")}
	svc, _ := newTestService(t, fn, clock, identityRedact)

	row, err := svc.Publish(context.Background(), Input{
		Session: "s1", Kind: KindError, Title: "boom", Body: "pane exited 1",
	})
	if err != nil {
		t.Fatalf("publish must not fail on advisory delivery error: %v", err)
	}
	if svc.DeliveryFailures() != 1 {
		t.Fatalf("delivery failures = %d, want 1", svc.DeliveryFailures())
	}
	rows, err := svc.List("s1", false, 0)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("rows = %d, want 1", len(rows))
	}
	if rows[0] != row {
		t.Fatalf("stored row altered by failed delivery:\n stored=%+v\nreturned=%+v", rows[0], row)
	}
	if rows[0].ReadAtMS != 0 || rows[0].Title != "boom" || rows[0].Body != "pane exited 1" {
		t.Fatalf("row content mutated: %+v", rows[0])
	}
}

func TestPublishRedactionFailsClosed(t *testing.T) {
	clock := platform.NewFakeClock(0)
	fn := &fakeNotifier{}
	failing := func(rc string, p []byte) ([]byte, error) {
		return nil, errors.New("policy engine unavailable")
	}
	svc, _ := newTestService(t, fn, clock, failing)

	_, err := svc.Publish(context.Background(), Input{
		Session: "s1", Kind: KindAttention, Title: "secret-ish", Body: "b",
	})
	if err == nil {
		t.Fatal("publish must fail closed on redaction error (RED-8)")
	}
	if n, _ := svc.CountUnread("s1"); n != 0 {
		t.Fatalf("rows persisted despite redaction failure: %d", n)
	}
	if fn.callCount() != 0 {
		t.Fatalf("delivery attempted despite redaction failure: %d calls", fn.callCount())
	}
}

func TestPublishRedactsBeforePersistenceAndDelivery(t *testing.T) {
	clock := platform.NewFakeClock(0)
	fn := &fakeNotifier{}
	var contexts []string
	redact := func(rc string, p []byte) ([]byte, error) {
		contexts = append(contexts, rc)
		return bytes.ReplaceAll(p, []byte("RAW"), []byte("[REDACTED]")), nil
	}
	svc, _ := newTestService(t, fn, clock, redact)

	row, err := svc.Publish(context.Background(), Input{
		Session: "s1", Kind: KindLifecycle, Title: "title RAW", Body: "body RAW",
	})
	if err != nil {
		t.Fatalf("publish: %v", err)
	}
	for _, rc := range contexts {
		if rc != RedactionContext {
			t.Fatalf("redaction context = %q, want %q", rc, RedactionContext)
		}
	}
	if strings.Contains(row.Title, "RAW") || strings.Contains(row.Body, "RAW") {
		t.Fatalf("stored row carries unredacted payload: %+v", row)
	}
	fn.mu.Lock()
	got := fn.calls[0]
	fn.mu.Unlock()
	if strings.Contains(got.Title, "RAW") || strings.Contains(got.Body, "RAW") {
		t.Fatalf("delivered payload unredacted: %+v", got)
	}
	if got.Title != "title [REDACTED]" || got.Body != "body [REDACTED]" {
		t.Fatalf("delivered payload = %+v", got)
	}
}

func TestPublishInfoKindSkipsDesktopDelivery(t *testing.T) {
	clock := platform.NewFakeClock(0)
	fn := &fakeNotifier{}
	svc, _ := newTestService(t, fn, clock, identityRedact)
	if _, err := svc.Publish(context.Background(), Input{Session: "s1", Kind: KindInfo, Title: "fyi"}); err != nil {
		t.Fatalf("publish: %v", err)
	}
	if fn.callCount() != 0 {
		t.Fatalf("info kind must not fire desktop delivery; got %d calls", fn.callCount())
	}
	if n, _ := svc.CountUnread("s1"); n != 1 {
		t.Fatalf("info row not stored: unread=%d", n)
	}
}

func TestRoute(t *testing.T) {
	tests := []struct {
		name string
		in   Input
		want Decision
	}{
		{"attention", Input{Kind: KindAttention}, Decision{platform.NotifyUrgencyCritical, true}},
		{"error", Input{Kind: KindError}, Decision{platform.NotifyUrgencyCritical, true}},
		{"lifecycle", Input{Kind: KindLifecycle}, Decision{platform.NotifyUrgencyNormal, true}},
		{"info", Input{Kind: KindInfo}, Decision{platform.NotifyUrgencyLow, false}},
		{"unknown keeps caller urgency, no desktop",
			Input{Kind: "custom", Urgency: platform.NotifyUrgencyNormal},
			Decision{platform.NotifyUrgencyNormal, false}},
		{"empty kind", Input{}, Decision{platform.NotifyUrgencyLow, false}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := Route(tc.in); got != tc.want {
				t.Fatalf("Route(%+v) = %+v, want %+v", tc.in, got, tc.want)
			}
		})
	}
}

func TestReadStateFlows(t *testing.T) {
	clock := platform.NewFakeClock(1_000)
	svc, _ := newTestService(t, NopNotifier{}, clock, identityRedact)
	ctx := context.Background()

	publish := func(title string) store.NotificationRow {
		t.Helper()
		row, err := svc.Publish(ctx, Input{Session: "s1", Kind: KindInfo, Title: title})
		if err != nil {
			t.Fatalf("publish %q: %v", title, err)
		}
		clock.Advance(10 * time.Millisecond) // distinct created_ms → deterministic order
		return row
	}
	first := publish("first")
	publish("second")
	third := publish("third")

	if n, _ := svc.CountUnread("s1"); n != 3 {
		t.Fatalf("unread = %d, want 3", n)
	}
	latest, err := svc.LatestUnread("s1")
	if err != nil {
		t.Fatalf("latest unread: %v", err)
	}
	if latest.ID != third.ID {
		t.Fatalf("latest unread = %q, want newest %q", latest.Title, third.Title)
	}

	if err := svc.MarkRead(third.ID); err != nil {
		t.Fatalf("mark read: %v", err)
	}
	if err := svc.MarkRead(third.ID); err != nil {
		t.Fatalf("mark read must be idempotent: %v", err)
	}
	if err := svc.MarkRead("no-such-id"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("mark read unknown id: err = %v, want ErrNotFound", err)
	}

	unread, err := svc.List("s1", true, 0)
	if err != nil {
		t.Fatalf("list unread: %v", err)
	}
	if len(unread) != 2 {
		t.Fatalf("unread list = %d rows, want 2", len(unread))
	}
	if latest, _ = svc.LatestUnread("s1"); latest.Title != "second" {
		t.Fatalf("latest unread after mark = %q, want %q", latest.Title, "second")
	}

	if limited, _ := svc.List("s1", false, 1); len(limited) != 1 || limited[0].Title != "third" {
		t.Fatalf("limited list = %+v, want single newest row", limited)
	}

	marked, err := svc.MarkAllRead("s1")
	if err != nil {
		t.Fatalf("mark all read: %v", err)
	}
	if marked != 2 {
		t.Fatalf("mark all read = %d, want 2", marked)
	}
	if n, _ := svc.CountUnread("s1"); n != 0 {
		t.Fatalf("unread after mark-all = %d, want 0", n)
	}
	if _, err := svc.LatestUnread("s1"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("latest unread after mark-all: err = %v, want ErrNotFound", err)
	}
	_ = first
}

func TestDesktopNotifierFailsClosedOffLinux(t *testing.T) {
	if runtime.GOOS == "linux" {
		t.Skip("linux has a real notify-send adapter; fail-closed applies off Linux only")
	}
	err := NewDesktopNotifier().Notify(platform.Notification{Title: "t"})
	if !errors.Is(err, platform.ErrUnsupportedPlatform) {
		t.Fatalf("off-linux Notify err = %v, want ErrUnsupportedPlatform", err)
	}
}

func TestNewServiceValidatesDependencies(t *testing.T) {
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer st.Close()
	clock := platform.NewFakeClock(0)

	if _, err := NewService(nil, NopNotifier{}, clock, identityRedact, nil); err == nil {
		t.Fatal("nil store accepted")
	}
	if _, err := NewService(st, nil, clock, identityRedact, nil); err == nil {
		t.Fatal("nil notifier accepted")
	}
	if _, err := NewService(st, NopNotifier{}, nil, identityRedact, nil); err == nil {
		t.Fatal("nil clock accepted")
	}
	if _, err := NewService(st, NopNotifier{}, clock, nil, nil); err == nil {
		t.Fatal("nil redactor accepted (RED-8 requires the seam)")
	}
}
