package securitytest

import (
	"testing"

	v1 "github.com/amux-run/amux/api/v1"
)

// Decision is an authorization outcome. Every non-allow decision is
// fail-closed: zero processes created, denial audited (AUD-3).
type Decision string

const (
	DecisionAllow Decision = "allow"
	DecisionDeny  Decision = "deny"
)

// Expected is the machine-pinned outcome of a matrix row or fixture step.
// Code is set exactly when Decision is deny and must be a member of the
// frozen v1.AllErrorCodes taxonomy (hook-authorization.md §8).
type Expected struct {
	Decision Decision `json:"decision"`
	// Code is the required v1 error code for a deny decision.
	Code v1.ErrorCode `json:"code,omitempty"`
	// Enforcement names a runtime enforcement obligation attached to an
	// allow decision (e.g. "truncate_at_output_cap", "kill_at_timeout",
	// "no_ambient_env").
	Enforcement string `json:"enforcement,omitempty"`
}

// ProjectID is the SUT's opaque handle for a registered project (the durable
// key behind it is platform.ComputeProjectKey; this handle stays opaque so
// the harness cannot depend on backend representation).
type ProjectID string

// Epoch is the monotonic per-project trust epoch (HA-4). Comparable only for
// monotonicity; values are never reused.
type Epoch uint64

// GrantID identifies one hook grant record, active or inactive (history is
// retained forever, AUD-6).
type GrantID string

// ActivationID identifies one submitted hook activation.
type ActivationID string

// CwdScopeKind enumerates the grant cwd scopes (HA-9). "none" is the default:
// cwd denied, hook runs in an Amux-owned scratch directory.
type CwdScopeKind string

const (
	ScopeNone             CwdScopeKind = "none"
	ScopeFixed            CwdScopeKind = "fixed"
	ScopeWorkspacePrimary CwdScopeKind = "workspace-primary"
	ScopePane             CwdScopeKind = "pane"
)

// GrantSpec is the full HA-6 binding requested at approval time. The SUT binds
// the digests of the executable and hooks.jsonc at approval; any later drift
// makes the grant stale (HA-7).
type GrantSpec struct {
	HookID         string
	ExecutablePath string
	AllowedEvents  []string
	ScopeKind      CwdScopeKind
	FixedPath      string // ScopeFixed only
	EnvAllowlist   []string
	TimeoutMS      int64
	OutputCapBytes int64
}

// ActivationSpec submits one hook activation.
type ActivationSpec struct {
	Project ProjectID
	Hook    string
	Event   string
	// Session tags the submitting session; cross-session fixtures use
	// distinct tags (revocation must act across all of them, HA-18).
	Session string
	// PaneProject is the project identity the target pane resolves to, for
	// ScopePane grants ("" = unregistered pane).
	PaneProject ProjectID
}

// ActivationResult is the SUT's decision for one activation.
type ActivationResult struct {
	Decision Decision
	Code     v1.ErrorCode
	// CompletedAtMS is the fake-clock time (Clock.NowUnixMilli) at which the
	// decision was produced; the 250 ms gates are asserted against it.
	CompletedAtMS int64
}

// Child is one entry in the SUT's process-creation ledger. The conformance
// wiring must record every process creation the implementation performs (a
// spy behind platform.DescriptorLaunch / platform.PTY), so "zero children"
// assertions observe reality, not intent.
type Child struct {
	Activation ActivationID
	// ExecSHA256 is the hex digest of the bytes actually executed (the opened
	// descriptor's content at exec time) — the races.* fixtures assert this
	// equals the approved digest whenever a child exists (HA-11).
	ExecSHA256 string
	// ConfigSHA256 is the hex digest of the config bytes actually validated.
	ConfigSHA256 string
	SpawnedAtMS  int64
	// TerminatedAtMS / KilledAtMS / ExitedAtMS are 0 until the event occurs.
	TerminatedAtMS int64
	KilledAtMS     int64
	ExitedAtMS     int64
}

// AuditKind classifies audit records for fixture assertions (AUD-2 names the
// full set; fixtures depend only on these).
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

// AuditRecord is the fixture-visible projection of one audit row (AUD-1).
type AuditRecord struct {
	Seq     uint64
	Kind    AuditKind
	Project ProjectID
	Epoch   Epoch
	Code    v1.ErrorCode // set for activation_denied
	AtMS    int64
}

