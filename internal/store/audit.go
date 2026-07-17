package store

import "fmt"

// AuditRow is one append-only audit record. Seq is assigned by the store
// (AUTOINCREMENT): monotonic, never reused, and it survives reopen. There is
// no update or delete API — the ledger is append-only by construction, so a
// snapshot restore or any later code path cannot erase audit history
// (ADR-0005: snapshots can never ... erase audit).
type AuditRow struct {
	Seq        uint64
	Kind       string
	ProjectKey string
	Epoch      uint64
	// Code is the v1 error code for a denial record, empty otherwise.
	Code        string
	AtMS        int64
	DetailsJSON string
}

// AppendAudit appends one audit record and returns its assigned monotonic
// sequence number. The caller-provided Seq is ignored.
func (s *Store) AppendAudit(a AuditRow) (uint64, error) {
	res, err := s.db.Exec(`
		INSERT INTO audit (kind, project_key, epoch, code, at_ms, details_json)
		VALUES (?, ?, ?, ?, ?, ?)`,
		a.Kind, a.ProjectKey, int64(a.Epoch), a.Code, a.AtMS, a.DetailsJSON)
	if err != nil {
		return 0, fmt.Errorf("store: append audit: %w", err)
	}
	seq, err := res.LastInsertId()
	if err != nil {
		return 0, fmt.Errorf("store: audit seq: %w", err)
	}
	return uint64(seq), nil
}

// ListAudit returns audit records with seq >= fromSeq in ascending sequence
// order. A limit <= 0 means no limit.
func (s *Store) ListAudit(fromSeq uint64, limit int) ([]AuditRow, error) {
	if limit <= 0 {
		limit = -1 // SQLite: negative LIMIT means unlimited.
	}
	rows, err := s.db.Query(`
		SELECT seq, kind, project_key, epoch, code, at_ms, details_json
		FROM audit WHERE seq >= ? ORDER BY seq LIMIT ?`,
		int64(fromSeq), limit)
	if err != nil {
		return nil, fmt.Errorf("store: list audit: %w", err)
	}
	defer rows.Close()
	var out []AuditRow
	for rows.Next() {
		var a AuditRow
		var seq, epoch int64
		if err := rows.Scan(&seq, &a.Kind, &a.ProjectKey, &epoch, &a.Code,
			&a.AtMS, &a.DetailsJSON); err != nil {
			return nil, fmt.Errorf("store: scan audit: %w", err)
		}
		a.Seq, a.Epoch = uint64(seq), uint64(epoch)
		out = append(out, a)
	}
	return out, rows.Err()
}

// CountAudit returns the total number of audit records.
func (s *Store) CountAudit() (uint64, error) {
	var n int64
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM audit`).Scan(&n); err != nil {
		return 0, fmt.Errorf("store: count audit: %w", err)
	}
	return uint64(n), nil
}
