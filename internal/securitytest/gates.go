package securitytest

// GateConstants are the frozen timing/size bounds of the security contract
// (hook-authorization.md §7). They are cross-checked against the machine
// manifest (docs/security/readiness-manifest.json) by
// TestGateConstantsMatchManifest so prose, manifest, and fixtures cannot
// drift apart. Changing any value is a spec-confirmation-gated contract
// change, not a tuning knob.
type GateConstants struct {
	// AbsentTrustMS: absent/insufficient trust must be answered (deny,
	// project_trust_required, zero children) within this bound.
	AbsentTrustMS int64 `json:"absent_trust_ms"`
	// RevokeCancelMS: cross-session cancellation of queued same-project work
	// after revocation.
	RevokeCancelMS int64 `json:"revoke_cancel_ms"`
	// KillEscalationMS: terminate -> kill-tree boundary for in-flight hooks.
	KillEscalationMS int64 `json:"kill_escalation_ms"`
	// DefaultTimeoutMS / MaxTimeoutMS: hook execution timeout default and the
	// maximum grantable value; a larger request is invalid_argument.
	DefaultTimeoutMS int64 `json:"default_timeout_ms"`
	MaxTimeoutMS     int64 `json:"max_timeout_ms"`
	// OutputCapBytes: combined stdout+stderr cap; excess is truncated
	// redaction-safely (RED-5).
	OutputCapBytes int64 `json:"output_cap_bytes"`
}

// Gates is the single authoritative Go value of the frozen bounds.
var Gates = GateConstants{
	AbsentTrustMS:    250,
	RevokeCancelMS:   250,
	KillEscalationMS: 2000,
	DefaultTimeoutMS: 2000,
	MaxTimeoutMS:     30000,
	OutputCapBytes:   1 << 20,
}

// RedactionContexts is the frozen RED-1 egress context set. The redaction
// fixture file must cover every entry (asserted executable-now by
// TestFixtureVectorsAreWellFormed); a new egress context added later must
// extend both in the same change.
var RedactionContexts = []string{
	"config",
	"environment",
	"hook_input",
	"hook_output",
	"error",
	"log",
	"audit",
	"snapshot",
	"agent_adapter",
	"notification",
	"diagnostics",
}
