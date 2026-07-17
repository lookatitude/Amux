package control

import (
	"context"
	"errors"
	"fmt"
	"sort"

	"github.com/google/uuid"

	v1 "github.com/amux-run/amux/api/v1"

	"github.com/amux-run/amux/internal/platform"
)

// DefaultMailbox bounds the actor's message queue. A bounded mailbox plus
// context cancellation means a stalled actor produces backpressure and typed
// failures, never unbounded buffering (PRD F4 local-DoS posture).
const DefaultMailbox = 256

// ErrStopped is returned for calls made after Stop.
var ErrStopped = errors.New("control: actor stopped")

// ErrMailboxFull is returned when the bounded mailbox rejects a message
// because the caller's context expired while the queue was at capacity.
var ErrMailboxFull = errors.New("control: mailbox full")

// SessionInfo is one session-registry entry.
type SessionInfo struct {
	ID        string
	Name      string
	CreatedMS int64
}

// RevokeListener observes a committed revocation. Listeners run on the actor
// goroutine strictly AFTER the transition and its audit records commit; they
// must hand off quickly and MUST NOT call back into the actor synchronously
// (ADR-0001 no-nested-wait).
type RevokeListener func(project ProjectKey, newEpoch uint64)

// Deps wires the actor's seams.
type Deps struct {
	FS    platform.FilesystemIdentity
	Clock platform.Clock
	Store TrustStore
	// Validator resolves the replacement-validation discriminator (G-lane
	// F2): the second identity factor that detects a replaced root when the
	// filesystem reuses (dev, ino). Defaults to the production resolver.
	Validator platform.ReplacementValidator
}

// Actor is the daemon-global control actor. All state below is owned
// exclusively by the run goroutine.
type Actor struct {
	deps    Deps
	mailbox chan func()
	stop    chan struct{}
	done    chan struct{}

	// Owned by run():
	sessions  map[string]SessionInfo
	projects  map[ProjectKey]*ProjectRecord
	grants    map[string]*GrantRecord
	listeners []RevokeListener
}

// New creates a control actor. Store is required; FS and Clock default to the
// production implementations.
func New(deps Deps) *Actor {
	if deps.Store == nil {
		panic("control.New: Deps.Store is required (trust state is SQLite-only, ADR-0005)")
	}
	if deps.FS == nil {
		deps.FS = platform.NewFilesystemIdentity()
	}
	if deps.Clock == nil {
		deps.Clock = platform.NewSystemClock()
	}
	if deps.Validator == nil {
		deps.Validator = platform.NewReplacementValidator()
	}
	return &Actor{
		deps:     deps,
		mailbox:  make(chan func(), DefaultMailbox),
		stop:     make(chan struct{}),
		done:     make(chan struct{}),
		sessions: map[string]SessionInfo{},
		projects: map[ProjectKey]*ProjectRecord{},
		grants:   map[string]*GrantRecord{},
	}
}

// Start launches the actor goroutine.
func (a *Actor) Start() {
	go a.run()
}

// Stop halts the actor. Idempotent. Pending mailbox messages are drained with
// ErrStopped via their reply channels closing behavior (each do() call
// observes the stop).
func (a *Actor) Stop() {
	select {
	case <-a.stop:
	default:
		close(a.stop)
	}
	<-a.done
}

func (a *Actor) run() {
	defer close(a.done)
	for {
		select {
		case fn := <-a.mailbox:
			fn()
		case <-a.stop:
			return
		}
	}
}

// do serializes fn onto the actor goroutine and waits for completion.
func (a *Actor) do(ctx context.Context, fn func()) error {
	donec := make(chan struct{})
	wrapped := func() { fn(); close(donec) }
	select {
	case a.mailbox <- wrapped:
	case <-ctx.Done():
		return fmt.Errorf("%w: %w", ErrMailboxFull, ctx.Err())
	case <-a.stop:
		return ErrStopped
	}
	select {
	case <-donec:
		return nil
	case <-a.stop:
		return ErrStopped
	}
}

// --- session registry -------------------------------------------------------

