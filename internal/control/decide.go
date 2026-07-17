package control

import (
	v1 "github.com/amux-run/amux/api/v1"
)

// Decision is the authorization outcome of the final pre-spawn check (HA-14).
// Every non-allow outcome is fail-closed: the caller creates zero processes
// and audits the denial.
type Decision struct {
	Allow bool
	// Code is the frozen-taxonomy error code of a deny decision ("" on allow).
	Code v1.ErrorCode
	// Enforcements names the runtime obligations attached to an allow decision
	// (hook-authorization contract): the launcher must honor every entry.
	Enforcements []string
	// Reason is a human diagnostic, never an automation contract.
	Reason string
}

// Enforcement labels attached to allow decisions. They mirror the trust-matrix
// vocabulary (testdata/security/trust-matrix.json).
const (
	EnforceKillAtTimeout      = "kill_at_timeout"
	EnforceTruncateAtCap      = "truncate_at_output_cap"
	EnforceNoAmbientEnv       = "no_ambient_env"
	EnforceScratchCwd         = "scratch_cwd"
	EnforceDescriptorBoundRun = "descriptor_bound_exec"
)

// Bounds are the frozen hook execution bounds (hook-authorization.md §7,
// mirrored by securitytest.Gates; changing them is a spec-confirmation gate).
const (
	DefaultTimeoutMS  int64 = 2000
	MaxTimeoutMS      int64 = 30000
	MaxOutputCapBytes int64 = 1 << 20
)

// TrustState is a project's operator-facing trust state. Only StateApproved
// confers anything; registration alone confers nothing (HA-3b).
type TrustState string

const (
	StateNone     TrustState = ""         // registered, no decision yet
	StateApproved TrustState = "approved" //
	StateDenied   TrustState = "denied"
	StateRevoked  TrustState = "revoked"
)

// ScopeKind enumerates hook cwd scopes (HA-9). ScopeNone is the default: cwd
// denied, hook runs in an Amux-owned scratch directory.
type ScopeKind string

const (
	ScopeNone             ScopeKind = "none"
	ScopeFixed            ScopeKind = "fixed"
	ScopeWorkspacePrimary ScopeKind = "workspace-primary"
	ScopePane             ScopeKind = "pane"
)

// ScopeFacts is the resolved cwd-scope evidence for one activation. The hooks
// runtime resolves paths/identities BEFORE launch and reports outcomes here;
// Decide never touches the filesystem.
type ScopeFacts struct {
	Kind ScopeKind
	// Resolved reports that the scope target exists, is unambiguous, and its
	// current filesystem identity equals the granted identity. Always true for
	// ScopeNone (the scratch directory is daemon-owned).
	Resolved bool
	// Reason carries the resolution failure diagnostic when !Resolved.
	Reason string
}

// ActivationFacts is the complete, already-resolved evidence the final
// pre-spawn authorization consumes. Every field is a fact, not a promise: the
// digests are of the OPENED descriptors (descriptor-bound launch, ADR-0006),
// identity checks were recomputed immediately before this call, and the queue
// state is the bounded activation queue's admission answer.
type ActivationFacts struct {
	// Load: the bounded activation queue rejected admission (STR-8).
	QueueExhausted bool

	// Project trust (HA-2, HA-3).
	ProjectRegistered bool
	TrustState        TrustState
	// RootIdentityMatch: the project root's current canonical identity
	// (realpath, dev, ino) equals the registered identity. A replaced or
	// remounted root fails this (HA-2c).
	RootIdentityMatch bool
	CurrentEpoch      uint64

	// Grant existence/binding (HA-5..HA-7).
	GrantFound      bool
	GrantActive     bool
	GrantBoundEpoch uint64
	// Digest facts compare the bytes actually opened for execution/validation
	// against the digests bound at approval (HA-11).
	ExecDigestMatch   bool
	ConfigDigestMatch bool
	// Config redefinition facts: the current hooks.jsonc redefines the named
	// field vs the grant binding (HA-7). Any false is a stale grant.
	ConfigEventSetMatch  bool
	ConfigScopeMatch     bool
	ConfigEnvMatch       bool
	ConfigTimeoutMatch   bool
	ConfigOutputCapMatch bool

	// Bounds (HA-8, HA-12).
	TimeoutMS         int64 // effective timeout (0 = use default)
	OutputCapBytes    int64 // effective cap (0 = use default)
	ExecIsRegularFile bool

	// Scope + event admission (HA-9).
	EventAllowed bool
	Scope        ScopeFacts

	// Environment (HA-8, RED-3): the activation requires a key outside the
	// grant's allowlist.
	EnvOutsideAllowlist bool

	// InvariantViolated: the trust engine detected an internal inconsistency
	// while evaluating. Fail closed, never allow (hook-authorization.md §8).
	InvariantViolated bool
}

