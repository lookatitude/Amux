// Package store implements the daemon's durable SQLite state (ADR-0005): the
// SOLE authority for project trust epochs, grants, revocation, and audit, and
// the canonical live record for notifications during crash recovery. It is the
// B8 realization of the persist contract's AuthorityControlActor and
// AuthorityNotifyService durability rows (internal/persist/manifest.go).
//
// The store is a leaf package: stdlib plus the cgo-free modernc.org/sqlite
// driver (ADR-0007 cgo prohibition), no other internal imports. Every row shape
// is defined locally; the trust engine above projects them onto its own types.
//
// Invariants enforced HERE, at the storage layer, not only in the actor above:
//
//   - Epochs never decrease (ADR-0005 "SQLite precedence and non-rollback of
//     trust"): SetProjectState refuses a non-increasing epoch with
//     ErrEpochNotMonotonic.
//   - Grants are never deleted: revocation deactivates; history is retained
//     forever. There is deliberately no delete API.
//   - Audit is append-only with a monotonic sequence that survives reopen
//     (AUTOINCREMENT); there is no update or delete API.
//   - Schema migrations are ordered, transactional, and forward-only at
//     runtime: a database recorded at a newer version than this binary
//     supports fails closed with ErrNewerSchema, never a partial write or a
//     downgrade.
//   - A snapshot restore may import ONLY the notification/read-state export
//     matching the committed checkpoint id; ImportNotifications refuses a
//     mismatch with ErrCheckpointMismatch and never touches the projects,
//     grants, or audit tables.
package store

import (
	"database/sql"
	"fmt"
	"net/url"
	"os"
	"path/filepath"

	// Register the cgo-free "sqlite" database/sql driver (ADR-0007: the
	// modernc pure-Go route is the sanctioned SQLite dependency).
	_ "modernc.org/sqlite"
)

// dbFileName is the single database file under the store directory.
const dbFileName = "amux.db"

// busyTimeoutMS bounds how long a connection waits on SQLite's internal write
// lock before returning SQLITE_BUSY. SQLite serializes writers; the timeout
// makes concurrent daemon writers queue instead of erroring.
const busyTimeoutMS = 5000

// Store owns the single SQLite database backing trust, grants, audit,
// notifications, metadata, and event cursors. All methods are safe for
// concurrent use; SQLite serializes writes under WAL with a busy timeout.
type Store struct {
	db   *sql.DB
	path string
	// trustTxFailpoint injects deterministic failures between the statements
	// of ApplyTrustTransition. Test scaffolding only (fail-closed direction
	// only); nil in production. See SetTrustTransitionFailpoint.
	trustTxFailpoint func(stage string) error
}

// Open creates or opens <dir>/amux.db and applies any pending migrations.
// The directory is created 0700 (owner-only, matching the daemon's private
// state directory posture) if it does not exist. The connection enables WAL
// journaling (readers never block the writer, and a torn write is never
// loaded — ADR-0005 "a partial write must never be loaded"), foreign keys,
// and a busy timeout.
//
// Re-opening an up-to-date database is idempotent. Opening a database whose
// recorded schema version is newer than this binary supports fails closed
// with ErrNewerSchema (ADR-0005: migrations are forward-only at runtime).
func Open(dir string) (*Store, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("store: create dir: %w", err)
	}
	path := filepath.Join(dir, dbFileName)
	dsn := "file:" + url.PathEscape(path) +
		fmt.Sprintf("?_pragma=busy_timeout(%d)&_pragma=journal_mode(WAL)&_pragma=foreign_keys(1)", busyTimeoutMS)
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("store: open %s: %w", path, err)
	}
	s := &Store{db: db, path: path}
	if err := s.migrate(); err != nil {
		_ = db.Close()
		return nil, err
	}
	return s, nil
}

// Path returns the absolute path of the database file (diagnostics only).
func (s *Store) Path() string { return s.path }

// Close closes the underlying database. The store is unusable afterwards.
func (s *Store) Close() error {
	return s.db.Close()
}

// inTx runs fn inside one transaction, committing on nil and rolling back on
// error, so every multi-statement mutation is atomic (ADR-0005: never partial
// writes).
func (s *Store) inTx(fn func(tx *sql.Tx) error) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	if err := fn(tx); err != nil {
		_ = tx.Rollback()
		return err
	}
	return tx.Commit()
}
