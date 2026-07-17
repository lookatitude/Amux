package store

import (
	"database/sql"
	"errors"
	"fmt"
)

// NotificationRow is one in-app notification. Live SQLite is canonical for
// notifications during crash recovery (ADR-0005); desktop delivery is
// advisory and never mutates these rows. Title and Body are stored exactly as
// given — redaction happens above the store, at egress. ReadAtMS is 0 while
// unread.
type NotificationRow struct {
	ID        string
	Session   string
	Workspace string
	Pane      string
	Kind      string
	Title     string
	Body      string
	CreatedMS int64
	ReadAtMS  int64
}

const notificationColumns = `id, session, workspace, pane, kind, title, body,
	created_ms, read_at_ms`

// InsertNotification records one notification. ReadAtMS on the input is
// ignored; a new notification is always unread.
func (s *Store) InsertNotification(n NotificationRow) error {
	_, err := s.execWrite(`
		INSERT INTO notifications (id, session, workspace, pane, kind, title, body, created_ms)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		n.ID, n.Session, n.Workspace, n.Pane, n.Kind, n.Title, n.Body, n.CreatedMS)
	if err != nil {
		return fmt.Errorf("store: insert notification: %w", err)
	}
	return nil
}

// MarkRead marks one notification read at atMS. Idempotent: an already-read
// notification keeps its original read time. Returns ErrNotFound for an
// unknown id.
func (s *Store) MarkRead(id string, atMS int64) error {
	res, err := s.execWrite(
		`UPDATE notifications SET read_at_ms = ? WHERE id = ? AND read_at_ms = 0`,
		atMS, id)
	if err != nil {
		return fmt.Errorf("store: mark read: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("store: mark read: %w", err)
	}
	if n == 0 {
		// Distinguish "already read" (fine, idempotent) from "unknown id".
		var exists int
		err := s.db.QueryRow(
			`SELECT 1 FROM notifications WHERE id = ?`, id).Scan(&exists)
		if errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("%w: notification %q", ErrNotFound, id)
		}
		if err != nil {
			return fmt.Errorf("store: mark read: %w", err)
		}
	}
	return nil
}

// MarkAllRead marks every unread notification of a session read at atMS and
// returns how many it marked.
func (s *Store) MarkAllRead(session string, atMS int64) (int64, error) {
	res, err := s.execWrite(
		`UPDATE notifications SET read_at_ms = ? WHERE session = ? AND read_at_ms = 0`,
		atMS, session)
	if err != nil {
		return 0, fmt.Errorf("store: mark all read: %w", err)
	}
	return res.RowsAffected()
}

// ListNotifications returns a session's notifications newest-first (creation
// time, then id for a stable order). With unreadOnly true only unread rows are
// returned. A limit <= 0 means no limit.
func (s *Store) ListNotifications(session string, unreadOnly bool, limit int) ([]NotificationRow, error) {
	if limit <= 0 {
		limit = -1 // SQLite: negative LIMIT means unlimited.
	}
	q := `SELECT ` + notificationColumns + ` FROM notifications WHERE session = ?`
	if unreadOnly {
		q += ` AND read_at_ms = 0`
	}
	q += ` ORDER BY created_ms DESC, id DESC LIMIT ?`
	rows, err := s.db.Query(q, session, limit)
	if err != nil {
		return nil, fmt.Errorf("store: list notifications: %w", err)
	}
	defer rows.Close()
	var out []NotificationRow
	for rows.Next() {
		var n NotificationRow
		if err := rows.Scan(&n.ID, &n.Session, &n.Workspace, &n.Pane, &n.Kind,
			&n.Title, &n.Body, &n.CreatedMS, &n.ReadAtMS); err != nil {
			return nil, fmt.Errorf("store: scan notification: %w", err)
		}
		out = append(out, n)
	}
	return out, rows.Err()
}

// LatestUnread returns the newest unread notification of a session, or
// ErrNotFound when everything is read.
func (s *Store) LatestUnread(session string) (NotificationRow, error) {
	var n NotificationRow
	err := s.db.QueryRow(`
		SELECT `+notificationColumns+` FROM notifications
		WHERE session = ? AND read_at_ms = 0
		ORDER BY created_ms DESC, id DESC LIMIT 1`, session,
	).Scan(&n.ID, &n.Session, &n.Workspace, &n.Pane, &n.Kind,
		&n.Title, &n.Body, &n.CreatedMS, &n.ReadAtMS)
	if errors.Is(err, sql.ErrNoRows) {
		return NotificationRow{}, fmt.Errorf("%w: no unread notification for session %q", ErrNotFound, session)
	}
	if err != nil {
		return NotificationRow{}, fmt.Errorf("store: latest unread: %w", err)
	}
	return n, nil
}

// CountUnread returns the number of unread notifications for a session.
func (s *Store) CountUnread(session string) (int64, error) {
	var n int64
	err := s.db.QueryRow(
		`SELECT COUNT(*) FROM notifications WHERE session = ? AND read_at_ms = 0`,
		session).Scan(&n)
	if err != nil {
		return 0, fmt.Errorf("store: count unread: %w", err)
	}
	return n, nil
}
