package control

import (
	"errors"
	"fmt"
	"sort"
	"sync"

	v1 "github.com/amux-run/amux/api/v1"

	"github.com/amux-run/amux/internal/platform"
)

// ProjectKey is the durable project identity: hex SHA-256 of the length-
// prefixed (realpath, dev, ino) tuple (platform.ComputeProjectKey). Moving,
// replacing, or remounting a root changes the key and therefore the trust
// boundary (HA-2).
type ProjectKey string

// ProjectRecord is the control actor's view of one registered project.
type ProjectRecord struct {
	Key      ProjectKey
	Root     string // canonical realpath at registration
	Identity platform.FSIdentity
	State    TrustState
	// Epoch is the monotonic trust epoch. Every operator trust transition
	// (approve/deny/revoke) bumps it; it never decreases and values are never
	// reused (HA-4).
	Epoch uint64
	// Validation is the separately persisted replacement-validation
	// discriminator (platform.ReplacementValidator, G-lane F2). It is NOT part
	// of the frozen durable key; it is the second factor that detects a
	// replaced root when the filesystem reuses (dev, ino) — a mismatch (or an
	// absent value on a trusted record) invalidates trust, never transfers it.
	Validation platform.ValidationID
}

// GrantRecord is one hook grant. Grants are bound at approval time to the
// executable/config digests, the event set, scope, environment allowlist, and
// bounds (HA-6); any later drift makes the grant stale. Rows are never
// deleted: revocation deactivates, history is retained forever (AUD-6).
type GrantRecord struct {
	ID            string
	Project       ProjectKey
	HookID        string
	ExecPath      string
	ExecSHA256    string
	ConfigSHA256  string
	AllowedEvents []string
	Scope         ScopeKind
	FixedPath     string
	EnvAllowlist  []string
	TimeoutMS     int64
	OutputCap     int64
	BoundEpoch    uint64
	Active        bool
}

// AuditKind classifies audit records. The vocabulary matches the frozen
// securitytest projection (AUD-2) so conformance wiring maps 1:1.
type AuditKind string

const (
	AuditProjectApproved AuditKind = "project_approved"
	AuditProjectRevoked  AuditKind = "project_revoked"
	AuditGrantApproved   AuditKind = "grant_approved"
	AuditGrantInactive   AuditKind = "grant_inactive"
	AuditActivationDeny  AuditKind = "activation_denied"
	AuditSpawn           AuditKind = "spawn"
	AuditTerminate       AuditKind = "terminate"
	AuditKillEscalation  AuditKind = "kill_escalation"
	AuditExit            AuditKind = "exit"
)

// AuditRecord is one append-only audit row (AUD-1). Seq is assigned by the
// store; audit is never rewritten or erased.
type AuditRecord struct {
	Seq     uint64
	Kind    AuditKind
	Project ProjectKey
	Epoch   uint64
	Code    v1.ErrorCode
	AtMS    int64
}

// TrustTransition is one audited project trust transition, committed
// atomically by TrustStore.ApplyTransition (G-lane F1): the full next project
// record (state + monotonic epoch + replacement discriminator), optional
// deactivation of every active grant, the transition's main audit record, and
// one per-deactivated-grant audit record. Everything lands together or not at
// all — a failed transition leaves the previous state fully intact so a retry
// reuses the same epoch.
type TrustTransition struct {
	Project ProjectRecord
	// DeactivateGrants marks every active grant of the project inactive in
	// the same commit (revocation/invalidation semantics; history retained).
	DeactivateGrants bool
	// Audit is the main transition record (Seq store-assigned); nil for the
	// deliberately unaudited deny transition.
	Audit *AuditRecord
	// GrantAudit is the template appended once per deactivated grant,
	// strictly after Audit (AUD-4 ordering); nil suppresses per-grant records.
	GrantAudit *AuditRecord
}

// ErrEpochNotMonotonic reports a TrustTransition whose epoch is not strictly
// greater than the stored epoch. Epochs never decrease (ADR-0005 non-rollback
// of trust); both TrustStore implementations enforce this independently of
// the actor. The SQLite path surfaces its own typed sentinel
// (store.ErrEpochNotMonotonic) with identical semantics.
var ErrEpochNotMonotonic = errors.New("control: trust transition epoch must strictly increase")

