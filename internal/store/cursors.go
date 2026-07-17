package store

import (
	"database/sql"
	"errors"
	"fmt"
)

// SetCursor durably records the last event sequence a client acknowledged for
// a session, supporting cursor re-establishment after reconnect or daemon
// restart (ADR-0004 event cursor; durability row in ADR-0005). Overwrites any
// prior cursor for the (client, session) pair.
func (s *Store) SetCursor(client, session string, seq uint64) error {
	_, err := s.db.Exec(`
		INSERT INTO cursors (client, session, seq) VALUES (?, ?, ?)
		ON CONFLICT(client, session) DO UPDATE SET seq = excluded.seq`,
		client, session, int64(seq))
	if err != nil {
		return fmt.Errorf("store: set cursor: %w", err)
	}
	return nil
}

// GetCursor returns the recorded cursor for a (client, session) pair, or
// ErrNotFound when none was ever set.
func (s *Store) GetCursor(client, session string) (uint64, error) {
	var seq int64
	err := s.db.QueryRow(
		`SELECT seq FROM cursors WHERE client = ? AND session = ?`,
		client, session).Scan(&seq)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, fmt.Errorf("%w: cursor %q/%q", ErrNotFound, client, session)
	}
	if err != nil {
		return 0, fmt.Errorf("store: get cursor: %w", err)
	}
	return uint64(seq), nil
}