// RegisterSession records a session in the daemon-global registry.
func (a *Actor) RegisterSession(ctx context.Context, info SessionInfo) error {
	var err error
	derr := a.do(ctx, func() {
		if info.ID == "" {
			err = errors.New("control: empty session id")
			return
		}
		if _, dup := a.sessions[info.ID]; dup {
			err = fmt.Errorf("control: session %s already registered", info.ID)
			return
		}
		if info.CreatedMS == 0 {
			info.CreatedMS = a.deps.Clock.NowUnixMilli()
		}
		a.sessions[info.ID] = info
	})
	if derr != nil {
		return derr
	}
	return err
}

// UnregisterSession removes a session from the registry. Unknown IDs are a
// no-op (idempotent teardown).
func (a *Actor) UnregisterSession(ctx context.Context, id string) error {
	return a.do(ctx, func() { delete(a.sessions, id) })
}

// ListSessions returns registry entries sorted by creation then ID.
func (a *Actor) ListSessions(ctx context.Context) ([]SessionInfo, error) {
	var out []SessionInfo
	if err := a.do(ctx, func() {
		for _, s := range a.sessions {
			out = append(out, s)
		}
	}); err != nil {
		return nil, err
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].CreatedMS != out[j].CreatedMS {
			return out[i].CreatedMS < out[j].CreatedMS
		}
		return out[i].ID < out[j].ID
	})
	return out, nil
}

// --- project trust ----------------------------------------------------------

// RegisterProject registers root as an explicit project root and returns its
// durable key. Registration is idempotent for the same identity and confers
// NOTHING by itself (HA-3b). It is also the replacement-validation point
// (G-lane F2): every call recomputes the discriminator on the caller's
// goroutine and, when the object at the path no longer matches the persisted
// discriminator — the (dev, ino)-reuse replacement the frozen key cannot see —
// any approved trust is invalidated before the key is returned. Durable
// records re-enter the actor here after a restart, through the same recheck,
// so persistence revalidation is not merely an in-memory comparison.
func (a *Actor) RegisterProject(ctx context.Context, root string) (ProjectKey, error) {
	// Filesystem resolution happens on the caller's goroutine: the actor
	// goroutine never does filesystem I/O (ADR-0001 discipline).
	pk, realpath, id, err := a.identify(root)
	if err != nil {
		return "", fmt.Errorf("control: project identity: %w", err)
	}
	val, verr := a.deps.Validator.ValidationID(realpath)
	if verr != nil {
		// No reliable discriminator ⇒ ambiguous identity ⇒ fail closed with a
		// typed, audited denial (AUD-3). Trust must never ride on a guess.
		_ = a.do(ctx, func() { a.appendAudit(AuditActivationDeny, pk, 0, v1.ErrProjectTrustRequired) })
		return "", &typedError{code: v1.ErrProjectTrustRequired,
			msg: "project identity cannot be validated on this filesystem: " + verr.Error()}
	}
	var out ProjectKey
	var terr error
	if derr := a.do(ctx, func() {
		rec, ok := a.projects[pk]
		if !ok {
			// Restart rehydration: durable trust re-enters memory only through
			// this revalidation path.
			durable, found, lerr := a.deps.Store.LoadProject(pk)
			if lerr != nil {
				terr = lerr
				return
			}
			if found {
				cp := durable
				rec = &cp
				a.projects[pk] = rec
			}
		}
		if rec != nil {
			if rec.Validation != val {
				// Replaced object under a reused identity tuple, or an
				// ambiguous (absent) persisted discriminator: never reuse.
				if terr = a.invalidateIdentityLocked(rec, val); terr != nil {
					return
				}
			}
			out = rec.Key
			return
		}
		nrec := &ProjectRecord{Key: pk, Root: realpath, Identity: id, State: StateNone, Epoch: 0, Validation: val}
		if terr = a.deps.Store.SaveProject(*nrec); terr != nil {
			return // fail closed: no in-memory registration without durability
		}
		a.projects[pk] = nrec
		out = pk
	}); derr != nil {
		return "", derr
	}
	return out, terr
}