// Decide is the pure final-authorization function. Deny precedence is frozen
// by the trust matrix: internal invariant, load, project trust, grant
// existence, grant staleness, bounds validity, scope/event/environment
// containment. On allow it attaches every runtime enforcement obligation.
func Decide(f ActivationFacts) Decision {
	deny := func(code v1.ErrorCode, reason string) Decision {
		return Decision{Allow: false, Code: code, Reason: reason}
	}

	if f.InvariantViolated {
		return deny(v1.ErrInternal, "trust-engine invariant violated during evaluation")
	}
	if f.QueueExhausted {
		return deny(v1.ErrResourceExhausted, "activation queue at bound")
	}

	// Project family: trust gates everything (HA-3). Identity mismatch means
	// the trusted object no longer exists — never transferable to whatever now
	// occupies the path (HA-2c).
	if !f.ProjectRegistered || f.TrustState != StateApproved || !f.RootIdentityMatch {
		return deny(v1.ErrProjectTrustRequired, "project trust absent, denied, revoked, or identity changed")
	}

	// Grant family: existence/activity first (HA-5, HA-18e) ...
	if !f.GrantFound || !f.GrantActive {
		return deny(v1.ErrHookGrantRequired, "no active grant for this hook")
	}
	// ... then binding freshness (HA-4a, HA-7, HA-11).
	if f.GrantBoundEpoch != f.CurrentEpoch ||
		!f.ExecDigestMatch || !f.ConfigDigestMatch ||
		!f.ConfigEventSetMatch || !f.ConfigScopeMatch || !f.ConfigEnvMatch ||
		!f.ConfigTimeoutMatch || !f.ConfigOutputCapMatch {
		return deny(v1.ErrHookGrantStale, "grant binding is stale (epoch, digest, or config drift)")
	}

	// Bounds validity (HA-8, HA-12): out-of-range requests are invalid, never
	// silently clamped.
	timeout := f.TimeoutMS
	if timeout == 0 {
		timeout = DefaultTimeoutMS
	}
	if timeout < 0 || timeout > MaxTimeoutMS {
		return deny(v1.ErrInvalidArgument, "timeout outside the grantable range")
	}
	cap := f.OutputCapBytes
	if cap == 0 {
		cap = MaxOutputCapBytes
	}
	if cap < 0 || cap > MaxOutputCapBytes {
		return deny(v1.ErrInvalidArgument, "output cap outside the grantable range")
	}
	if !f.ExecIsRegularFile {
		return deny(v1.ErrInvalidArgument, "granted executable is not a regular file")
	}

	// Scope/event/environment containment (HA-9, HA-8).
	if !f.EventAllowed {
		return deny(v1.ErrScopeDenied, "activation event outside the granted event set")
	}
	if !f.Scope.Resolved {
		return deny(v1.ErrScopeDenied, "cwd scope did not resolve: "+f.Scope.Reason)
	}
	if f.EnvOutsideAllowlist {
		return deny(v1.ErrScopeDenied, "environment key outside the grant allowlist")
	}

	enf := []string{EnforceKillAtTimeout, EnforceTruncateAtCap, EnforceNoAmbientEnv, EnforceDescriptorBoundRun}
	if f.Scope.Kind == ScopeNone {
		enf = append(enf, EnforceScratchCwd)
	}
	return Decision{Allow: true, Enforcements: enf}
}
