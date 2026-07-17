package store

import (
	"database/sql"
	"fmt"
	"time"
)

// migration is one forward-only schema step. Statements are applied in order
// inside a single transaction together with the schema_migrations record, so a
// migration either fully applies or leaves the database untouched (ADR-0005:
// migrations are ordered, transactional, forward-only at runtime).
type migration struct {
	version int
	stmts   []string
}

// migrations is the ordered, embedded migration list. Entries are append-only:
// released versions are never edited or removed, and version numbers are
// contiguous starting at 1.
var migrations = []migration{
	{
		version: 1,
		stmts: []string{
			// Trust: one row per registered project. State/epoch mutate only
			// through SetProjectState, which enforces epoch monotonicity.
			`CREATE TABLE projects (
				key      TEXT PRIMARY KEY,
				realpath TEXT NOT NULL,
				dev      INTEGER NOT NULL,
				ino      INTEGER NOT NULL,
				state    TEXT NOT NULL DEFAULT 'registered',
				epoch    INTEGER NOT NULL DEFAULT 0
			)`,
			// Grants: rows are never deleted (history retained forever);
			// revocation flips active to 0.
			`CREATE TABLE grants (
				id                 TEXT PRIMARY KEY,
				project_key        TEXT NOT NULL REFERENCES projects(key),
				hook_id            TEXT NOT NULL,
				exec_path          TEXT NOT NULL,
				exec_sha256        TEXT NOT NULL,
				config_sha256      TEXT NOT NULL,
				events_json        TEXT NOT NULL,
				scope_kind         TEXT NOT NULL,
				fixed_path         TEXT NOT NULL DEFAULT '',
				env_allowlist_json TEXT NOT NULL,
				timeout_ms         INTEGER NOT NULL,
				output_cap_bytes   INTEGER NOT NULL,
				bound_epoch        INTEGER NOT NULL,
				active             INTEGER NOT NULL
			)`,
			`CREATE INDEX grants_by_project ON grants(project_key)`,
			// Audit: append-only ledger. AUTOINCREMENT makes seq monotonic
			// across reopen and never reused, even after the highest row would
			// otherwise be recycled.
			`CREATE TABLE audit (
				seq          INTEGER PRIMARY KEY AUTOINCREMENT,
				kind         TEXT NOT NULL,
				project_key  TEXT NOT NULL,
				epoch        INTEGER NOT NULL,
				code         TEXT NOT NULL DEFAULT '',
				at_ms        INTEGER NOT NULL,
				details_json TEXT NOT NULL DEFAULT ''
			)`,
			// Notifications: live SQLite is canonical during crash recovery
			// (ADR-0005). read_at_ms = 0 means unread.
			`CREATE TABLE notifications (
				id         TEXT PRIMARY KEY,
				session    TEXT NOT NULL,
				workspace  TEXT NOT NULL,
				pane       TEXT NOT NULL,
				kind       TEXT NOT NULL,
				title      TEXT NOT NULL,
				body       TEXT NOT NULL,
				created_ms INTEGER NOT NULL,
				read_at_ms INTEGER NOT NULL DEFAULT 0
			)`,
			`CREATE INDEX notifications_by_session ON notifications(session, created_ms)`,
			// Metadata KV: boot id continuity, committed notification
			// checkpoint id, and similar single-value durability records.
			`CREATE TABLE meta (
				key   TEXT PRIMARY KEY,
				value TEXT NOT NULL
			)`,
			// Event cursors: per (client, session) re-establishment support
			// (ADR-0004 cursor reset pairs with ADR-0005 durability).
			`CREATE TABLE cursors (
				client  TEXT NOT NULL,
				session TEXT NOT NULL,
				seq     INTEGER NOT NULL,
				PRIMARY KEY (client, session)
			)`,
		},
	},
	{
		// Replacement-validation discriminator (G-lane F2): a separately
		// persisted second identity factor beside the frozen (realpath, dev,
		// ino)-derived key, so a replaced root is detected even when the
		// filesystem reuses the inode (overlayfs). Rows predating this
		// migration keep the empty default — an ABSENT discriminator, which
		// the control actor treats as ambiguous: trust reuse is denied on the
		// first post-upgrade registration.
		version: 2,
		stmts: []string{
			`ALTER TABLE projects ADD COLUMN validation_scheme TEXT NOT NULL DEFAULT ''`,
			`ALTER TABLE projects ADD COLUMN validation_value  TEXT NOT NULL DEFAULT ''`,
		},
	},
}

// supportedSchemaVersion is the highest schema version this binary can open.
func supportedSchemaVersion() int { return migrations[len(migrations)-1].version }

// migrate bootstraps schema_migrations, fails closed on a newer-than-supported
// database, and applies each pending migration in its own transaction.
// Re-running against an up-to-date database is a no-op (idempotent reopen).
func (s *Store) migrate() error {
	if _, err := s.execWrite(`CREATE TABLE IF NOT EXISTS schema_migrations (
		version         INTEGER PRIMARY KEY,
		applied_unix_ms INTEGER NOT NULL
	)`); err != nil {
		return fmt.Errorf("store: bootstrap schema_migrations: %w", err)
	}

	var current int
	if err := s.db.QueryRow(
		`SELECT COALESCE(MAX(version), 0) FROM schema_migrations`,
	).Scan(&current); err != nil {
		return fmt.Errorf("store: read schema version: %w", err)
	}

	// Forward-only at runtime (ADR-0005): a database written by a newer binary
	// is refused, never partially rewritten or downgraded.
	if current > supportedSchemaVersion() {
		return fmt.Errorf("%w: database at version %d, binary supports %d",
			ErrNewerSchema, current, supportedSchemaVersion())
	}

	for _, m := range migrations {
		if m.version <= current {
			continue
		}
		if err := s.applyMigration(m); err != nil {
			return err
		}
	}
	return nil
}

// applyMigration runs one migration's statements plus its version record in a
// single transaction: it either fully applies or leaves no trace.
func (s *Store) applyMigration(m migration) error {
	return s.inTx(func(tx *sql.Tx) error {
		for _, stmt := range m.stmts {
			if _, err := tx.Exec(stmt); err != nil {
				return fmt.Errorf("store: migration %d: %w", m.version, err)
			}
		}
		if _, err := tx.Exec(
			`INSERT INTO schema_migrations (version, applied_unix_ms) VALUES (?, ?)`,
			m.version, time.Now().UnixMilli(),
		); err != nil {
			return fmt.Errorf("store: record migration %d: %w", m.version, err)
		}
		return nil
	})
}