// TrustStore is the durable write-through seam beneath the control actor.
// Trust epochs, grants, and audit live in SQLite ONLY (ADR-0005); the actor
// owns the live state and writes through synchronously — a failed durable
// write aborts the transition (fail closed, no divergence between memory and
// disk). internal/store provides the SQLite implementation; NewMemStore backs
// tests and the securitytest conformance wiring.
type TrustStore interface {
	SaveProject(p ProjectRecord) error
	// LoadProject returns the durable record for key, or found=false when the
	// key was never registered. It backs restart rehydration: persisted trust
	// re-enters the actor ONLY through a replacement-validation recheck
	// (Actor.RegisterProject), never silently.
	LoadProject(key ProjectKey) (p ProjectRecord, found bool, err error)
	// SaveValidation persists only the replacement-validation discriminator
	// for an already-registered project (state and epoch untouched).
	SaveValidation(key ProjectKey, v platform.ValidationID) error
	SaveGrant(g GrantRecord) error
	// ApplyTransition commits t as one all-or-nothing unit and returns the
	// exact IDs of the grants it deactivated — only after the commit is
	// durable. The SQLite implementation executes the whole transition in a
	// single SQL transaction; the memory implementation provides equivalent
	// atomicity under its lock. Every audited project transition (approve,
	// deny, revoke, replacement invalidation) goes through here: audit
	// failures abort the transition and are returned, never ignored.
	ApplyTransition(t TrustTransition) (inactivated []string, err error)
	AppendAudit(r AuditRecord) (uint64, error)
	ListAudit() ([]AuditRecord, error)
}

// ErrStoreClosed is returned by stores that can no longer accept writes.
var ErrStoreClosed = errors.New("control: trust store closed")

// memStore is the in-memory TrustStore for tests and conformance wiring. It
// honors the same append-only / retain-history semantics as the SQLite store.
type memStore struct {
	mu       sync.Mutex
	projects map[ProjectKey]ProjectRecord
	grants   map[string]GrantRecord
	audit    []AuditRecord
}

// NewMemStore returns an empty in-memory TrustStore.
func NewMemStore() TrustStore { return &memStore{} }

func (m *memStore) SaveProject(p ProjectRecord) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.projects == nil {
		m.projects = map[ProjectKey]ProjectRecord{}
	}
	m.projects[p.Key] = p
	return nil
}

func (m *memStore) LoadProject(key ProjectKey) (ProjectRecord, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	p, ok := m.projects[key]
	return p, ok, nil
}

func (m *memStore) SaveValidation(key ProjectKey, v platform.ValidationID) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	p, ok := m.projects[key]
	if !ok {
		return errors.New("control: SaveValidation on unregistered project")
	}
	p.Validation = v
	m.projects[key] = p
	return nil
}

func (m *memStore) SaveGrant(g GrantRecord) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.grants == nil {
		m.grants = map[string]GrantRecord{}
	}
	m.grants[g.ID] = g
	return nil
}

// ApplyTransition provides the memory path's all-or-nothing semantics: every
// precondition is validated BEFORE the first mutation, and all mutations then
// happen under one lock hold, so no observer — concurrent or subsequent — can
// see a partially applied transition (G-lane F1).
func (m *memStore) ApplyTransition(t TrustTransition) ([]string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	switch t.Project.State {
	case StateApproved, StateDenied, StateRevoked:
	default:
		return nil, fmt.Errorf("control: invalid transition state %q", t.Project.State)
	}
	cur, ok := m.projects[t.Project.Key]
	if !ok {
		return nil, fmt.Errorf("control: transition on unregistered project %s", t.Project.Key)
	}
	if t.Project.Epoch <= cur.Epoch {
		return nil, fmt.Errorf("%w: project %s epoch %d -> %d",
			ErrEpochNotMonotonic, t.Project.Key, cur.Epoch, t.Project.Epoch)
	}
	var ids []string
	if t.DeactivateGrants {
		for id, g := range m.grants {
			if g.Project == t.Project.Key && g.Active {
				ids = append(ids, id)
			}
		}
		sort.Strings(ids) // deterministic, matching the SQLite ORDER BY id
	}
	// All preconditions hold — commit everything.
	if m.projects == nil {
		m.projects = map[ProjectKey]ProjectRecord{}
	}
	m.projects[t.Project.Key] = t.Project
	for _, id := range ids {
		g := m.grants[id]
		g.Active = false
		m.grants[id] = g
	}
	appendLocked := func(r AuditRecord) {
		r.Seq = uint64(len(m.audit) + 1)
		m.audit = append(m.audit, r)
	}
	if t.Audit != nil {
		appendLocked(*t.Audit)
	}
	if t.GrantAudit != nil {
		for range ids {
			appendLocked(*t.GrantAudit)
		}
	}
	return ids, nil
}

func (m *memStore) AppendAudit(r AuditRecord) (uint64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	r.Seq = uint64(len(m.audit) + 1)
	m.audit = append(m.audit, r)
	return r.Seq, nil
}

func (m *memStore) ListAudit() ([]AuditRecord, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]AuditRecord(nil), m.audit...), nil
}
