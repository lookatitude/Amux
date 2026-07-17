package notify

import (
	"strings"
	"testing"

	"github.com/amux-run/amux/internal/tui/model"
)

func sample() []model.Notification {
	return []model.Notification{
		{ID: "n1", Kind: model.NotifyInfo, Title: "old", CreatedMS: 100, Read: true, Pane: "p1"},
		{ID: "n2", Kind: model.NotifyAttention, Title: "mid", CreatedMS: 200, Read: false, Pane: "p2"},
		{ID: "n3", Kind: model.NotifyExit, Title: "new", CreatedMS: 300, Read: false, Pane: "p2", Delivery: model.DeliveryFailed},
	}
}

func TestInboxOrderingAndUnread(t *testing.T) {
	b := NewInbox(sample())
	vis := b.Visible()
	if vis[0].ID != "n3" || vis[2].ID != "n1" {
		t.Fatalf("not newest-first: %v", []string{vis[0].ID, vis[1].ID, vis[2].ID})
	}
	if b.Unread() != 2 {
		t.Fatalf("unread = %d, want 2", b.Unread())
	}
	if b.UnreadForPane("p2") != 2 || b.UnreadForPane("p1") != 0 {
		t.Fatalf("per-pane unread wrong")
	}
}

func TestLatestUnreadRouting(t *testing.T) {
	b := NewInbox(sample())
	n, ok := b.LatestUnread()
	if !ok || n.ID != "n3" {
		t.Fatalf("latest unread = %+v ok=%v, want n3", n, ok)
	}
	pane, ok := b.RouteTarget()
	if !ok || pane != "p2" {
		t.Fatalf("route target = %q ok=%v, want p2", pane, ok)
	}
}

func TestMarkReadIntent(t *testing.T) {
	b := NewInbox(sample())
	intent, ok := b.MarkRead("n2")
	if !ok || intent.ID != "n2" {
		t.Fatalf("mark read intent = %+v ok=%v", intent, ok)
	}
	// Already-read returns no intent.
	if _, ok := b.MarkRead("n1"); ok {
		t.Fatal("already-read should yield no intent")
	}
	// The inbox does NOT flip read locally (daemon authority).
	if b.Unread() != 2 {
		t.Fatal("inbox must not mutate read state locally")
	}
}

func TestDismissIsLocalHide(t *testing.T) {
	b := NewInbox(sample())
	b.Dismiss("n3")
	for _, n := range b.Visible() {
		if n.ID == "n3" {
			t.Fatal("dismissed notification still visible")
		}
	}
	if b.Unread() != 1 {
		t.Fatalf("after dismiss unread = %d, want 1", b.Unread())
	}
}

func TestDeliveryFailures(t *testing.T) {
	b := NewInbox(sample())
	f := b.DeliveryFailures()
	if len(f) != 1 || f[0].ID != "n3" {
		t.Fatalf("delivery failures = %v", f)
	}
}

func TestFocusLatestUnread(t *testing.T) {
	b := NewInbox(sample())
	if !b.FocusLatestUnread() {
		t.Fatal("should focus latest unread")
	}
	cur, ok := b.Cursor()
	if !ok || cur.ID != "n3" {
		t.Fatalf("cursor = %+v, want n3", cur)
	}
}

func TestTrustCardShowsAllFrozenFields(t *testing.T) {
	g := model.HookGrant{
		ID: "g1", HookID: "hook-1", Project: "proj-x",
		Events: []string{"PreToolUse", "PostToolUse"}, Scope: "pane",
		Active: true, BoundEpoch: 3,
		Executable: "/usr/bin/tool", Digest: "sha256:abc", CwdScope: "/home/w",
		EnvKeys: []string{"PATH", "HOME"}, TimeoutMS: 5000, OutputCapB: 65536,
	}
	lines := TrustCard(g, TrustApprove)
	joined := strings.Join(lines, "\n")
	for _, want := range []string{"proj-x", "/usr/bin/tool", "sha256:abc", "PreToolUse", "/home/w", "PATH", "5000ms", "65536 bytes", "bound_epoch=3"} {
		if !strings.Contains(joined, want) {
			t.Errorf("trust card missing %q:\n%s", want, joined)
		}
	}
	// Approve requires confirmation and fails closed.
	if !strings.Contains(joined, "fail-closed") {
		t.Errorf("approve card must state fail-closed confirmation:\n%s", joined)
	}
}

func TestTrustCardMarksUnavailableFields(t *testing.T) {
	// Only wire-delivered fields present; extended trust fields missing.
	g := model.HookGrant{ID: "g1", HookID: "hook-1", Project: "proj-y", Events: []string{"PreToolUse"}, Scope: "pane", Active: true}
	lines := TrustCard(g, TrustRevoke)
	joined := strings.Join(lines, "\n")
	if !strings.Contains(joined, "UNAVAILABLE") {
		t.Fatalf("missing trust fields must be marked UNAVAILABLE:\n%s", joined)
	}
	if !strings.Contains(joined, "destructive") {
		t.Fatalf("revoke must be marked destructive:\n%s", joined)
	}
}

func TestTrustActionConfirmMatrix(t *testing.T) {
	if !TrustApprove.NeedsConfirm() || !TrustRevoke.NeedsConfirm() {
		t.Fatal("approve/revoke must need confirmation")
	}
	if TrustDeny.NeedsConfirm() {
		t.Fatal("deny must not need confirmation")
	}
}
