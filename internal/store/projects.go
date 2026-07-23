package store

import (
	"database/sql"
	"errors"
	"fmt"
)

// Project trust states. Registered is the initial state (registration alone
// confers nothing); the operator-facing transitions move a project to
// approved, denied, or revoked, always with a strictly increasing epoch.
const (
	ProjectStateRegistered = "registered"
	ProjectStateApproved   = "approved"
	ProjectStateDenied     = "denied"
	ProjectStateRevoked    = "revoked"
)

// ProjectRow is one registered project. Key is the durable project key
// (SHA-256(realpath || dev || ino) computed above the store); realpath/dev/ino
// record the filesystem identity behind it. ValidationScheme/ValidationValue
// persist the replacement-validation discriminator (G-lane F2) — the second
// identity factor that detects a replaced root under (dev, ino) reuse; empty
// values mean "absent" (pre-migration row) and deny trust reuse upstream.
type ProjectRow struct {
	Key              string
	Realpath         string
	Dev              uint64
	Ino              uint64
	State            string
	Epoch            uint64
	ValidationScheme string
	ValidationValue  string
}

// UpsertProject registers a project or refreshes its recorded filesystem
// identity and replacement-validation discriminator. It never touches state
// or epoch: registration confers nothing, and trust transitions go
// exclusively through SetProjectState.
func (s *Store) UpsertProject(key, realpath string, dev, ino uint64, vscheme, vvalue string) error {
	_, err := s.execWrite(`
		INSERT INTO projects (key, realpath, dev, ino, validation_scheme, validation_value)
		VALUES (?, ?, ?, ?, ?, ?)
		ON CONFLICT(key) DO UPDATE SET realpath = excluded.realpath,
			dev = excluded.dev, ino = excluded.ino,
			validation_scheme = excluded.validation_scheme,
			validation_value = excluded.validation_value`,
		key, realpath, int64(dev), int64(ino), vscheme, vvalue)
	if err != nil {
		return fmt.Errorf("store: upsert project: %w", err)
	}
	return nil
}

// UpdateProjectValidation persists only the replacement-validation
// discriminator of an already-registered project (state/epoch untouched).
// Returns ErrNotFound for an unregistered key.
func (s *Store) UpdateProjectValidation(key, vscheme, vvalue string) error {
	res, err := s.execWrite(
		`UPDATE projects SET validation_scheme = ?, validation_value = ? WHERE key = ?`,
		vscheme, vvalue, key)
	if err != nil {
		return fmt.Errorf("store: update project validation: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("store: update project validation: %w", err)
	}
	if n == 0 {
		return fmt.Errorf("%w: project %q", ErrNotFound, key)
	}
	return nil
}

// SetProjectState transitions a project to approved, denied, or revoked with
// newEpoch. The write REFUSES any newEpoch <= the stored epoch with
// ErrEpochNotMonotonic: epochs never decrease (ADR-0005 non-rollback of
// trust), enforced at the storage layer independently of the control actor.
// Returns ErrNotFound for an unregistered key and ErrInvalidState for a state
// outside the trust vocabulary.
func (s *Store) SetProjectState(key, state string, newEpoch uint64) error {
	switch state {
	case ProjectStateApproved, ProjectStateDenied, ProjectStateRevoked:
	default:
		return fmt.Errorf("%w: %q", ErrInvalidState, state)
	}
	return s.inTx(func(tx *sql.Tx) error {
		var current uint64
		err := tx.QueryRow(`SELECT epoch FROM projects WHERE key = ?`, key).Scan(&current)
		if errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("%w: project %q", ErrNotFound, key)
		}
		if err != nil {
			return fmt.Errorf("store: read project epoch: %w", err)
		}
		if newEpoch <= current {
			return fmt.Errorf("%w: project %q epoch %d -> %d",
				ErrEpochNotMonotonic, key, current, newEpoch)
		}
		if _, err := tx.Exec(
			`UPDATE projects SET state = ?, epoch = ? WHERE key = ?`,
			state, int64(newEpoch), key,
		); err != nil {
			return fmt.Errorf("store: set project state: %w", err)
		}
		return nil
	})
}

// GetProject returns one project row, or ErrNotFound.
func (s *Store) GetProject(key string) (ProjectRow, error) {
	var p ProjectRow
	var dev, ino, epoch int64
	err := s.db.QueryRow(
		`SELECT key, realpath, dev, ino, state, epoch, validation_scheme, validation_value
		 FROM projects WHERE key = ?`, key,
	).Scan(&p.Key, &p.Realpath, &dev, &ino, &p.State, &epoch, &p.ValidationScheme, &p.ValidationValue)
	if errors.Is(err, sql.ErrNoRows) {
		return ProjectRow{}, fmt.Errorf("%w: project %q", ErrNotFound, key)
	}
	if err != nil {
		return ProjectRow{}, fmt.Errorf("store: get project: %w", err)
	}
	p.Dev, p.Ino, p.Epoch = uint64(dev), uint64(ino), uint64(epoch)
	return p, nil
}

// ListProjects returns every registered project ordered by key.
func (s *Store) ListProjects() ([]ProjectRow, error) {
	rows, err := s.db.Query(
		`SELECT key, realpath, dev, ino, state, epoch, validation_scheme, validation_value
		 FROM projects ORDER BY key`)
	if err != nil {
		return nil, fmt.Errorf("store: list projects: %w", err)
	}
	defer rows.Close()
	var out []ProjectRow
	for rows.Next() {
		var p ProjectRow
		var dev, ino, epoch int64
		if err := rows.Scan(&p.Key, &p.Realpath, &dev, &ino, &p.State, &epoch, &p.ValidationScheme, &p.ValidationValue); err != nil {
			return nil, fmt.Errorf("store: scan project: %w", err)
		}
		p.Dev, p.Ino, p.Epoch = uint64(dev), uint64(ino), uint64(epoch)
		out = append(out, p)
	}
	return out, rows.Err()
}