// SyncPoint is a deterministic barrier location inside the SUT's launch
// pipeline. Conformance wirings expose these three points; fixtures use them
// to force both linearization orderings and the check-to-exec race window.
type SyncPoint string

const (
	// SyncBeforeFinalValidation parks an activation immediately before the
	// HA-14 final pre-spawn authorization point.
	SyncBeforeFinalValidation SyncPoint = "before-final-validation"
	// SyncAfterObjectOpen parks after OpenBound + digest capture but before
	// final validation/exec — the window the races.* fixtures attack.
	SyncAfterObjectOpen SyncPoint = "after-object-open"
	// SyncAfterSpawn parks after the child exists but before the activation
	// is reported complete, so launch-first orderings are deterministic.
	SyncAfterSpawn SyncPoint = "after-spawn"
)

// GenerationFixture describes the CLAIMS a crafted old snapshot generation
// makes; the conformance wiring materializes real snapshot bytes making these
// claims and presents them to restore. Restore must reject every claim
// (HA-18..HA-21): epochs never decrease, grants never reactivate, audit is
// never erased, snapshots never confer launch authority.
type GenerationFixture struct {
	ClaimedEpochs         map[ProjectID]Epoch
	ClaimedActiveGrants   []GrantID
	OmitsAuditHistory     bool
	ClaimsLaunchAuthority bool
}

// FakeClock drives the SUT's injected platform.Clock deterministically.
type FakeClock interface {
	NowUnixMilli() int64
	Advance(ms int64)
}

// TrustOps are the operator-facing trust transitions, serialized by the SUT's
// control actor (HA-4b). session tags the acting session for cross-session
// fixtures.
type TrustOps interface {
	// RegisterProject registers root (a harness-created directory) as an
	// explicit project root and returns its handle. Registration alone
	// confers nothing (HA-3b).
	RegisterProject(root string) (ProjectID, error)
	ApproveProject(session string, p ProjectID) (Epoch, error)
	DenyProject(session string, p ProjectID) error
	// RevokeProject returns after revocation has linearized: the epoch is
	// bumped and queued same-project work is being canceled (HA-18).
	RevokeProject(session string, p ProjectID) (Epoch, error)
	Epoch(p ProjectID) (Epoch, error)
}

// HookOps grant and activate hooks.
type HookOps interface {
	ApproveHook(session string, p ProjectID, g GrantSpec) (GrantID, error)
	// Activate submits an activation asynchronously and returns its ID;
	// Await blocks (bounded by a real-time watchdog in the harness) for the
	// decision.
	Activate(spec ActivationSpec) (ActivationID, error)
	Await(a ActivationID) (ActivationResult, error)
}

// Barriers controls the SyncPoints.
type Barriers interface {
	// Hold makes every activation park at point until Release.
	Hold(point SyncPoint)
	// AwaitParked blocks until activation a is parked at point.
	AwaitParked(point SyncPoint, a ActivationID) error
	Release(point SyncPoint)
}

// Observations expose the ledgers fixtures assert on.
type Observations interface {
	Children() []Child
	Audit() []AuditRecord
}

// Redactor is the single centralized redaction engine (RED-1) applied to the
// named egress context.
type Redactor interface {
	Redact(context string, payload []byte) ([]byte, error)
}

// RestoreOps present crafted snapshot generations to the restore path.
type RestoreOps interface {
	ImportGeneration(gen GenerationFixture) error
}

// SystemUnderTest is the complete implementation-neutral surface the T4
// backend wires up (real trust engine + spy launcher + fake clock) and hands
// to RunConformance. Close tears the instance down; every fixture gets a
// fresh instance.
type SystemUnderTest interface {
	Clock() FakeClock
	Trust() TrustOps
	Hooks() HookOps
	Barriers() Barriers
	Observe() Observations
	Redactor() Redactor
	Restore() RestoreOps
	Close() error
}

// Factory builds a fresh SystemUnderTest per fixture. T4 backend calls
// RunConformance from its own test package with a real factory; this
// package's own TestSecurityConformance passes nil and therefore skips with
// the prerequisite message (never a silent pass).
type Factory func(t *testing.T) SystemUnderTest
