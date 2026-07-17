package store

import (
	"database/sql"
	"errors"
	"fmt"
)

// SetMeta writes one metadata key/value (boot id continuity, notification
// checkpoint ids, and similar single-value durability records). Overwrites an
// existing key.
func (s *Store) SetMeta(key, value string) error {
	_, err := s.execWrite(`
		INSERT INTO meta (key, value) VALUES (?, ?)
		ON CONFLICT(key) DO UPDATE SET value = excluded.value`, key, value)
	if err != nil {
		return fmt.Errorf("store: set meta: %w", err)
	}
	return nil
}

// GetMeta returns one metadata value, or ErrNotFound.
func (s *Store) GetMeta(key string) (string, error) {
	var v string
	err := s.db.QueryRow(`SELECT value FROM meta WHERE key = ?`, key).Scan(&v)
	if errors.Is(err, sql.ErrNoRows) {
		return "", fmt.Errorf("%w: meta key %q", ErrNotFound, key)
	}
	if err != nil {
		return "", fmt.Errorf("store: get meta: %w", err)
	}
	return v, nil
}