// invalidateIdentityLocked runs ON the actor goroutine. The object at the
// project's path failed replacement validation: for an approved project this
// is a full system revocation — monotonic epoch bump, grant deactivation, an
// AuditProjectRevoked record carrying the project_trust_required code
// (distinguishing it from an operator revoke), and one grant_inactive record
// per deactivated grant, all committed durably as ONE ApplyTransition unit
// (G-lane F1). In-memory state moves and revoke listeners fire only AFTER
// that commit succeeds; on failure everything stays at the old approved
// epoch, so a retry re-derives the same transition. For any untrusted state
// only the persisted discriminator is refreshed to describe the object now
// at the path.
func (a *Actor) invalidateIdentityLocked(rec *ProjectRecord, val platform.ValidationID) error {
	if rec.State != StateApproved {
		if err := a.deps.Store.SaveValidation(rec.Key, val); err != nil {
			return err
		}
		rec.Validation = val
		return nil
	}
	next := *rec
	next.State = StateRevoked
	next.Epoch = rec.Epoch + 1 // monotonic; never reused (HA-4)
	next.Validation = val
	now := a.deps.Clock.NowUnixMilli()
	inactivated, err := a.deps.Store.ApplyTransition(TrustTransition{
		Project:          next,
		DeactivateGrants: true,
		Audit: &AuditRecord{Kind: AuditProjectRevoked, Project: next.Key,
			Epoch: next.Epoch, Code: v1.ErrProjectTrustRequired, AtMS: now},
		GrantAudit: &AuditRecord{Kind: AuditGrantInactive, Project: next.Key,
			Epoch: next.Epoch, AtMS: now},
	})
	if err != nil {
		return err // fail closed: no in-memory transition without durability
	}
	for _, id := range inactivated {
		if g, ok := a.grants[id]; ok {
			g.Active = false
		}
	}
	*rec = next
	for _, l := range a.listeners {
		l(rec.Key, rec.Epoch)
	}
	return nil
}

func (a *Actor) identify(root string) (ProjectKey, string, platform.FSIdentity, error) {
	realpath, id, err := a.deps.FS.Identify(root)
	if err != nil {
		return "", "", platform.FSIdentity{}, err
	}
	pk, _, err := platform.ComputeProjectKey(a.deps.FS, realpath)
	if err != nil {
		return "", "", platform.FSIdentity{}, err
	}
	return ProjectKey(pk), realpath, id, nil
}

// transitionProject applies one operator trust transition on the actor
// goroutine. The epoch bump, state change, grant deactivation, and every
// audit record commit durably as ONE ApplyTransition unit (G-lane F1);
// in-memory state moves and revoke listeners fire only AFTER that commit, so
// no observer — including the durable store across a restart — can see a
// partial transition, and a failed attempt leaves the old epoch reusable.
func (a *Actor) transitionProject(ctx context.Context, key ProjectKey, to TrustState, kind AuditKind, audited bool) (uint64, error) {
	var epoch uint64
	var terr error
	if derr := a.do(ctx, func() {
		rec, ok := a.projects[key]
		if !ok {
			terr = fmt.Errorf("control: project %s not registered", key)
			return
		}
		next := *rec
		next.State = to
		next.Epoch = rec.Epoch + 1 // monotonic; never reused (HA-4)
		now := a.deps.Clock.NowUnixMilli()
		tr := TrustTransition{Project: next}
		if audited {
			tr.Audit = &AuditRecord{Kind: kind, Project: key, Epoch: next.Epoch, AtMS: now}
		}
		if to == StateRevoked {
			// Revocation deactivates every grant of the project; history is
			// retained (HA-18e / AUD-6) and each deactivation is audited.
			tr.DeactivateGrants = true
			tr.GrantAudit = &AuditRecord{Kind: AuditGrantInactive, Project: key, Epoch: next.Epoch, AtMS: now}
		}
		inactivated, err := a.deps.Store.ApplyTransition(tr)
		if err != nil {
			terr = err
			return // fail closed: no in-memory transition without durability
		}
		for _, id := range inactivated {
			if g, ok := a.grants[id]; ok {
				g.Active = false
			}
		}
		*rec = next
		epoch = rec.Epoch
		if to == StateRevoked {
			// Post-commit, still on the actor goroutine: in-flight work sees
			// the bump before any later message can be processed (HA-18).
			for _, l := range a.listeners {
				l(key, epoch)
			}
		}
	}); derr != nil {
		return 0, derr
	}
	return epoch, terr
}

