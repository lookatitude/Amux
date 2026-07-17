// truststore.go adapts the SQLite store (internal/store) to the control
// actor's TrustStore seam, making trust epochs, grants, and audit SQLite-only
// and durable (ADR-0005). The control actor writes through synchronously; a
// failed durable write aborts the trust transition (fail closed). The adapter
// is pure translation — no policy lives here.
package daemon

import (
	"encoding/json"
	"errors"
	"fmt"

	v1 "github.com/amux-run/amux/api/v1"

	"github.com/amux-run/amux/internal/control"
	"github.com/amux-run/amux/internal/platform"
	"github.com/amux-run/amux/internal/store"
)

// sqliteTrustStore implements control.TrustStore over *store.Store.
type sqliteTrustStore struct{ st *store.Store }

// NewTrustStore wraps st as the control actor's durable trust seam.
func NewTrustStore(st *store.Store) control.TrustStore {
	return &sqliteTrustStore{st: st}
}

func (s *sqliteTrustStore) SaveProject(p control.ProjectRecord) error {
	if err := s.st.UpsertProject(string(p.Key), p.Root, p.Identity.Dev, p.Identity.Ino,
		p.Validation.Scheme, p.Validation.Value); err != nil {
		return err
	}
	if p.State == control.StateNone {
		// Registration confers nothing (HA-3b): the upsert records identity
		// only. State/epoch move exclusively on real trust transitions — the
		// store refuses the empty state and a non-increasing epoch, so writing
		// here would fail closed and block registration entirely.
		return nil
	}
	return s.st.SetProjectState(string(p.Key), string(p.State), p.Epoch)
}

func (s *sqliteTrustStore) LoadProject(key control.ProjectKey) (control.ProjectRecord, bool, error) {
	row, err := s.st.GetProject(string(key))
	if errors.Is(err, store.ErrNotFound) {
		return control.ProjectRecord{}, false, nil
	}
	if err != nil {
		return control.ProjectRecord{}, false, err
	}
	state := control.TrustState(row.State)
	if row.State == store.ProjectStateRegistered {
		state = control.StateNone
	}
	return control.ProjectRecord{
		Key:        control.ProjectKey(row.Key),
		Root:       row.Realpath,
		Identity:   platform.FSIdentity{Dev: row.Dev, Ino: row.Ino},
		State:      state,
		Epoch:      row.Epoch,
		Validation: platform.ValidationID{Scheme: row.ValidationScheme, Value: row.ValidationValue},
	}, true, nil
}

func (s *sqliteTrustStore) SaveValidation(key control.ProjectKey, v platform.ValidationID) error {
	return s.st.UpdateProjectValidation(string(key), v.Scheme, v.Value)
}

func (s *sqliteTrustStore) SaveGrant(g control.GrantRecord) error {
	events, err := json.Marshal(g.AllowedEvents)
	if err != nil {
		return fmt.Errorf("daemon: encode grant events: %w", err)
	}
	env, err := json.Marshal(g.EnvAllowlist)
	if err != nil {
		return fmt.Errorf("daemon: encode grant env allowlist: %w", err)
	}
	return s.st.InsertGrant(store.GrantRow{
		ID:               g.ID,
		ProjectKey:       string(g.Project),
		HookID:           g.HookID,
		ExecPath:         g.ExecPath,
		ExecSHA256:       g.ExecSHA256,
		ConfigSHA256:     g.ConfigSHA256,
		EventsJSON:       string(events),
		ScopeKind:        string(g.Scope),
		FixedPath:        g.FixedPath,
		EnvAllowlistJSON: string(env),
		TimeoutMS:        g.TimeoutMS,
		OutputCapBytes:   g.OutputCap,
		BoundEpoch:       g.BoundEpoch,
		Active:           g.Active,
	})
}

// ApplyTransition executes the whole audited trust transition — project
// state + epoch + replacement discriminator, grant deactivation, the main
// audit record, and one grant_inactive record per affected grant — in ONE
// real SQL transaction (store.ApplyTrustTransition, G-lane F1). The exact
// deactivated grant IDs are returned only after the commit is durable; any
// failure rolls the database back to the previous transition in full.
func (s *sqliteTrustStore) ApplyTransition(t control.TrustTransition) ([]string, error) {
	row := store.TrustTransitionRow{
		Key:              string(t.Project.Key),
		State:            string(t.Project.State),
		Epoch:            t.Project.Epoch,
		ValidationScheme: t.Project.Validation.Scheme,
		ValidationValue:  t.Project.Validation.Value,
		DeactivateGrants: t.DeactivateGrants,
	}
	if t.Audit != nil {
		ar := auditRow(*t.Audit)
		row.Audit = &ar
	}
	if t.GrantAudit != nil {
		ar := auditRow(*t.GrantAudit)
		row.GrantAudit = &ar
	}
	return s.st.ApplyTrustTransition(row)
}

// auditRow translates a control audit record to its storage row.
func auditRow(r control.AuditRecord) store.AuditRow {
	return store.AuditRow{
		Kind:       string(r.Kind),
		ProjectKey: string(r.Project),
		Epoch:      r.Epoch,
		Code:       string(r.Code),
		AtMS:       r.AtMS,
	}
}

func (s *sqliteTrustStore) AppendAudit(r control.AuditRecord) (uint64, error) {
	return s.st.AppendAudit(auditRow(r))
}

func (s *sqliteTrustStore) ListAudit() ([]control.AuditRecord, error) {
	rows, err := s.st.ListAudit(0, 0)
	if err != nil {
		return nil, err
	}
	out := make([]control.AuditRecord, 0, len(rows))
	for _, r := range rows {
		out = append(out, control.AuditRecord{
			Seq:     r.Seq,
			Kind:    control.AuditKind(r.Kind),
			Project: control.ProjectKey(r.ProjectKey),
			Epoch:   r.Epoch,
			Code:    v1.ErrorCode(r.Code),
			AtMS:    r.AtMS,
		})
	}
	return out, nil
}
