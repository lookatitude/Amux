package store

import (
	"errors"
	"testing"
)

func seedNotifications(t *testing.T, s *Store) {
	t.Helper()
	for _, n := range []NotificationRow{
		{ID: "n1", Session: "s1", Workspace: "w1", Pane: "p1", Kind: "bell", Title: "t1", Body: "b1", CreatedMS: 100},
		{ID: "n2", Session: "s1", Workspace: "w1", Pane: "p2", Kind: "exit", Title: "t2", Body: "b2", CreatedMS: 200},
		{ID: "n3", Session: "s1", Workspace: "w2", Pane: "p3", Kind: "bell", Title: "t3", Body: "b3", CreatedMS: 300},
		{ID: "n4", Session: "s2", Workspace: "w9", Pane: "p9", Kind: "bell", Title: "t4", Body: "b4", CreatedMS: 400},
	} {
		if err := s.InsertNotification(n); err != nil {
			t.Fatalf("InsertNotification(%s): %v", n.ID, err)
		}
	}
}

func TestNotificationsNewestFirstAndUnreadState(t *testing.T) {
	s := openStore(t, t.TempDir())
	seedNotifications(t, s)

	list, err := s.ListNotifications("s1", false, 0)
	if err != nil {
		t.Fatalf("ListNotifications: %v", err)
	}
	if len(list) != 3 || list[0].ID != "n3" || list[1].ID != "n2" || list[2].ID != "n1" {
		t.Fatalf("ListNotifications = %+v, want newest-first [n3 n2 n1]", list)
	}

	if n, err := s.CountUnread("s1"); err != nil || n != 3 {
		t.Fatalf("CountUnread = %d, %v; want 3", n, err)
	}
	latest, err := s.LatestUnread("s1")
	if err != nil || latest.ID != "n3" {
		t.Fatalf("LatestUnread = %+v, %v; want n3", latest, err)
	}

	if err := s.MarkRead("n3", 500); err != nil {
		t.Fatalf("MarkRead: %v", err)
	}
	// Idempotent: a second MarkRead keeps the original read time.
	if err := s.MarkRead("n3", 999); err != nil {
		t.Fatalf("second MarkRead: %v", err)
	}
	got, err := s.ListNotifications("s1", false, 1)
	if err != nil || len(got) != 1 || got[0].ReadAtMS != 500 {
		t.Fatalf("after double MarkRead: %+v, %v; want read_at 500", got, err)
	}

	latest, err = s.LatestUnread("s1")
	if err != nil || latest.ID != "n2" {
		t.Fatalf("LatestUnread after read = %+v, %v; want n2", latest, err)
	}

	unread, err := s.ListNotifications("s1", true, 0)
	if err != nil || len(unread) != 2 {
		t.Fatalf("unread list = %+v, %v; want 2 rows", unread, err)
	}

	marked, err := s.MarkAllRead("s1", 600)
	if err != nil || marked != 2 {
		t.Fatalf("MarkAllRead = %d, %v; want 2", marked, err)
	}
	if n, _ := s.CountUnread("s1"); n != 0 {
		t.Fatalf("CountUnread after MarkAllRead = %d, want 0", n)
	}
	if _, err := s.LatestUnread("s1"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("LatestUnread with none: err = %v, want ErrNotFound", err)
	}

	// Session isolation: s2 untouched by s1's MarkAllRead.
	if n, _ := s.CountUnread("s2"); n != 1 {
		t.Fatalf("CountUnread(s2) = %d, want 1", n)
	}
}

func TestMarkReadUnknownIDIsTyped(t *testing.T) {
	s := openStore(t, t.TempDir())
	if err := s.MarkRead("ghost", 1); !errors.Is(err, ErrNotFound) {
		t.Fatalf("MarkRead(ghost): err = %v, want ErrNotFound", err)
	}
}

func TestNotificationBodyStoredVerbatim(t *testing.T) {
	s := openStore(t, t.TempDir())
	// Redaction happens above the store: bytes are stored exactly as given.
	body := "token=SECRET\x1b[31m raw \n bytes"
	if err := s.InsertNotification(NotificationRow{ID: "n1", Session: "s1", Body: body, Title: body, CreatedMS: 1}); err != nil {
		t.Fatalf("InsertNotification: %v", err)
	}
	got, err := s.ListNotifications("s1", false, 1)
	if err != nil || len(got) != 1 || got[0].Body != body || got[0].Title != body {
		t.Fatalf("verbatim roundtrip failed: %+v, %v", got, err)
	}
}