// ApproveProject grants project trust, bumping the epoch.
func (a *Actor) ApproveProject(ctx context.Context, session string, key ProjectKey) (uint64, error) {
	return a.transitionProject(ctx, key, StateApproved, AuditProjectApproved, true)
}

// DenyProject records an explicit operator denial.
func (a *Actor) DenyProject(ctx context.Context, session string, key ProjectKey) error {
	_, err := a.transitionProject(ctx, key, StateDenied, "", false)
	return err
}

// RevokeProject revokes trust: the epoch is bumped, every grant of the
// project is deactivated, the revocation is audited, and revoke listeners are
// notified — all before this returns (the revocation has linearized, HA-18).
func (a *Actor) RevokeProject(ctx context.Context, session string, key ProjectKey) (uint64, error) {
	return a.transitionProject(ctx, key, StateRevoked, AuditProjectRevoked, true)
}

// Epoch returns the project's current trust epoch.
func (a *Actor) Epoch(ctx context.Context, key ProjectKey) (uint64, error) {
	var epoch uint64
	var terr error
	if derr := a.do(ctx, func() {
		rec, ok := a.projects[key]
		if !ok {
			terr = fmt.Errorf("control: project %s not registered", key)
			return
		}
		epoch = rec.Epoch
	}); derr != nil {
		return 0, derr
	}
	return epoch, terr
}

// Project returns a copy of the project record.
func (a *Actor) Project(ctx context.Context, key ProjectKey) (ProjectRecord, bool, error) {
	var rec ProjectRecord
	var found bool
	if err := a.do(ctx, func() {
		if r, ok := a.projects[key]; ok {
			rec, found = *r, true
		}
	}); err != nil {
		return ProjectRecord{}, false, err
	}
	return rec, found, nil
}

// OnRevoke registers a revocation listener.
func (a *Actor) OnRevoke(ctx context.Context, l RevokeListener) error {
	return a.do(ctx, func() { a.listeners = append(a.listeners, l) })
}

// --- grants -------------------------------------------------------------

// GrantInput is the caller-resolved binding for ApproveGrant. Digests are of
// the bytes the approving flow actually validated (HA-6).
type GrantInput struct {
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
}

// ApproveGrant records a hook grant bound to the project's CURRENT epoch.
// The project must be approved; bounds outside the frozen range are rejected
// here as well (never silently clamped, HA-8).
func (a *Actor) ApproveGrant(ctx context.Context, session string, key ProjectKey, in GrantInput) (string, error) {
	if in.TimeoutMS < 0 || in.TimeoutMS > MaxTimeoutMS {
		return "", &typedError{code: v1.ErrInvalidArgument, msg: "grant timeout outside the grantable range"}
	}
	if in.OutputCap < 0 || in.OutputCap > MaxOutputCapBytes {
		return "", &typedError{code: v1.ErrInvalidArgument, msg: "grant output cap outside the grantable range"}
	}
	id := newID()
	var terr error
	if derr := a.do(ctx, func() {
		rec, ok := a.projects[key]
		if !ok || rec.State != StateApproved {
			terr = &typedError{code: v1.ErrProjectTrustRequired, msg: "grant requires an approved project"}
			return
		}
		g := &GrantRecord{
			ID:            id,
			Project:       key,
			HookID:        in.HookID,
			ExecPath:      in.ExecPath,
			ExecSHA256:    in.ExecSHA256,
			ConfigSHA256:  in.ConfigSHA256,
			AllowedEvents: append([]string(nil), in.AllowedEvents...),
			Scope:         in.Scope,
			FixedPath:     in.FixedPath,
			EnvAllowlist:  append([]string(nil), in.EnvAllowlist...),
			TimeoutMS:     in.TimeoutMS,
			OutputCap:     in.OutputCap,
			BoundEpoch:    rec.Epoch,
			Active:        true,
		}
		if terr = a.deps.Store.SaveGrant(*g); terr != nil {
			return
		}
		a.grants[id] = g
		a.appendAudit(AuditGrantApproved, key, rec.Epoch, "")
	}); derr != nil {
		return "", derr
	}
	if terr != nil {
		return "", terr
	}
	return id, nil
}

