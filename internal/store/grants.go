package store

import (
	"database/sql"
	"errors"
	"fmt"
)

// GrantRow is one hook grant record, active or inactive. Rows are NEVER
// deleted: revocation deactivates, and history is retained forever (ADR-0005:
// snapshots can never reactivate grants or erase audit; the audit/grant ledger
// outlives every trust transition). There is deliberately no delete API.
type GrantRow struct {
	ID               string
	ProjectKey       string
	HookID           string
	ExecPath         string
	ExecSHA256       string
	ConfigSHA256     string
	EventsJSON       string
	ScopeKind        string
	FixedPath        string
	EnvAllowlistJSON string
	TimeoutMS        int64
	OutputCapBytes   int64
	// BoundEpoch is the project epoch the grant was approved under; the trust
	// engine compares it against the live epoch to detect staleness.
	BoundEpoch uint64
	Active     bool
}

// grantColumns is the shared column list for grant scans.
const grantColumns = `id, project_key, hook_id, exec_path, exec_sha256,
	config_sha256, events_json, scope_kind, fixed_path, env_allowlist_json,
	timeout_ms, output_cap_bytes, bound_epoch, active`

// InsertGrant records one approved grant. The project must already be
// registered (foreign key), keeping every grant traceable to a trust row.
func (s *Store) InsertGrant(g GrantRow) error {
	_, err := s.execWrite(`
		INSERT INTO grants (`+grantColumns+`)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		g.ID, g.ProjectKey, g.HookID, g.ExecPath, g.ExecSHA256,
		g.ConfigSHA256, g.EventsJSON, g.ScopeKind, g.FixedPath,
		g.EnvAllowlistJSON, g.TimeoutMS, g.OutputCapBytes,
		int64(g.BoundEpoch), g.Active)
	if err != nil {
		return fmt.Errorf("store: insert grant: %w", err)
	}
	return nil
}

// DeactivateGrantsForProject marks every grant of a project inactive —
// revocation semantics. Rows are retained: history is never deleted, and a
// later snapshot import can never flip these back active (ADR-0005).
// Returns the number of grants deactivated.
func (s *Store) DeactivateGrantsForProject(projectKey string) (int64, error) {
	res, err := s.execWrite(
		`UPDATE grants SET active = 0 WHERE project_key = ? AND active = 1`,
		projectKey)
	if err != nil {
		return 0, fmt.Errorf("store: deactivate grants: %w", err)
	}
	return res.RowsAffected()
}

// GetGrant returns one grant by id, or ErrNotFound.
func (s *Store) GetGrant(id string) (GrantRow, error) {
	g, err := scanGrant(s.db.QueryRow(
		`SELECT `+grantColumns+` FROM grants WHERE id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return GrantRow{}, fmt.Errorf("%w: grant %q", ErrNotFound, id)
	}
	if err != nil {
		return GrantRow{}, fmt.Errorf("store: get grant: %w", err)
	}
	return g, nil
}

// ListGrants returns a project's grants ordered by id. With includeInactive
// false only active grants are returned; true returns the full retained
// history.
func (s *Store) ListGrants(projectKey string, includeInactive bool) ([]GrantRow, error) {
	q := `SELECT ` + grantColumns + ` FROM grants WHERE project_key = ?`
	if !includeInactive {
		q += ` AND active = 1`
	}
	q += ` ORDER BY id`
	rows, err := s.db.Query(q, projectKey)
	if err != nil {
		return nil, fmt.Errorf("store: list grants: %w", err)
	}
	defer rows.Close()
	var out []GrantRow
	for rows.Next() {
		g, err := scanGrant(rows)
		if err != nil {
			return nil, fmt.Errorf("store: scan grant: %w", err)
		}
		out = append(out, g)
	}
	return out, rows.Err()
}

// rowScanner abstracts *sql.Row and *sql.Rows for shared grant scanning.
type rowScanner interface {
	Scan(dest ...any) error
}

func scanGrant(r rowScanner) (GrantRow, error) {
	var g GrantRow
	var boundEpoch int64
	err := r.Scan(&g.ID, &g.ProjectKey, &g.HookID, &g.ExecPath, &g.ExecSHA256,
		&g.ConfigSHA256, &g.EventsJSON, &g.ScopeKind, &g.FixedPath,
		&g.EnvAllowlistJSON, &g.TimeoutMS, &g.OutputCapBytes,
		&boundEpoch, &g.Active)
	if err != nil {
		return GrantRow{}, err
	}
	g.BoundEpoch = uint64(boundEpoch)
	return g, nil
}
