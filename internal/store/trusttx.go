package store

import (
	"database/sql"
	"errors"
	"fmt"
)

// TrustTransitionRow describes one audited project trust transition committed
// atomically by ApplyTrustTransition (G-lane F1): the project's next state,
// strictly-increasing epoch, and replacement-validation discriminator; optional
// deactivation of every active grant; the main audit record; and one
// per-deactivated-grant audit record. Everything lands in ONE SQL transaction
// or not at all — a failure at any stage leaves the previous transition fully
// intact on disk, so a retry never trips ErrEpochNotMonotonic.
type TrustTransitionRow struct {
	Key              string
	State            string
	Epoch            uint64
	ValidationScheme string
	ValidationValue  string
	// DeactivateGrants marks every active grant of the project inactive inside
	// the same commit (revocation semantics; rows retained forever, ADR-0005).
	DeactivateGrants bool
	// Audit is the transition's main record (Seq store-assigned); nil for the
	// deliberately unaudited deny transition.
	Audit *AuditRow
	// GrantAudit is the template appended once per deactivated grant, strictly
	// after Audit (AUD-4 ordering); nil suppresses per-grant records.
	GrantAudit *AuditRow
}

// SetTrustTransitionFailpoint installs fn as a deterministic failure-injection
// point inside ApplyTrustTransition: it is invoked with a stage label before
// each statement ("project-update", "deactivate-grants", "audit-main",
// "audit-grant") and before "commit"; a non-nil return aborts and rolls back
// the transaction at that stage. Test scaffolding ONLY — it can only force a
// transition to fail closed, never to skip a stage — and it must not be set
// concurrently with live transitions. Production code never calls this.
func (s *Store) SetTrustTransitionFailpoint(fn func(stage string) error) {
	s.trustTxFailpoint = fn
}

func (s *Store) trustTxStage(stage string) error {
	if s.trustTxFailpoint == nil {
		return nil
	}
	return s.trustTxFailpoint(stage)
}

// ApplyTrustTransition commits t atomically and returns the IDs (ordered) of
// the grants it deactivated — only after the commit is durable. It enforces
// the same storage-layer invariants as SetProjectState: the state vocabulary
// and the strictly increasing epoch (ErrEpochNotMonotonic, ADR-0005
// non-rollback of trust), independently of the control actor above.
func (s *Store) ApplyTrustTransition(t TrustTransitionRow) ([]string, error) {
	switch t.State {
	case ProjectStateApproved, ProjectStateDenied, ProjectStateRevoked:
	default:
		return nil, fmt.Errorf("%w: %q", ErrInvalidState, t.State)
	}
	var ids []string
	err := s.inTx(func(tx *sql.Tx) error {
		var current uint64
		err := tx.QueryRow(`SELECT epoch FROM projects WHERE key = ?`, t.Key).Scan(&current)
		if errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("%w: project %q", ErrNotFound, t.Key)
		}
		if err != nil {
			return fmt.Errorf("store: read project epoch: %w", err)
		}
		if t.Epoch <= current {
			return fmt.Errorf("%w: project %q epoch %d -> %d",
				ErrEpochNotMonotonic, t.Key, current, t.Epoch)
		}
		if err := s.trustTxStage("project-update"); err != nil {
			return err
		}
		if _, err := tx.Exec(
			`UPDATE projects SET state = ?, epoch = ?, validation_scheme = ?, validation_value = ?
			 WHERE key = ?`,
			t.State, int64(t.Epoch), t.ValidationScheme, t.ValidationValue, t.Key,
		); err != nil {
			return fmt.Errorf("store: set project state: %w", err)
		}
		if t.DeactivateGrants {
			if err := s.trustTxStage("deactivate-grants"); err != nil {
				return err
			}
			rows, err := tx.Query(
				`SELECT id FROM grants WHERE project_key = ? AND active = 1 ORDER BY id`, t.Key)
			if err != nil {
				return fmt.Errorf("store: list active grants: %w", err)
			}
			for rows.Next() {
				var id string
				if err := rows.Scan(&id); err != nil {
					rows.Close()
					return fmt.Errorf("store: scan grant id: %w", err)
				}
				ids = append(ids, id)
			}
			if err := rows.Close(); err != nil {
				return fmt.Errorf("store: list active grants: %w", err)
			}
			if _, err := tx.Exec(
				`UPDATE grants SET active = 0 WHERE project_key = ? AND active = 1`, t.Key,
			); err != nil {
				return fmt.Errorf("store: deactivate grants: %w", err)
			}
		}
		if t.Audit != nil {
			if err := s.trustTxStage("audit-main"); err != nil {
				return err
			}
			if err := appendAuditTx(tx, *t.Audit); err != nil {
				return err
			}
		}
		if t.GrantAudit != nil {
			for range ids {
				if err := s.trustTxStage("audit-grant"); err != nil {
					return err
				}
				if err := appendAuditTx(tx, *t.GrantAudit); err != nil {
					return err
				}
			}
		}
		return s.trustTxStage("commit")
	})
	if err != nil {
		// The transaction rolled back: nothing was deactivated.
		return nil, err
	}
	return ids, nil
}

// appendAuditTx appends one audit record inside an open transaction; the
// AUTOINCREMENT sequence keeps ordering identical to AppendAudit.
func appendAuditTx(tx *sql.Tx, a AuditRow) error {
	if _, err := tx.Exec(`
		INSERT INTO audit (kind, project_key, epoch, code, at_ms, details_json)
		VALUES (?, ?, ?, ?, ?, ?)`,
		a.Kind, a.ProjectKey, int64(a.Epoch), a.Code, a.AtMS, a.DetailsJSON); err != nil {
		return fmt.Errorf("store: append audit: %w", err)
	}
	return nil
}