// Grant returns a copy of a grant record.
func (a *Actor) Grant(ctx context.Context, id string) (GrantRecord, bool, error) {
	var g GrantRecord
	var found bool
	if err := a.do(ctx, func() {
		if r, ok := a.grants[id]; ok {
			g, found = *r, true
		}
	}); err != nil {
		return GrantRecord{}, false, err
	}
	return g, found, nil
}

// FindGrant returns the most recently approved grant for (project, hook),
// active or not — the caller distinguishes absent from inactive (they carry
// different deny codes: hook_grant_required either way, but audit differs).
func (a *Actor) FindGrant(ctx context.Context, key ProjectKey, hookID string) (GrantRecord, bool, error) {
	var out GrantRecord
	var found bool
	if err := a.do(ctx, func() {
		for _, g := range a.grants {
			if g.Project != key || g.HookID != hookID {
				continue
			}
			if !found || g.ID > out.ID { // UUIDv7: lexically newest = latest
				out, found = *g, true
			}
		}
	}); err != nil {
		return GrantRecord{}, false, err
	}
	return out, found, nil
}

// --- launch authorization -----------------------------------------------

// RuntimeFacts are the caller-resolved (I/O-derived) halves of the final
// authorization: descriptor digests, filesystem identity recheck, scope
// resolution, bounds, environment, queue admission. The hooks runtime
// resolves them immediately before calling AuthorizeLaunch (HA-14).
type RuntimeFacts struct {
	RootIdentityMatch bool
	ExecDigestMatch   bool
	ConfigDigestMatch bool
	ConfigMatch       ConfigMatch
	TimeoutMS         int64
	OutputCapBytes    int64
	ExecIsRegularFile bool
	EventAllowed      bool
	Scope             ScopeFacts
	EnvOutside        bool
	QueueExhausted    bool
	Invariant         bool
}

// ConfigMatch reports whether the CURRENT hooks.jsonc still defines the hook
// exactly as granted (HA-7).
type ConfigMatch struct {
	EventSet  bool
	Scope     bool
	Env       bool
	Timeout   bool
	OutputCap bool
}

// AuthorizeResult is the linearized authorization outcome.
type AuthorizeResult struct {
	Decision Decision
	// Epoch is the project's trust epoch at the linearization point.
	Epoch uint64
	// AtMS is the actor clock reading when the decision committed.
	AtMS int64
}

// AuthorizeLaunch is the final pre-spawn authorization point (HA-14). It runs
// on the actor goroutine — the single linearization point — so a launch
// authorized here can never have been ordered after a completed revocation
// (ADR-0004). Denials are audited before the result is returned (AUD-3).
func (a *Actor) AuthorizeLaunch(ctx context.Context, key ProjectKey, hookID string, grantID string, rt RuntimeFacts) (AuthorizeResult, error) {
	var res AuthorizeResult
	if err := a.do(ctx, func() {
		facts := ActivationFacts{
			QueueExhausted:       rt.QueueExhausted,
			RootIdentityMatch:    rt.RootIdentityMatch,
			ExecDigestMatch:      rt.ExecDigestMatch,
			ConfigDigestMatch:    rt.ConfigDigestMatch,
			ConfigEventSetMatch:  rt.ConfigMatch.EventSet,
			ConfigScopeMatch:     rt.ConfigMatch.Scope,
			ConfigEnvMatch:       rt.ConfigMatch.Env,
			ConfigTimeoutMatch:   rt.ConfigMatch.Timeout,
			ConfigOutputCapMatch: rt.ConfigMatch.OutputCap,
			TimeoutMS:            rt.TimeoutMS,
			OutputCapBytes:       rt.OutputCapBytes,
			ExecIsRegularFile:    rt.ExecIsRegularFile,
			EventAllowed:         rt.EventAllowed,
			Scope:                rt.Scope,
			EnvOutsideAllowlist:  rt.EnvOutside,
			InvariantViolated:    rt.Invariant,
		}
		if rec, ok := a.projects[key]; ok {
			facts.ProjectRegistered = true
			facts.TrustState = rec.State
			facts.CurrentEpoch = rec.Epoch
			res.Epoch = rec.Epoch
		}
		if g, ok := a.grants[grantID]; ok && g.Project == key && g.HookID == hookID {
			facts.GrantFound = true
			facts.GrantActive = g.Active
			facts.GrantBoundEpoch = g.BoundEpoch
		}
		res.Decision = Decide(facts)
		res.AtMS = a.deps.Clock.NowUnixMilli()
		if !res.Decision.Allow {
			a.appendAudit(AuditActivationDeny, key, facts.CurrentEpoch, res.Decision.Code)
		}
	}); err != nil {
		return AuthorizeResult{}, err
	}
	return res, nil
}

// AppendAudit lets the hooks runtime record spawn/terminate/kill/exit events
// through the same append-only trail (AUD-1). Serialized on the actor.
func (a *Actor) AppendAudit(ctx context.Context, kind AuditKind, key ProjectKey, epoch uint64, code v1.ErrorCode) error {
	return a.do(ctx, func() { a.appendAudit(kind, key, epoch, code) })
}

// RecordAudit appends a hook-lifecycle audit record DIRECTLY to the durable
// store, bypassing the actor mailbox. The store is thread-safe and the record
// sequence is monotonic, so this is safe from any goroutine. It exists for two
// callers that cannot round-trip through the actor: a revoke listener (which
// already runs ON the actor goroutine, so a do() call would self-deadlock) and
// the deterministic kill-escalation timer (which fires on a clock goroutine
// after the revoke transition has fully committed). Both append strictly after
// the project_revoked record, preserving the AUD-4 ordering. Like appendAudit,
// its scope is records with NO accompanying trust transition, so best-effort
// is acceptable here by design (G-lane F1): the revoke transition these
// records trail was already committed atomically — with its own audit —
// through TrustStore.ApplyTransition, which returns audit failures instead of
// ignoring them.
func (a *Actor) RecordAudit(kind AuditKind, key ProjectKey, epoch uint64, code v1.ErrorCode) {
	_, _ = a.deps.Store.AppendAudit(AuditRecord{
		Kind:    kind,
		Project: key,
		Epoch:   epoch,
		Code:    code,
		AtMS:    a.deps.Clock.NowUnixMilli(),
	})
}

// Audit returns the append-only audit trail.
func (a *Actor) Audit(ctx context.Context) ([]AuditRecord, error) {
	var out []AuditRecord
	var terr error
	if err := a.do(ctx, func() { out, terr = a.deps.Store.ListAudit() }); err != nil {
		return nil, err
	}
	return out, terr
}

// appendAudit runs on the actor goroutine. Its scope is deliberately narrow
// (G-lane F1): only records with no accompanying state transition — denial
// records (activation_denied, registration denials) and grant_approved — pass
// through here best-effort, because a failed audit write on a pure-record
// path must not wedge the actor. Every audited PROJECT transition (approve,
// deny, revoke, replacement invalidation) instead commits its audit records
// inside TrustStore.ApplyTransition, where an audit failure aborts the whole
// transition and is returned, never ignored.
func (a *Actor) appendAudit(kind AuditKind, key ProjectKey, epoch uint64, code v1.ErrorCode) {
	_, _ = a.deps.Store.AppendAudit(AuditRecord{
		Kind:    kind,
		Project: key,
		Epoch:   epoch,
		Code:    code,
		AtMS:    a.deps.Clock.NowUnixMilli(),
	})
}

// typedError carries a frozen-taxonomy code (branch on Code, not strings).
type typedError struct {
	code v1.ErrorCode
	msg  string
}

func (e *typedError) Error() string { return string(e.code) + ": " + e.msg }

// CodeOf returns the taxonomy code of a control error, or "" for plain errors.
func CodeOf(err error) v1.ErrorCode {
	var te *typedError
	if errors.As(err, &te) {
		return te.code
	}
	return ""
}

func newID() string { return NewID() }

// NewID mints an opaque, sortable UUIDv7 identifier (ADR-0002). It is exported
// so the daemon can mint session ids with the same discipline as grants.
func NewID() string {
	u, err := uuid.NewV7()
	if err != nil {
		u = uuid.New()
	}
	return u.String()
}
